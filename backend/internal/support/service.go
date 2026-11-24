package support

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type Service struct {
	db                           *sql.DB
	key                          []byte
	box                          cipher.AEAD
	client                       *http.Client
	apiBase, realtime, publicKey string
	telegramBase                 string
	telegramSigner               ed25519.PrivateKey
}

func Open(dsn, keyHex, apiBase, supabaseURL, publishable, signingSeed string) (*Service, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) != 32 {
		return nil, errors.New("invalid support encryption key")
	}
	u, err := url.Parse(apiBase)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return nil, errors.New("invalid Video API webhook base")
	}
	telegramBase := "https://api.telegram.org"
	var signer ed25519.PrivateKey
	if signingSeed != "" {
		seed, err := hex.DecodeString(signingSeed)
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, errors.New("invalid support Telegram signing key")
		}
		signer = ed25519.NewKeyFromSeed(seed)
		telegramBase = "https://www.zood.work/api/telegram/support"
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	box, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, errors.New("invalid support database configuration")
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	s := &Service{db: db, key: key, box: box, client: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }},
		apiBase: strings.TrimRight(apiBase, "/"), telegramBase: telegramBase, telegramSigner: signer, realtime: strings.TrimRight(supabaseURL, "/"), publicKey: publishable}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var table string
	if err = db.QueryRowContext(ctx, "SELECT 'support.telegram_bot'::regclass::text").Scan(&table); err != nil {
		db.Close()
		return nil, errors.New("support database unavailable")
	}
	return s, nil
}

func (s *Service) mac(label string) string {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte(label))
	return hex.EncodeToString(h.Sum(nil))
}
func (s *Service) Topic(session string) string { return "support-" + s.mac("broadcast/"+session) }
func (s *Service) RTEnabled() bool             { return s.realtime != "" && s.publicKey != "" }
func (s *Service) Notify(session string) {
	if !s.RTEnabled() {
		return
	}
	// Broadcast an empty wake-up event only. Each listener reloads through Video API's access checks.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		var events []map[string]any
		for _, target := range []string{session, "inbox"} {
			events = append(events, map[string]any{"topic": s.Topic(target), "event": "changed", "payload": map[string]any{}, "private": false})
		}
		body, _ := json.Marshal(map[string]any{"messages": events})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.realtime+"/realtime/v1/api/broadcast", bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("apikey", s.publicKey)
		if strings.HasPrefix(s.publicKey, "eyJ") {
			req.Header.Set("Authorization", "Bearer "+s.publicKey)
		}
		resp, err := s.client.Do(req)
		if err != nil {
			log.Print("Support Realtime broadcast unavailable")
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			log.Printf("Support Realtime HTTP %d", resp.StatusCode)
		}
	}()
}

func (s *Service) GuestAllowed(ctx context.Context, id string) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM public.user_profiles WHERE id=$1)
	  AND NOT EXISTS(SELECT 1 FROM public.messages WHERE session_id=$1 AND user_id IS NOT NULL)`, id).Scan(&ok)
	return ok, err
}

type Thread struct {
	ID           string    `json:"id"`
	Content      string    `json:"content"`
	Sender       string    `json:"sender"`
	At           time.Time `json:"at"`
	Member       bool      `json:"member"`
	CustomerName string    `json:"customer_name"`
}

// CustomerName uses the account recorded on the customer's message. Session IDs
// also exist for anonymous conversations and are not proof of account ownership.
func CustomerName(session string, member bool, displayName, username string) string {
	label := "网站访客"
	if member {
		label = "已登录用户"
		for _, value := range []string{displayName, username} {
			name := []rune(strings.Join(strings.Fields(value), " "))
			if len(name) > 0 {
				return string(name[:min(len(name), 80)])
			}
		}
	}
	return label + " · " + session[:min(len(session), 8)]
}

func (s *Service) Threads(ctx context.Context, page int) ([]Thread, bool, error) {
	rows, err := s.db.QueryContext(ctx, `WITH latest AS (
	  SELECT DISTINCT ON(session_id) session_id,id,content,sender_type,created_at FROM public.messages
	  ORDER BY session_id,created_at DESC,id DESC
	), page AS (SELECT * FROM latest ORDER BY created_at DESC,id DESC LIMIT 31 OFFSET $1)
	SELECT t.session_id,t.content,t.sender_type,t.created_at,customer.user_id IS NOT NULL,
	       COALESCE(p.display_name,''),COALESCE(p.username,'')
	FROM page t LEFT JOIN LATERAL (
	  SELECT m.user_id FROM public.messages m WHERE m.session_id=t.session_id AND m.sender_type='user' AND m.user_id IS NOT NULL
	  ORDER BY m.created_at DESC,m.id DESC LIMIT 1
	) customer ON true
	LEFT JOIN public.user_profiles p ON p.id=customer.user_id
	ORDER BY t.created_at DESC,t.id DESC`, (page-1)*30)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []Thread{}
	for rows.Next() {
		var r Thread
		var displayName, username string
		if err = rows.Scan(&r.ID, &r.Content, &r.Sender, &r.At, &r.Member, &displayName, &username); err != nil {
			return nil, false, err
		}
		r.CustomerName = CustomerName(r.ID, r.Member, displayName, username)
		out = append(out, r)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	if len(out) > 30 {
		return out[:30], true, nil
	}
	return out, false, nil
}

type Message struct {
	ID           string    `json:"id"`
	Sender       string    `json:"sender_type"`
	Content      string    `json:"content"`
	At           time.Time `json:"created_at"`
	CustomerName string    `json:"customer_name,omitempty"`
}

func (s *Service) History(ctx context.Context, session, before string) ([]Message, bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.id,m.sender_type,m.content,m.created_at,m.user_id IS NOT NULL,
	   COALESCE(p.display_name,''),COALESCE(p.username,'')
	   FROM public.messages m LEFT JOIN public.user_profiles p ON p.id=m.user_id
	   WHERE m.session_id=$1 AND ($2='' OR (m.created_at,m.id)<(SELECT created_at,id FROM public.messages WHERE session_id=$1 AND id=nullif($2,'')::uuid))
	   ORDER BY m.created_at DESC,m.id DESC LIMIT 61`, session, before)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		var member bool
		var displayName, username string
		if err = rows.Scan(&m.ID, &m.Sender, &m.Content, &m.At, &member, &displayName, &username); err != nil {
			return nil, false, err
		}
		if m.Sender == "user" {
			m.CustomerName = CustomerName(session, member, displayName, username)
		}
		out = append(out, m)
	}
	more := len(out) > 60
	if more {
		out = out[:60]
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, more, rows.Err()
}

func (s *Service) Reply(ctx context.Context, session, content string) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO public.messages(session_id,sender_type,content)
	  SELECT $1,'admin',$2 WHERE EXISTS(SELECT 1 FROM public.messages WHERE session_id=$1)`, session, content)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	if err == nil {
		s.Notify(session)
	}
	return err
}

type bot struct {
	ID              int64
	Username, Token string
}

func (s *Service) bot(ctx context.Context) (bot, error) {
	var b bot
	var sealed []byte
	err := s.db.QueryRowContext(ctx, "SELECT bot_id,username,encrypted_token FROM support.telegram_bot WHERE singleton").Scan(&b.ID, &b.Username, &sealed)
	if err != nil {
		return b, err
	}
	n := s.box.NonceSize()
	if len(sealed) < n {
		return b, errors.New("invalid bot configuration")
	}
	plain, err := s.box.Open(nil, sealed[:n], sealed[n:], nil)
	b.Token = string(plain)
	return b, err
}
func (s *Service) HasBot(ctx context.Context) bool {
	var present bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM support.telegram_bot WHERE singleton)").Scan(&present)
	return err == nil && present
}

func (s *Service) Config(ctx context.Context) (map[string]any, error) {
	var name string
	var bound int
	err := s.db.QueryRowContext(ctx, `SELECT username,(SELECT count(*) FROM support.telegram_bindings b WHERE b.bot_id=t.bot_id)
	  FROM support.telegram_bot t WHERE singleton`).Scan(&name, &bound)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return map[string]any{"configured": name != "", "username": name, "bindings": bound, "realtime": s.RTEnabled()}, err
}

var BotTokenPattern = regexp.MustCompile(`^[0-9]{4,20}:[A-Za-z0-9_-]{24,128}$`)
var (
	ErrTelegram        = errors.New("Telegram service unavailable")
	ErrTelegramToken   = errors.New("Telegram rejected the bot token")
	ErrTelegramWebhook = errors.New("Telegram rejected the webhook")
	ErrTelegramBusy    = errors.New("Telegram rate limit reached")
	ErrTelegramBot     = errors.New("Telegram bot identity is incomplete")
)

func (s *Service) tg(ctx context.Context, token, method string, input any, result any) (failure error) {
	status := 0
	defer func() {
		if failure != nil {
			// A transport error's URL can contain the token. Log only classified errors.
			log.Printf("Support Telegram %s status=%d: %v", method, status, failure)
		}
	}()
	endpoint := s.telegramBase
	if s.telegramSigner != nil {
		input = map[string]any{"method": method, "token": token, "payload": input}
	} else {
		endpoint += "/bot" + token + "/" + method
	}
	body, err := json.Marshal(input)
	if err != nil {
		return ErrTelegram
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ErrTelegram
	}
	req.Header.Set("Content-Type", "application/json")
	if s.telegramSigner != nil {
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		signature := ed25519.Sign(s.telegramSigner, append([]byte(stamp+"\n"), body...))
		req.Header.Set("X-Zood-Time", stamp)
		req.Header.Set("X-Zood-Signature", base64.StdEncoding.EncodeToString(signature))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return ErrTelegram
	}
	defer resp.Body.Close()
	status = resp.StatusCode
	var data struct {
		OK        bool            `json:"ok"`
		Result    json.RawMessage `json:"result"`
		ErrorCode int             `json:"error_code"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 128<<10)).Decode(&data) != nil {
		return ErrTelegram
	}
	if !data.OK {
		switch {
		case data.ErrorCode == 429:
			return ErrTelegramBusy
		case data.ErrorCode == 401 || (method == "getMe" && data.ErrorCode == 404):
			return ErrTelegramToken
		case method == "setWebhook" && (data.ErrorCode == 400 || data.ErrorCode == 403):
			return ErrTelegramWebhook
		default:
			return ErrTelegram
		}
	}
	if status/100 != 2 {
		return ErrTelegram
	}
	if result != nil && json.Unmarshal(data.Result, result) != nil {
		return ErrTelegram
	}
	return nil
}

func (s *Service) Configure(ctx context.Context, token string) error {
	if !BotTokenPattern.MatchString(token) {
		return ErrTelegramToken
	}
	var identity struct {
		ID              int64    `json:"id"`
		Username        string   `json:"username"`
		ActiveUsernames []string `json:"active_usernames"`
		IsBot           bool     `json:"is_bot"`
	}
	if err := s.tg(ctx, token, "getMe", map[string]any{}, &identity); err != nil {
		return err
	}
	if identity.Username == "" && len(identity.ActiveUsernames) != 0 {
		identity.Username = identity.ActiveUsernames[0]
	}
	if !identity.IsBot || identity.ID <= 0 || identity.Username == "" {
		return ErrTelegramBot
	}
	nonce := make([]byte, s.box.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	sealed := s.box.Seal(nonce, nonce, []byte(token), nil)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext('support.bot.configure'))"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO support.telegram_bot(singleton,bot_id,username,encrypted_token) VALUES(true,$1,$2,$3)
	  ON CONFLICT(singleton) DO UPDATE SET bot_id=excluded.bot_id,username=excluded.username,encrypted_token=excluded.encrypted_token,
	  bind_hash=NULL,bind_expires=NULL`, identity.ID, identity.Username, sealed); err != nil {
		return err
	}
	var registered bool
	err = s.tg(ctx, token, "setWebhook", map[string]any{"url": s.apiBase + "/api/platform/telegram/webhook", "secret_token": s.mac(fmt.Sprintf("telegram/%d", identity.ID)), "allowed_updates": []string{"message"}, "drop_pending_updates": false}, &registered)
	if err != nil {
		return err
	}
	if !registered {
		return ErrTelegramWebhook
	}
	return tx.Commit()
}
func (s *Service) Binding(ctx context.Context) (string, error) {
	var buf [24]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	code := base64.RawURLEncoding.EncodeToString(buf[:])
	hash := sha256.Sum256([]byte(code))
	var name string
	err := s.db.QueryRowContext(ctx, `UPDATE support.telegram_bot SET bind_hash=$1,bind_expires=now()+interval '10 minutes'
	  WHERE singleton RETURNING username`, hash[:]).Scan(&name)
	if err != nil {
		return "", err
	}
	return "https://t.me/" + name + "?start=" + code, nil
}
