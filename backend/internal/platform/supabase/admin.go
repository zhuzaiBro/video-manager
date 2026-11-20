package supabase

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zood/video-manager/internal/platform"
)

// Admin is the only client allowed to hold the service role key. It has no
// generic query method; each method names one privileged operation.
type Admin struct {
	baseURL string
	key     string
	http    *http.Client
}

func NewAdmin(baseURL, serviceRoleKey string) (*Admin, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	serviceRoleKey = strings.TrimSpace(serviceRoleKey)
	if baseURL == "" || serviceRoleKey == "" {
		return nil, fmt.Errorf("SUPABASE_URL and SUPABASE_SERVICE_ROLE_KEY are required")
	}
	if !isServiceRole(serviceRoleKey) {
		return nil, fmt.Errorf("SUPABASE_SERVICE_ROLE_KEY must be a service_role key")
	}
	return &Admin{baseURL: baseURL, key: serviceRoleKey, http: &http.Client{Timeout: 20 * time.Second}}, nil
}

func decodePart(value string) ([]byte, error) {
	if payload, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return payload, nil
	}
	return base64.URLEncoding.DecodeString(value)
}

func isServiceRole(key string) bool {
	if strings.HasPrefix(key, "sb_secret_") {
		return true
	}
	parts := strings.Split(key, ".")
	if len(parts) < 2 {
		return false
	}
	payload, err := decodePart(parts[1])
	if err != nil {
		return false
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return false
	}
	role, _ := claims["role"].(string)
	return role == "service_role"
}

func (a *Admin) GetAuthUser(ctx context.Context, userID string) (platform.AuthAccount, bool, error) {
	status, body, err := a.auth(ctx, http.MethodGet, "/auth/v1/admin/users/"+url.PathEscape(userID), nil)
	if err != nil {
		return platform.AuthAccount{}, false, err
	}
	if status == http.StatusNotFound {
		return platform.AuthAccount{}, false, nil
	}
	if status >= 400 {
		return platform.AuthAccount{}, false, upstreamError(status, body)
	}
	account, err := decodeAuthAccount(body)
	return account, account.ID != "", err
}

func (a *Admin) ListAuthUsers(ctx context.Context) ([]platform.AuthAccount, error) {
	var all []platform.AuthAccount
	for page := 1; page <= 20; page++ {
		status, body, err := a.auth(ctx, http.MethodGet, "/auth/v1/admin/users?page="+strconv.Itoa(page)+"&per_page=1000", nil)
		if err != nil {
			return nil, err
		}
		if status >= 400 {
			return nil, upstreamError(status, body)
		}
		var payload struct {
			Users []json.RawMessage `json:"users"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, err
		}
		if len(payload.Users) == 0 {
			break
		}
		for _, raw := range payload.Users {
			account, err := decodeAuthAccount(raw)
			if err != nil {
				return nil, err
			}
			all = append(all, account)
		}
		if len(payload.Users) < 1000 {
			break
		}
	}
	return all, nil
}

func (a *Admin) UpsertProfiles(ctx context.Context, rows []map[string]any, ignoreDuplicates bool) error {
	prefer := "return=minimal,resolution=merge-duplicates"
	if ignoreDuplicates {
		prefer = "return=minimal,resolution=ignore-duplicates"
	}
	return a.rest(ctx, http.MethodPost, "/rest/v1/user_profiles?on_conflict=id", rows, prefer)
}

func (a *Admin) ReferralExists(ctx context.Context, referredUserID string) (bool, error) {
	rows, err := a.selectRows(ctx, "/rest/v1/interview_referral_rewards?select=id&referred_user_id=eq."+referredUserID+"&limit=1")
	return len(rows) > 0, err
}

func (a *Admin) InsertReferral(ctx context.Context, row map[string]any) error {
	return a.rest(ctx, http.MethodPost, "/rest/v1/interview_referral_rewards", row, "return=minimal")
}

func (a *Admin) ReadProfile(ctx context.Context, userID string) (map[string]any, bool, error) {
	rows, err := a.selectRows(ctx, "/rest/v1/user_profiles?select=id,username,display_name,avatar_url,vip_level,vip_expires_at,is_admin&id=eq."+userID+"&limit=1")
	if err != nil || len(rows) == 0 {
		return nil, false, err
	}
	return rows[0], true, nil
}

func (a *Admin) UpdateVip(ctx context.Context, userID string, level int, expiresAt any) error {
	return a.rest(ctx, http.MethodPatch, "/rest/v1/user_profiles?id=eq."+userID, map[string]any{
		"vip_level": level, "vip_expires_at": expiresAt,
	}, "return=minimal")
}

func (a *Admin) MarkConsultationPaid(ctx context.Context, requestID int, userID, paidAt string) error {
	return a.rest(ctx, http.MethodPatch, "/rest/v1/membership_consultation_requests?id=eq."+strconv.Itoa(requestID), map[string]any{
		"status": "paid", "paid_at": paidAt, "user_id": userID,
	}, "return=minimal")
}

func (a *Admin) ListProfiles(ctx context.Context) ([]map[string]any, error) {
	return a.selectRows(ctx, "/rest/v1/user_profiles?select=id,username,display_name,avatar_url,vip_level,is_admin,created_at&order=created_at.desc&limit=1000")
}

func (a *Admin) ListWatchLogsSince(ctx context.Context, since time.Time) ([]map[string]any, error) {
	return a.selectRows(ctx, "/rest/v1/video_access_logs?select=user_id,video_id,watch_seconds,created_at&created_at=gte."+url.QueryEscape(since.UTC().Format(time.RFC3339))+"&order=created_at.desc&limit=1000")
}

func (a *Admin) ListActiveTelegramAdmins(ctx context.Context) ([]int64, error) {
	rows, err := a.selectRows(ctx, "/rest/v1/telegram_support_admins?select=chat_id&active=eq.true")
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		switch value := row["chat_id"].(type) {
		case float64:
			ids = append(ids, int64(value))
		}
	}
	return ids, nil
}

func (a *Admin) UpsertTelegramAdmin(ctx context.Context, chatID, telegramUserID int64, username string) error {
	return a.rest(ctx, http.MethodPost, "/rest/v1/telegram_support_admins?on_conflict=chat_id", map[string]any{
		"chat_id": chatID, "telegram_user_id": telegramUserID, "username": username, "active": true,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}, "return=minimal,resolution=merge-duplicates")
}

func (a *Admin) TelegramAdminActive(ctx context.Context, chatID, telegramUserID int64) (bool, error) {
	rows, err := a.selectRows(ctx, "/rest/v1/telegram_support_admins?select=chat_id&chat_id=eq."+strconv.FormatInt(chatID, 10)+"&telegram_user_id=eq."+strconv.FormatInt(telegramUserID, 10)+"&active=eq.true&limit=1")
	return len(rows) > 0, err
}

func (a *Admin) TelegramUpdateSeen(ctx context.Context, updateID int64) (bool, error) {
	rows, err := a.selectRows(ctx, "/rest/v1/telegram_message_links?select=id&telegram_update_id=eq."+strconv.FormatInt(updateID, 10)+"&limit=1")
	return len(rows) > 0, err
}

func (a *Admin) TelegramReplySession(ctx context.Context, chatID, messageID int64) (string, bool, error) {
	rows, err := a.selectRows(ctx, "/rest/v1/telegram_message_links?select=session_id&telegram_chat_id=eq."+strconv.FormatInt(chatID, 10)+"&telegram_message_id=eq."+strconv.FormatInt(messageID, 10)+"&direction=eq.to_telegram&limit=1")
	if err != nil || len(rows) == 0 {
		return "", false, err
	}
	session, _ := rows[0]["session_id"].(string)
	return session, session != "", nil
}

func (a *Admin) InsertAdminMessage(ctx context.Context, sessionID, content string) (string, error) {
	rows, err := a.writeRows(ctx, http.MethodPost, "/rest/v1/messages", map[string]any{
		"session_id": sessionID, "sender_type": "admin", "content": content,
	}, "return=representation")
	if err != nil || len(rows) == 0 {
		return "", err
	}
	id, _ := rows[0]["id"].(string)
	return id, nil
}

func (a *Admin) InsertTelegramLink(ctx context.Context, row map[string]any) error {
	return a.rest(ctx, http.MethodPost, "/rest/v1/telegram_message_links", row, "return=minimal")
}

func (a *Admin) selectRows(ctx context.Context, path string) ([]map[string]any, error) {
	return a.writeRows(ctx, http.MethodGet, path, nil, "")
}

func (a *Admin) writeRows(ctx context.Context, method, path string, payload any, prefer string) ([]map[string]any, error) {
	status, body, err := a.restRaw(ctx, method, path, payload, prefer)
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, upstreamError(status, body)
	}
	if len(bytes.TrimSpace(body)) == 0 || prefer == "return=minimal" || strings.Contains(prefer, "return=minimal") {
		return nil, nil
	}
	return decodeRows(status, body)
}

func (a *Admin) rest(ctx context.Context, method, path string, payload any, prefer string) error {
	_, err := a.writeRows(ctx, method, path, payload, prefer)
	return err
}

func (a *Admin) auth(ctx context.Context, method, path string, payload any) (int, []byte, error) {
	return a.restRaw(ctx, method, path, payload, "")
}

func (a *Admin) restRaw(ctx context.Context, method, path string, payload any, prefer string) (int, []byte, error) {
	var reader io.Reader
	if payload != nil && method != http.MethodGet {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("apikey", a.key)
	req.Header.Set("Authorization", "Bearer "+a.key)
	req.Header.Set("Accept", "application/json")
	if payload != nil && method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
	if prefer != "" {
		req.Header.Set("Prefer", prefer)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return 0, nil, platform.NewError(http.StatusBadGateway, "UPSTREAM", "管理数据服务暂时不可用")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return resp.StatusCode, body, err
}
