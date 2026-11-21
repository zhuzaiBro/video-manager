package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/zood/video-manager/internal/platform"
)

func callerFrom(ctx context.Context) (platform.Caller, error) {
	caller, ok := platform.CallerFromContext(ctx)
	if !ok {
		return platform.Caller{}, platform.NewError(http.StatusForbidden, "FORBIDDEN", "缺少已验证的调用者")
	}
	return caller, nil
}

func (s *Source) ReadProfile(ctx context.Context, userID string) (map[string]any, bool, error) {
	raw, err := s.adminJSON(ctx, `SELECT platform.admin_read_profile($1::uuid)`, userID)
	if err != nil || len(raw) == 0 || string(raw) == "null" {
		return nil, false, err
	}
	row := map[string]any{}
	if err := json.Unmarshal(raw, &row); err != nil {
		return nil, false, err
	}
	return row, true, nil
}

func (s *Source) UpdateVip(ctx context.Context, userID string, level int, expiresAt any) error {
	caller, err := callerFrom(ctx)
	if err != nil {
		return err
	}
	var expiry any
	if text, ok := expiresAt.(string); ok && text != "" {
		expiry = text
	}
	return s.withScope(ctx, caller, func(tx *sql.Tx) error {
		_, execErr := tx.ExecContext(ctx, `SELECT platform.admin_set_vip($1::uuid, $2, $3::timestamptz)`, userID, level, expiry)
		return mapDB(execErr)
	})
}

func (s *Source) ReferralExists(ctx context.Context, referredUserID string) (bool, error) {
	caller, err := callerFrom(ctx)
	if err != nil {
		return false, err
	}
	var exists bool
	err = s.withScope(ctx, caller, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT platform.referral_exists($1::uuid)`, referredUserID).Scan(&exists)
	})
	return exists, mapDB(err)
}

func (s *Source) InsertReferral(ctx context.Context, row map[string]any) error {
	caller, err := callerFrom(ctx)
	if err != nil {
		return err
	}
	var collection any
	if text := stringValue(row["source_collection_id"]); text != "" {
		collection = text
	}
	return s.withScope(ctx, caller, func(tx *sql.Tx) error {
		_, execErr := tx.ExecContext(ctx, `SELECT platform.insert_referral($1::uuid, $2::uuid, $3::uuid, $4, $5, $6)`,
			stringValue(row["referrer_user_id"]), stringValue(row["referred_user_id"]), collection,
			stringValue(row["source_url"]), intValue(row["reward_days"]), intValue(row["reward_vip_level"]))
		return mapDB(execErr)
	})
}

func (s *Source) UpsertProfiles(ctx context.Context, rows []map[string]any, _ bool) error {
	caller, err := callerFrom(ctx)
	if err != nil {
		return err
	}
	return s.withScope(ctx, caller, func(tx *sql.Tx) error {
		for _, row := range rows {
			if _, execErr := tx.ExecContext(ctx, `SELECT platform.upsert_own_or_admin_profile($1::uuid, $2, $3, $4)`,
				stringValue(row["id"]), stringValue(row["username"]), stringValue(row["display_name"]), stringValue(row["avatar_url"])); execErr != nil {
				return mapDB(execErr)
			}
		}
		return nil
	})
}

func (s *Source) GetAuthUser(ctx context.Context, userID string) (platform.AuthAccount, bool, error) {
	raw, err := s.adminJSON(ctx, `SELECT platform.admin_get_auth_user($1::uuid)`, userID)
	if err != nil || len(raw) == 0 || string(raw) == "null" {
		return platform.AuthAccount{}, false, err
	}
	return decodeAccount(raw)
}

func (s *Source) ListAuthUsers(ctx context.Context) ([]platform.AuthAccount, error) {
	raw, err := s.adminJSON(ctx, `SELECT platform.admin_list_auth_users()`)
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	out := make([]platform.AuthAccount, 0, len(rows))
	for _, row := range rows {
		encoded, _ := json.Marshal(row)
		account, ok, err := decodeAccount(encoded)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, account)
		}
	}
	return out, nil
}

// ListProfiles is the bounded profile helper used by backfill. The admin study
// list does not use it; that path pages with ReadAdminStudy.
func (s *Source) ListProfiles(ctx context.Context) ([]map[string]any, error) {
	caller, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.Select(ctx, caller, platform.Query{
		Table:  "user_profiles",
		Select: "id,username,display_name,avatar_url,vip_level,is_admin,created_at",
		Orders: []platform.Order{{Column: "created_at", Desc: true}, {Column: "id"}},
		Limit:  200,
		Count:  true,
	})
	if err != nil {
		return nil, err
	}
	if result.Count != nil && *result.Count > len(result.Rows) {
		return nil, platform.NewError(http.StatusInternalServerError, "RESULT_TRUNCATED", "用户数量超过可读取上限")
	}
	if result.Rows == nil {
		return []map[string]any{}, nil
	}
	return result.Rows, nil
}

func (s *Source) ListWatchLogsSince(ctx context.Context, since time.Time) ([]map[string]any, error) {
	caller, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.Select(ctx, caller, platform.Query{
		Table:  "video_access_logs",
		Select: "user_id,video_id,watch_seconds,created_at",
		Filters: []platform.Filter{{
			Column: "created_at", Op: "gte", Value: since.UTC().Format(time.RFC3339),
		}},
		Orders: []platform.Order{{Column: "created_at", Desc: true}},
		Limit:  200,
		Count:  true,
	})
	if err != nil {
		return nil, err
	}
	if result.Count != nil && *result.Count > len(result.Rows) {
		return nil, platform.NewError(http.StatusInternalServerError, "RESULT_TRUNCATED", "学习记录超过可读取上限")
	}
	if result.Rows == nil {
		return []map[string]any{}, nil
	}
	return result.Rows, nil
}

func (s *Source) MarkConsultationPaid(ctx context.Context, requestID int, userID, paidAt string) error {
	caller, err := callerFrom(ctx)
	if err != nil {
		return err
	}
	return s.withScope(ctx, caller, func(tx *sql.Tx) error {
		_, execErr := tx.ExecContext(ctx, `SELECT platform.admin_mark_consultation_paid($1, $2::uuid, $3::timestamptz)`, requestID, userID, paidAt)
		return mapDB(execErr)
	})
}

func (s *Source) ListActiveTelegramAdmins(ctx context.Context) ([]int64, error) {
	var raw []byte
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT platform.list_telegram_admins()`).Scan(&raw)
	})
	if err != nil {
		return nil, err
	}
	var ids []float64
	if err := json.Unmarshal(raw, &ids); err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		out = append(out, int64(id))
	}
	return out, nil
}

func (s *Source) UpsertTelegramAdmin(ctx context.Context, chatID, telegramUserID int64, username string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `SELECT platform.upsert_telegram_admin($1, $2, $3)`, chatID, telegramUserID, username)
		return err
	})
}

func (s *Source) TelegramAdminActive(ctx context.Context, chatID, telegramUserID int64) (bool, error) {
	var active bool
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT platform.telegram_admin_active($1, $2)`, chatID, telegramUserID).Scan(&active)
	})
	return active, err
}

func (s *Source) TelegramUpdateSeen(ctx context.Context, updateID int64) (bool, error) {
	var seen bool
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT platform.telegram_update_seen($1)`, updateID).Scan(&seen)
	})
	return seen, err
}

func (s *Source) TelegramReplySession(ctx context.Context, chatID, messageID int64) (string, bool, error) {
	var session sql.NullString
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT platform.telegram_reply_session($1, $2)`, chatID, messageID).Scan(&session)
	})
	if errors.Is(err, sql.ErrNoRows) || !session.Valid {
		return "", false, nil
	}
	return session.String, session.String != "", err
}

func (s *Source) InsertAdminMessage(ctx context.Context, sessionID, content string) (string, error) {
	var id string
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT platform.insert_admin_message($1::uuid, $2)::text`, sessionID, content).Scan(&id)
	})
	return id, err
}

func (s *Source) InsertTelegramLink(ctx context.Context, row map[string]any) error {
	encoded, err := json.Marshal(row)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		_, execErr := tx.ExecContext(ctx, `SELECT platform.insert_telegram_link($1::jsonb)`, string(encoded))
		return execErr
	})
}

func (s *Source) adminJSON(ctx context.Context, query string, args ...any) ([]byte, error) {
	caller, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	var raw []byte
	err = s.withScope(ctx, caller, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, query, args...).Scan(&raw)
	})
	return raw, mapDB(err)
}

func (s *Source) adminRows(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	raw, err := s.adminJSON(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return decodeRows(raw)
}

func decodeRows(raw []byte) ([]map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []map[string]any{}, nil
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func decodeAccount(raw []byte) (platform.AuthAccount, bool, error) {
	var row map[string]any
	if err := json.Unmarshal(raw, &row); err != nil {
		return platform.AuthAccount{}, false, err
	}
	account := platform.AuthAccount{ID: stringValue(row["id"]), Email: stringValue(row["email"]), Phone: stringValue(row["phone"])}
	if text := stringValue(row["created_at"]); text != "" {
		if parsed, err := time.Parse(time.RFC3339, text); err == nil {
			account.CreatedAt = parsed
		}
	}
	return account, account.ID != "", nil
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return 0
	}
}
