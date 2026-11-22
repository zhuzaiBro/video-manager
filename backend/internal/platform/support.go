package platform

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	supportTopicPrefix    = "support.v1."
	supportRealtimeHeader = "support-realtime"
	supportConfigHeader   = "support-config"
	supportTokenTTL       = 10 * time.Minute
)

var telegramTokenPattern = regexp.MustCompile(`^[0-9]{6,15}:[A-Za-z0-9_-]{30,80}$`)

// SupportDelivery is one private broadcast queued after a message is stored.
type SupportDelivery struct {
	MessageID string
	Topic     string
	Payload   map[string]any
	Attempts  int
}

// TelegramTokenRecord is the encrypted token. Plaintext is never stored here.
type TelegramTokenRecord struct {
	Configured bool
	Ciphertext []byte
	Nonce      []byte
	Hint       string
}

// SupportStore is the local business database. It is not the remote Supabase project.
type SupportStore interface {
	ReadTelegramToken(ctx context.Context) (TelegramTokenRecord, error)
	SaveTelegramToken(ctx context.Context, ciphertext, nonce []byte, hint, updatedBy string) error
	ClearTelegramToken(ctx context.Context, updatedBy string) error
	EnqueueSupport(ctx context.Context, deliveries []SupportDelivery) error
	ClaimSupport(ctx context.Context, limit int) ([]SupportDelivery, error)
	FinishSupport(ctx context.Context, messageID, topic string, delivered bool) error
}

// SupportPublisher sends an already authorized private broadcast.
type SupportPublisher interface {
	Publish(ctx context.Context, topic string, payload map[string]any) error
}

// SupportConfig wires local storage to the remote Realtime publisher.
// Empty keys leave the feature unavailable; they are never invented here.
type SupportConfig struct {
	Store          SupportStore
	ConfigKey      []byte
	ChannelKey     []byte
	JWTSecret      string
	Issuer         string
	Publisher      SupportPublisher
	SupabaseURL    string
	ServiceRoleKey string
}

// TelegramConfigView is the only token shape returned to an admin browser.
type TelegramConfigView struct {
	Configured bool    `json:"configured"`
	Hint       *string `json:"hint"`
}

type telegramTokenUpdate struct {
	keep  bool
	clear bool
	token string
}

// DecodeKey accepts a standard base64 32-byte key. Invalid or empty input
// returns nil and never echoes the supplied text.
func DecodeKey(value string) []byte {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil || len(decoded) != 32 {
		return nil
	}
	return decoded
}

func EncryptToken(key []byte, plaintext string) (ciphertext, nonce []byte, hint string, err error) {
	if len(key) != 32 {
		return nil, nil, "", NewError(http.StatusServiceUnavailable, "CONFIG_KEY_UNAVAILABLE", "服务端还没有配置 SUPPORT_CONFIG_KEY")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, "", NewError(http.StatusServiceUnavailable, "CONFIG_KEY_UNAVAILABLE", "服务端还没有配置 SUPPORT_CONFIG_KEY")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, "", NewError(http.StatusServiceUnavailable, "CONFIG_KEY_UNAVAILABLE", "服务端还没有配置 SUPPORT_CONFIG_KEY")
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, "", NewError(http.StatusServiceUnavailable, "CONFIG_KEY_UNAVAILABLE", "客服配置暂时不能保存")
	}
	ciphertext = gcm.Seal(nil, nonce, []byte(plaintext), nil)
	hint = tokenHint(plaintext)
	return ciphertext, nonce, hint, nil
}

func DecryptToken(key, ciphertext, nonce []byte) (string, error) {
	if len(key) != 32 || len(ciphertext) == 0 || len(nonce) == 0 {
		return "", NewError(http.StatusServiceUnavailable, "CONFIG_KEY_UNAVAILABLE", "服务端还没有配置 SUPPORT_CONFIG_KEY")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", NewError(http.StatusServiceUnavailable, "CONFIG_KEY_UNAVAILABLE", "服务端还没有配置 SUPPORT_CONFIG_KEY")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", NewError(http.StatusServiceUnavailable, "CONFIG_KEY_UNAVAILABLE", "服务端还没有配置 SUPPORT_CONFIG_KEY")
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", NewError(http.StatusServiceUnavailable, "CONFIG_KEY_UNAVAILABLE", "客服配置无法读取")
	}
	return string(plain), nil
}

func tokenHint(token string) string {
	parts := strings.SplitN(token, ":", 2)
	if len(parts) != 2 || len(parts[1]) < 8 {
		return ""
	}
	return parts[1][len(parts[1])-4:]
}

func parseTelegramTokenUpdate(body map[string]any) (telegramTokenUpdate, error) {
	clear, _ := body["clearTelegramBotToken"].(bool)
	raw, present := body["telegramBotToken"]
	token := ""
	if present && raw != nil {
		value, ok := raw.(string)
		if !ok {
			return telegramTokenUpdate{}, invalid("token 格式不正确")
		}
		token = strings.TrimSpace(value)
	}
	if clear && token != "" {
		return telegramTokenUpdate{}, invalid("清空和更新 token 需要分开提交")
	}
	if clear {
		return telegramTokenUpdate{clear: true}, nil
	}
	if token == "" {
		return telegramTokenUpdate{keep: true}, nil
	}
	if !telegramTokenPattern.MatchString(token) {
		return telegramTokenUpdate{}, invalid("token 格式不正确")
	}
	return telegramTokenUpdate{token: token}, nil
}

func SessionTopic(key []byte, sessionID string) string {
	return supportTopic(key, "session:"+strings.ToLower(sessionID))
}

func DeskTopic(key []byte) string {
	return supportTopic(key, "desk")
}

func supportTopic(key []byte, subject string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(subject))
	return supportTopicPrefix + hex.EncodeToString(mac.Sum(nil))
}

func topicAuthorized(claim, topic string) bool {
	if claim == "" || len(claim) != len(topic) || !strings.HasPrefix(topic, supportTopicPrefix) {
		return false
	}
	return hmac.Equal([]byte(claim), []byte(topic))
}

func mintSupportToken(secret, issuer, topic string, now time.Time) (string, time.Time, error) {
	if secret == "" || issuer == "" || !topicAuthorized(topic, topic) {
		return "", time.Time{}, NewError(http.StatusServiceUnavailable, "REALTIME_UNAVAILABLE", "服务端还没有配置实时频道")
	}
	expires := now.Add(supportTokenTTL).UTC()
	claims := jwt.MapClaims{
		"iss":  issuer,
		"aud":  "authenticated",
		"exp":  expires.Unix(),
		"iat":  now.UTC().Unix(),
		"sub":  uuid.NewString(),
		"role": "anon",
		"app_metadata": map[string]any{
			"support_topic": topic,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", time.Time{}, NewError(http.StatusServiceUnavailable, "REALTIME_UNAVAILABLE", "服务端还没有配置实时频道")
	}
	return signed, expires, nil
}

func supportClaimTopic(tokenString, secret string) (string, string, error) {
	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, invalid("登录状态无效")
		}
		return []byte(secret), nil
	})
	if err != nil || !parsed.Valid {
		return "", "", err
	}
	role, _ := claims["role"].(string)
	sub, _ := claims["sub"].(string)
	meta, _ := claims["app_metadata"].(map[string]any)
	topic, _ := meta["support_topic"].(string)
	if role == "" {
		return "", "", invalid("登录状态无效")
	}
	return role + ":" + sub, topic, nil
}

func publicTokenView(record TelegramTokenRecord) TelegramConfigView {
	view := TelegramConfigView{Configured: record.Configured}
	if record.Hint != "" {
		hint := record.Hint
		view.Hint = &hint
	}
	return view
}

func (s *Service) SetSupport(cfg SupportConfig) {
	if cfg.Publisher == nil && cfg.SupabaseURL != "" && cfg.ServiceRoleKey != "" {
		cfg.Publisher = NewBroadcast(cfg.SupabaseURL, cfg.ServiceRoleKey)
	}
	cfg.ServiceRoleKey = ""
	s.support = cfg
}

func (s *Service) supportReady() bool {
	return s.support.Store != nil && len(s.support.ChannelKey) == 32 && s.support.JWTSecret != "" && s.support.Issuer != "" && s.support.Publisher != nil
}

func (s *Service) GetTelegramConfig(ctx context.Context, caller Caller) (TelegramConfigView, error) {
	if err := s.requireAdmin(ctx, caller); err != nil {
		return TelegramConfigView{}, err
	}
	if s.support.Store == nil {
		return TelegramConfigView{}, NewError(http.StatusServiceUnavailable, "SUPPORT_UNAVAILABLE", "客服配置还没有接入本地库")
	}
	record, err := s.support.Store.ReadTelegramToken(ctx)
	if err != nil {
		return TelegramConfigView{}, err
	}
	return publicTokenView(record), nil
}

func (s *Service) UpdateTelegramConfig(ctx context.Context, caller Caller, body map[string]any) (TelegramConfigView, error) {
	if err := s.requireAdmin(ctx, caller); err != nil {
		return TelegramConfigView{}, err
	}
	if s.support.Store == nil {
		return TelegramConfigView{}, NewError(http.StatusServiceUnavailable, "SUPPORT_UNAVAILABLE", "客服配置还没有接入本地库")
	}
	update, err := parseTelegramTokenUpdate(body)
	if err != nil {
		return TelegramConfigView{}, err
	}
	if update.keep {
		return s.GetTelegramConfig(ctx, caller)
	}
	if update.clear {
		if err := s.support.Store.ClearTelegramToken(ctx, caller.UserID); err != nil {
			return TelegramConfigView{}, err
		}
		return s.GetTelegramConfig(ctx, caller)
	}
	ciphertext, nonce, hint, err := EncryptToken(s.support.ConfigKey, update.token)
	if err != nil {
		return TelegramConfigView{}, err
	}
	if err := s.support.Store.SaveTelegramToken(ctx, ciphertext, nonce, hint, caller.UserID); err != nil {
		return TelegramConfigView{}, err
	}
	return TelegramConfigView{Configured: true, Hint: hintPointer(hint)}, nil
}

func hintPointer(hint string) *string {
	if hint == "" {
		return nil
	}
	return &hint
}

func (s *Service) SupportRealtimeToken(ctx context.Context, caller Caller, sessionID, scope string) (map[string]any, error) {
	if !s.supportReady() {
		return nil, NewError(http.StatusServiceUnavailable, "REALTIME_UNAVAILABLE", "服务端还没有配置实时频道")
	}
	scope = strings.TrimSpace(scope)
	var topic string
	switch scope {
	case "":
		if err := s.authorizeSupportSession(ctx, caller, sessionID, true); err != nil {
			return nil, err
		}
		topic = SessionTopic(s.support.ChannelKey, sessionID)
	case "desk":
		if err := s.requireAdmin(ctx, caller); err != nil {
			return nil, err
		}
		topic = DeskTopic(s.support.ChannelKey)
	default:
		return nil, invalid("实时范围不合法")
	}
	token, expires, err := mintSupportToken(s.support.JWTSecret, s.support.Issuer, topic, time.Now())
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"topic":     topic,
		"token":     token,
		"expiresAt": expires.Format(time.RFC3339),
	}, nil
}

func (s *Service) authorizeSupportSession(ctx context.Context, caller Caller, sessionID string, allowAdmin bool) error {
	if !IsUUID(sessionID) {
		return invalid("会话标识不合法")
	}
	if caller.SignedIn() {
		if strings.EqualFold(sessionID, caller.UserID) {
			return nil
		}
		if !allowAdmin {
			return forbidden("会话身份不匹配")
		}
		return s.requireAdmin(ctx, caller)
	}
	belongs, err := s.sessionBelongsToProfile(ctx, sessionID)
	if err != nil {
		return err
	}
	if belongs {
		return forbidden("会话身份不匹配")
	}
	return nil
}

func (s *Service) sessionBelongsToProfile(ctx context.Context, sessionID string) (bool, error) {
	result, err := s.source.Select(ctx, Caller{}, Query{
		Table:   "user_profiles",
		Select:  "id",
		Filters: []Filter{{Column: "id", Op: "eq", Value: sessionID}},
		Limit:   1,
	})
	if err != nil {
		return false, err
	}
	for _, row := range result.Rows {
		if strings.EqualFold(stringField(row, "id"), sessionID) {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) publishSupportMessage(ctx context.Context, row map[string]any) {
	if !s.supportReady() {
		return
	}
	messageID := stringField(row, "id")
	sessionID := stringField(row, "session_id")
	if !IsUUID(messageID) || !IsUUID(sessionID) {
		return
	}
	payload := map[string]any{
		"id":          messageID,
		"session_id":  sessionID,
		"sender_type": stringField(row, "sender_type"),
		"content":     stringField(row, "content"),
		"created_at":  supportTime(row["created_at"]),
	}
	deliveries := []SupportDelivery{
		{MessageID: messageID, Topic: SessionTopic(s.support.ChannelKey, sessionID), Payload: payload},
		{MessageID: messageID, Topic: DeskTopic(s.support.ChannelKey), Payload: payload},
	}
	if err := s.support.Store.EnqueueSupport(ctx, deliveries); err != nil {
		return
	}
	s.flushSupport(ctx)
}

func (s *Service) flushSupport(ctx context.Context) {
	if !s.supportReady() {
		return
	}
	deliveries, err := s.support.Store.ClaimSupport(ctx, 20)
	if err != nil {
		return
	}
	for _, delivery := range deliveries {
		err := s.support.Publisher.Publish(ctx, delivery.Topic, delivery.Payload)
		_ = s.support.Store.FinishSupport(ctx, delivery.MessageID, delivery.Topic, err == nil)
	}
}

func (s *Service) telegramSender(ctx context.Context) TelegramSender {
	if s.support.Store == nil {
		return s.telegram
	}
	record, err := s.support.Store.ReadTelegramToken(ctx)
	if err != nil || !record.Configured {
		return s.telegram
	}
	plain, err := DecryptToken(s.support.ConfigKey, record.Ciphertext, record.Nonce)
	if err != nil || plain == "" {
		return deniedTelegramSender{}
	}
	return NewTelegram(plain)
}

type deniedTelegramSender struct{}

func (deniedTelegramSender) Send(context.Context, int64, string) (int64, error) {
	return 0, NewError(http.StatusServiceUnavailable, "TELEGRAM_UNAVAILABLE", "Telegram 发送配置不可用")
}

func supportTime(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano)
	default:
		return ""
	}
}
