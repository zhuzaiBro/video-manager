package platform

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zood/video-manager/internal/auth"
	"github.com/zood/video-manager/internal/config"
)

const (
	supportAdminID = "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10"
	supportUserID  = "11111111-1111-4111-8111-111111111111"
	supportOtherID = "22222222-2222-4222-8222-222222222222"
	supportGuestID = "33333333-3333-4333-8333-333333333333"
	supportMessage = "44444444-4444-4444-8444-444444444444"
	sampleBotToken = "123456789:AAH12345678901234567890123456789012"
)

func TestTelegramTokenStaysEncryptedAndBlankDoesNotClear(t *testing.T) {
	store := newMemorySupportStore()
	recorder := &recordingSender{}
	router, _ := supportRouter(t, filteringSource{fakeSource: adminSource(true)}, store, &scriptedPublisher{}, recorder)
	header := map[string]string{"X-Zood-Request": supportConfigHeader}

	if supportPerform(router, http.MethodGet, "/api/platform/admin/support/telegram", "", "", nil).Code != http.StatusUnauthorized {
		t.Fatal("anonymous config read was accepted")
	}
	missing := supportPerform(router, http.MethodPut, "/api/platform/admin/support/telegram", `{"telegramBotToken":"`+sampleBotToken+`"}`, "", header)
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d body %s", missing.Code, missing.Body.String())
	}
	normal := supportPerform(router, http.MethodPut, "/api/platform/admin/support/telegram", `{"telegramBotToken":"`+sampleBotToken+`"}`, signUser(t, supportUserID), header)
	if normal.Code != http.StatusForbidden {
		t.Fatalf("user status = %d body %s", normal.Code, normal.Body.String())
	}
	noHeader := supportPerform(router, http.MethodPut, "/api/platform/admin/support/telegram", `{"telegramBotToken":"`+sampleBotToken+`"}`, signUser(t, supportAdminID), nil)
	if noHeader.Code != http.StatusForbidden || !strings.Contains(noHeader.Body.String(), "CSRF_REJECTED") {
		t.Fatalf("missing header status = %d body %s", noHeader.Code, noHeader.Body.String())
	}

	saved := supportPerform(router, http.MethodPut, "/api/platform/admin/support/telegram", `{"telegramBotToken":"`+sampleBotToken+`"}`, signUser(t, supportAdminID), header)
	if saved.Code != http.StatusOK {
		t.Fatalf("save status = %d body %s", saved.Code, saved.Body.String())
	}
	if strings.Contains(saved.Body.String(), sampleBotToken) || strings.Contains(string(store.snapshot().Ciphertext), sampleBotToken) {
		t.Fatal("plaintext token was returned or stored")
	}
	if !strings.Contains(saved.Body.String(), `"configured":true`) || !strings.Contains(saved.Body.String(), `"hint":"9012"`) {
		t.Fatalf("mask = %s", saved.Body.String())
	}
	if recorder.calls() != 0 {
		t.Fatalf("saving the token sent %d telegram messages", recorder.calls())
	}

	before := append([]byte(nil), store.snapshot().Ciphertext...)
	kept := supportPerform(router, http.MethodPut, "/api/platform/admin/support/telegram", `{"telegramBotToken":"   "}`, signUser(t, supportAdminID), header)
	if kept.Code != http.StatusOK || !bytes.Equal(store.snapshot().Ciphertext, before) {
		t.Fatalf("blank update changed the token: %s", kept.Body.String())
	}
	both := supportPerform(router, http.MethodPut, "/api/platform/admin/support/telegram", `{"telegramBotToken":"`+sampleBotToken+`","clearTelegramBotToken":true}`, signUser(t, supportAdminID), header)
	if both.Code != http.StatusBadRequest {
		t.Fatalf("combined update status = %d", both.Code)
	}
	cleared := supportPerform(router, http.MethodPut, "/api/platform/admin/support/telegram", `{"clearTelegramBotToken":true}`, signUser(t, supportAdminID), header)
	if cleared.Code != http.StatusOK || strings.Contains(cleared.Body.String(), `"configured":true`) || recorder.calls() != 0 {
		t.Fatalf("clear = %s calls %d", cleared.Body.String(), recorder.calls())
	}
}

func TestSupportRealtimeIsolatesSessionsAndRecoversPublish(t *testing.T) {
	store := newMemorySupportStore()
	publisher := &scriptedPublisher{failTimes: 2}
	source := &fakeSource{tables: map[string][]map[string]any{
		"user_profiles": {{"id": supportAdminID, "is_admin": true}, {"id": supportUserID, "is_admin": false}},
	}, writeRow: map[string]any{
		"id": supportMessage, "session_id": supportGuestID, "sender_type": "user", "content": "hello", "created_at": "2026-11-22T00:00:00Z",
	}}
	router, service := supportRouter(t, filteringSource{fakeSource: source}, store, publisher, &recordingSender{})
	header := map[string]string{"X-Zood-Request": supportRealtimeHeader}

	if err := ValidateQuery(Query{Table: "support_settings", Select: "telegram_bot_token_ciphertext"}); err == nil {
		t.Fatal("generic query accepted the token table")
	}
	if supportPerform(router, http.MethodPost, "/api/platform/messages/realtime", `{"sessionId":"`+supportGuestID+`"}`, "", nil).Code != http.StatusForbidden {
		t.Fatal("realtime without the request header was accepted")
	}
	cross := supportPerform(router, http.MethodPost, "/api/platform/messages/realtime", `{"sessionId":"`+supportUserID+`"}`, "", header)
	if cross.Code != http.StatusForbidden {
		t.Fatalf("anonymous user session status = %d body %s", cross.Code, cross.Body.String())
	}
	guest := supportPerform(router, http.MethodPost, "/api/platform/messages/realtime", `{"sessionId":"`+supportGuestID+`"}`, "", header)
	if guest.Code != http.StatusOK {
		t.Fatalf("guest status = %d body %s", guest.Code, guest.Body.String())
	}
	guestView := decodeSupport(t, guest.Body.Bytes())
	if strings.Contains(guestView["topic"].(string), supportGuestID) {
		t.Fatalf("topic contains the session id: %s", guestView["topic"])
	}
	userToken := signUser(t, supportUserID)
	other := supportPerform(router, http.MethodPost, "/api/platform/messages/realtime", `{"sessionId":"`+supportGuestID+`"}`, userToken, header)
	if other.Code != http.StatusForbidden {
		t.Fatalf("user reading guest session status = %d", other.Code)
	}
	own := supportPerform(router, http.MethodPost, "/api/platform/messages/realtime", `{"sessionId":"`+supportUserID+`"}`, userToken, header)
	if own.Code != http.StatusOK {
		t.Fatalf("own session status = %d body %s", own.Code, own.Body.String())
	}
	ownView := decodeSupport(t, own.Body.Bytes())
	if topicAuthorized(ownView["topic"].(string), guestView["topic"].(string)) {
		t.Fatal("user A token authorizes user B topic")
	}
	validator := auth.NewValidator(&config.Config{
		SupabaseJWTSecret: "test-secret",
		SupabaseURL:       "https://gmfuorleragagsqbkajh.supabase.co",
	})
	if _, err := validator.ParseSupabaseToken(ownView["token"].(string)); err == nil {
		t.Fatal("realtime token was accepted as a platform session")
	}
	role, claim, err := supportClaimTopic(ownView["token"].(string), "test-secret")
	if err != nil || !strings.HasPrefix(role, "anon:") || claim != ownView["topic"].(string) || strings.Contains(role, supportUserID) {
		t.Fatalf("claim = %s %s %v", role, claim, err)
	}

	admin := signUser(t, supportAdminID)
	desk := supportPerform(router, http.MethodPost, "/api/platform/messages/realtime", `{"scope":"desk"}`, admin, header)
	if desk.Code != http.StatusOK || decodeSupport(t, desk.Body.Bytes())["topic"] != DeskTopic(bytes.Repeat([]byte{9}, 32)) {
		t.Fatalf("desk = %s", desk.Body.String())
	}
	if supportPerform(router, http.MethodPost, "/api/platform/messages/realtime", `{"scope":"desk"}`, userToken, header).Code != http.StatusForbidden {
		t.Fatal("normal user received the desk channel")
	}

	created := supportPerform(router, http.MethodPost, "/api/platform/messages", `{"sessionId":"`+supportGuestID+`","content":"hello"}`, "", nil)
	if created.Code != http.StatusOK {
		t.Fatalf("create status = %d body %s", created.Code, created.Body.String())
	}
	if len(publisher.topics()) != 2 || store.pending() != 2 {
		t.Fatalf("failed publish topics %v pending %d", publisher.topics(), store.pending())
	}
	service.flushSupport(context.Background())
	if store.pending() != 0 || len(publisher.topics()) != 4 {
		t.Fatalf("retry topics %v pending %d", publisher.topics(), store.pending())
	}
	service.publishSupportMessage(context.Background(), source.writeRow)
	service.flushSupport(context.Background())
	if len(publisher.topics()) != 4 || store.pending() != 0 {
		t.Fatalf("duplicate publish topics %d pending %d", len(publisher.topics()), store.pending())
	}
}

func TestBroadcastRequestIsPrivateAndDoesNotEchoTheKey(t *testing.T) {
	const apiKey = "service-role-secret-value"
	topic := SessionTopic(bytes.Repeat([]byte{9}, 32), supportGuestID)
	var seen broadcastRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &seen.Body)
		seen.URL = r.URL.RequestURI()
		seen.Authorization = r.Header.Get("Authorization")
		seen.APIKey = r.Header.Get("apikey")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	err := NewBroadcast(server.URL, apiKey).Publish(context.Background(), topic, map[string]any{"id": supportMessage})
	if err == nil || strings.Contains(err.Error(), apiKey) || strings.Contains(seen.URL, apiKey) {
		t.Fatalf("error = %v url %s", err, seen.URL)
	}
	if !strings.Contains(seen.URL, "/realtime/v1/api/broadcast/"+topic+"/events/message?private=true") || seen.Body["id"] != supportMessage || seen.Authorization != "Bearer "+apiKey || strings.Contains(topic, supportGuestID) {
		t.Fatalf("broadcast = %#v topic %s", seen, topic)
	}
}

type broadcastRequest struct {
	URL           string
	Body          map[string]any
	Authorization string
	APIKey        string
}

func supportRouter(t *testing.T, source Source, store SupportStore, publisher SupportPublisher, sender TelegramSender) (*gin.Engine, *Service) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	validator := auth.NewValidator(&config.Config{
		SupabaseJWTSecret: "test-secret",
		SupabaseURL:       "https://gmfuorleragagsqbkajh.supabase.co",
	})
	service := NewService(source, nil, "postgres")
	service.SetTelegram(sender)
	service.SetSupport(SupportConfig{
		Store:      store,
		ConfigKey:  bytes.Repeat([]byte{7}, 32),
		ChannelKey: bytes.Repeat([]byte{9}, 32),
		JWTSecret:  "test-secret",
		Issuer:     "https://gmfuorleragagsqbkajh.supabase.co/auth/v1",
		Publisher:  publisher,
	})
	router := gin.New()
	NewHandler(service, validator).Register(router)
	return router, service
}

func adminSource(admin bool) *fakeSource {
	return &fakeSource{tables: map[string][]map[string]any{
		"user_profiles": {{"id": supportAdminID, "is_admin": admin}},
	}}
}

func supportPerform(router http.Handler, method, path, body, token string, extra map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range extra {
		req.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func decodeSupport(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

type filteringSource struct {
	fakeSource *fakeSource
}

func (f filteringSource) Select(ctx context.Context, caller Caller, query Query) (Result, error) {
	result, err := f.fakeSource.Select(ctx, caller, query)
	if err != nil || len(query.Filters) == 0 {
		return result, err
	}
	var rows []map[string]any
	for _, row := range result.Rows {
		matched := true
		for _, filter := range query.Filters {
			if filter.Op != "" && filter.Op != "eq" {
				continue
			}
			if !strings.EqualFold(stringField(row, filter.Column), fmt.Sprint(filter.Value)) {
				matched = false
			}
		}
		if matched {
			rows = append(rows, row)
		}
	}
	result.Rows = rows
	return result, nil
}

func (f filteringSource) Write(ctx context.Context, caller Caller, method string, query Query, payload any) (Result, error) {
	return f.fakeSource.Write(ctx, caller, method, query, payload)
}

type recordingSender struct {
	mu    sync.Mutex
	count int
}

func (r *recordingSender) Send(context.Context, int64, string) (int64, error) {
	r.mu.Lock()
	r.count++
	r.mu.Unlock()
	return 1, nil
}

func (r *recordingSender) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

type scriptedPublisher struct {
	mu        sync.Mutex
	failTimes int
	seen      []string
}

func (p *scriptedPublisher) Publish(_ context.Context, topic string, _ map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seen = append(p.seen, topic)
	if p.failTimes > 0 {
		p.failTimes--
		return http.ErrHandlerTimeout
	}
	return nil
}

func (p *scriptedPublisher) topics() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

type memorySupportStore struct {
	mu         sync.Mutex
	record     TelegramTokenRecord
	deliveries []storedSupportDelivery
}

type storedSupportDelivery struct {
	SupportDelivery
	delivered bool
	next      time.Time
}

func newMemorySupportStore() *memorySupportStore {
	return &memorySupportStore{}
}

func (m *memorySupportStore) snapshot() TelegramTokenRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.record
}

func (m *memorySupportStore) ReadTelegramToken(context.Context) (TelegramTokenRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.record, nil
}

func (m *memorySupportStore) SaveTelegramToken(_ context.Context, ciphertext, nonce []byte, hint, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.record = TelegramTokenRecord{
		Configured: true,
		Ciphertext: append([]byte(nil), ciphertext...),
		Nonce:      append([]byte(nil), nonce...),
		Hint:       hint,
	}
	return nil
}

func (m *memorySupportStore) ClearTelegramToken(context.Context, string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.record = TelegramTokenRecord{}
	return nil
}

func (m *memorySupportStore) EnqueueSupport(_ context.Context, deliveries []SupportDelivery) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, delivery := range deliveries {
		exists := false
		for _, current := range m.deliveries {
			if current.MessageID == delivery.MessageID && current.Topic == delivery.Topic {
				exists = true
				break
			}
		}
		if exists {
			continue
		}
		m.deliveries = append(m.deliveries, storedSupportDelivery{SupportDelivery: delivery, next: time.Now().Add(-time.Second)})
	}
	return nil
}

func (m *memorySupportStore) ClaimSupport(_ context.Context, limit int) ([]SupportDelivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var claimed []SupportDelivery
	now := time.Now()
	for _, delivery := range m.deliveries {
		if delivery.delivered || delivery.next.After(now) {
			continue
		}
		claimed = append(claimed, delivery.SupportDelivery)
		if len(claimed) == limit {
			break
		}
	}
	return claimed, nil
}

func (m *memorySupportStore) FinishSupport(_ context.Context, messageID, topic string, delivered bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.deliveries {
		if m.deliveries[i].MessageID != messageID || m.deliveries[i].Topic != topic || m.deliveries[i].delivered {
			continue
		}
		if delivered {
			m.deliveries[i].delivered = true
			continue
		}
		m.deliveries[i].Attempts++
		m.deliveries[i].next = time.Now().Add(-time.Second)
	}
	return nil
}

func (m *memorySupportStore) pending() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, delivery := range m.deliveries {
		if !delivery.delivered {
			count++
		}
	}
	return count
}

func TestDecodeKeyRejectsShortMaterial(t *testing.T) {
	if DecodeKey("") != nil || DecodeKey(base64.StdEncoding.EncodeToString([]byte("short"))) != nil {
		t.Fatal("short key was accepted")
	}
	if len(DecodeKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))) != 32 {
		t.Fatal("32-byte key was rejected")
	}
}
