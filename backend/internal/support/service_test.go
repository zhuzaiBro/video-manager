package support

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Run against a disposable platform-schema fixture with migration 008 applied.
func TestTelegramInboxRoundTrip(t *testing.T) {
	dsn := os.Getenv("SUPPORT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("no disposable support database")
	}
	ctx := context.Background()
	s, err := Open(dsn, strings.Repeat("01", 32), "https://fixture.example.test", "", "")
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
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		_ = json.NewDecoder(r.Body).Decode(&input)
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			io.WriteString(w, `{"ok":true,"result":{"id":54321,"username":"example_test_bot","is_bot":true}}`)
		case strings.HasSuffix(r.URL.Path, "/setWebhook"):
			webhookSecret, _ = input["secret_token"].(string)
			if input["url"] != "https://fixture.example.test/api/platform/telegram/webhook" {
				t.Error("wrong webhook URL")
			}
			io.WriteString(w, `{"ok":true,"result":true}`)
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			io.WriteString(w, `{"ok":true,"result":{"message_id":42}}`)
		default:
			t.Error("unknown Telegram method")
		}
	}))
	defer api.Close()
	s.telegramBase = api.URL
	token := "54321:" + strings.Repeat("example", 5)
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
