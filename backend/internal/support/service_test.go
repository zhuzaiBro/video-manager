package support

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Run against a disposable platform-schema fixture with migration 008 applied.
func TestTelegramInboxRoundTrip(t *testing.T) {
	dsn := os.Getenv("SUPPORT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("no disposable support database")
	}
	ctx := context.Background()
	s, err := Open(dsn, strings.Repeat("01", 32), "https://fixture.example.test", "", "", strings.Repeat("02", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	admin, err := sql.Open("pgx", os.Getenv("SUPPORT_TEST_FIXTURE_ADMIN"))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err = admin.Exec("TRUNCATE support.telegram_updates,support.telegram_bot,support.telegram_bindings,support.telegram_deliveries"); err != nil {
		t.Fatal(err)
	}
	var webhookSecret string
	token := "54321:" + strings.Repeat("example", 5)
	rejectedToken := "43210:" + strings.Repeat("fixture", 5)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		stamp := r.Header.Get("X-Zood-Time")
		seconds, _ := strconv.ParseInt(stamp, 10, 64)
		signature, _ := base64.StdEncoding.DecodeString(r.Header.Get("X-Zood-Signature"))
		if r.URL.RequestURI() != "/support" || time.Now().Unix()-seconds > 60 ||
			!ed25519.Verify(s.telegramSigner.Public().(ed25519.PublicKey), append([]byte(stamp+"\n"), body...), signature) {
			t.Error("invalid relay request or signature")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var call struct {
			Method, Token string
			Payload       map[string]any
		}
		if json.Unmarshal(body, &call) != nil || (call.Token != token && call.Token != rejectedToken) {
			t.Error("invalid signed envelope")
			return
		}
		switch call.Method {
		case "getMe":
			if call.Token == rejectedToken {
				io.WriteString(w, `{"ok":true,"result":{"id":43210,"username":"other_bot","is_bot":true}}`)
				return
			}
			io.WriteString(w, `{"ok":true,"result":{"id":54321,"active_usernames":["example_test_bot"],"is_bot":true}}`)
		case "setWebhook":
			if call.Token == rejectedToken {
				io.WriteString(w, `{"ok":true,"result":false}`)
				return
			}
			webhookSecret, _ = call.Payload["secret_token"].(string)
			if call.Payload["url"] != "https://fixture.example.test/api/platform/telegram/webhook" {
				t.Error("wrong webhook URL")
			}
			io.WriteString(w, `{"ok":true,"result":true}`)
		case "sendMessage":
			io.WriteString(w, `{"ok":true,"result":{"message_id":42}}`)
		default:
			t.Error("unknown Telegram method")
		}
	}))
	defer api.Close()
	s.telegramBase = api.URL + "/support"
	if err = s.Configure(ctx, token); err != nil {
		t.Fatal(err)
	}
	conf, err := s.Config(ctx)
	if err != nil || conf["username"] != "example_test_bot" {
		t.Fatalf("invalid status: %v", err)
	}
	v, _ := json.Marshal(conf)
	if strings.Contains(string(v), token) {
		t.Fatal("bot token visible in admin response")
	}
	bind, err := s.Binding(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Configure(ctx, rejectedToken); !errors.Is(err, ErrTelegramWebhook) {
		t.Fatalf("unconfirmed webhook: %v", err)
	}
	bot, err := s.bot(ctx)
	if err != nil || bot.Token != token {
		t.Fatal("failed replacement changed the saved bot")
	}
	link, _ := url.Parse(bind)
	message := func(id int, content string, reply bool) []byte {
		m := map[string]any{"message_id": id, "chat": map[string]any{"id": 20, "type": "private"}, "from": map[string]any{"id": 20}, "text": content}
		if reply {
			m["reply_to_message"] = map[string]int{"message_id": 42}
		}
		out, _ := json.Marshal(map[string]any{"update_id": id, "message": m})
		return out
	}
	if err = s.Webhook(ctx, webhookSecret, message(1, "/start "+link.Query().Get("start"), false)); err != nil {
		t.Fatal(err)
	}
	session := uuid.NewString()
	defer admin.Exec("DELETE FROM public.messages WHERE session_id=$1", session)
	if _, err = admin.Exec("INSERT INTO public.messages(session_id,sender_type,content) VALUES($1,'user','问课程')", session); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Deliver(ctx); !ok || err != nil {
		t.Fatalf("delivery: %v %v", ok, err)
	}
	if ok, err := s.Deliver(ctx); ok || err != nil {
		t.Fatalf("duplicate: %v %v", ok, err)
	}
	for i := 0; i < 2; i++ {
		if err = s.Webhook(ctx, webhookSecret, message(2, "可以先试一节。", true)); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Reply(ctx, session, "后台补充"); err != nil {
		t.Fatal(err)
	}
	h, more, err := s.History(ctx, session, "")
	if err != nil || more || len(h) != 3 || h[1].Content != "可以先试一节。" {
		t.Fatalf("wrong conversation history: %d %v", len(h), err)
	}
	h, _, err = s.History(ctx, session, h[1].ID)
	if err != nil || len(h) != 1 {
		t.Fatal("history cursor")
	}
	threads, _, err := s.Threads(ctx, 1)
	if err != nil || len(threads) == 0 || threads[0].ID != session {
		t.Fatal("inbox missing conversation", err)
	}
}

func TestTelegramFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name, method, response string
		status                 int
		want                   error
	}{
		{"rejected token", "getMe", `{"ok":false,"error_code":401}`, 401, ErrTelegramToken},
		{"unknown token", "getMe", `{"ok":false,"error_code":404}`, 404, ErrTelegramToken},
		{"invalid callback", "setWebhook", `{"ok":false,"error_code":400}`, 400, ErrTelegramWebhook},
		{"rate limit", "getMe", `{"ok":false,"error_code":429}`, 429, ErrTelegramBusy},
		{"relay unauthorized", "getMe", "", 401, ErrTelegram},
		{"relay offline", "getMe", "Bad Gateway", 502, ErrTelegram},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/botfixture/"+tc.method || r.Header.Get("X-Zood-Signature") != "" {
					t.Error("unexpected direct Telegram request")
				}
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.response)
			}))
			defer api.Close()
			s := Service{client: api.Client(), telegramBase: api.URL}
			if got := s.tg(context.Background(), "fixture", tc.method, nil, nil); !errors.Is(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
