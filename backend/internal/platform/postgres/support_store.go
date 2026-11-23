package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/zood/video-manager/internal/platform"
)

func (s *Source) ReadTelegramToken(ctx context.Context) (platform.TelegramTokenRecord, error) {
	var record platform.TelegramTokenRecord
	var hint sql.NullString
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
			SELECT telegram_bot_token_ciphertext, telegram_bot_token_nonce, telegram_bot_token_hint
			FROM public.support_settings
			WHERE id = 1
		`).Scan(&record.Ciphertext, &record.Nonce, &hint)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return platform.TelegramTokenRecord{}, nil
	}
	if err != nil {
		return platform.TelegramTokenRecord{}, supportStoreError(err)
	}
	if hint.Valid {
		record.Hint = hint.String
	}
	record.Configured = len(record.Ciphertext) > 0 && len(record.Nonce) > 0
	return record, nil
}

func (s *Source) SaveTelegramToken(ctx context.Context, ciphertext, nonce []byte, hint, updatedBy string) error {
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		_, execErr := tx.ExecContext(ctx, `
			INSERT INTO public.support_settings (
				id, telegram_bot_token_ciphertext, telegram_bot_token_nonce, telegram_bot_token_hint, updated_by
			)
			VALUES (1, $1, $2, NULLIF($3, ''), NULLIF($4, '')::uuid)
			ON CONFLICT (id) DO UPDATE SET
				telegram_bot_token_ciphertext = EXCLUDED.telegram_bot_token_ciphertext,
				telegram_bot_token_nonce = EXCLUDED.telegram_bot_token_nonce,
				telegram_bot_token_hint = EXCLUDED.telegram_bot_token_hint,
				updated_at = now(),
				updated_by = EXCLUDED.updated_by
		`, ciphertext, nonce, hint, updatedBy)
		return execErr
	})
	return supportStoreError(err)
}

func (s *Source) ClearTelegramToken(ctx context.Context, updatedBy string) error {
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		_, execErr := tx.ExecContext(ctx, `
			INSERT INTO public.support_settings (id, updated_by)
			VALUES (1, NULLIF($1, '')::uuid)
			ON CONFLICT (id) DO UPDATE SET
				telegram_bot_token_ciphertext = NULL,
				telegram_bot_token_nonce = NULL,
				telegram_bot_token_hint = NULL,
				updated_at = now(),
				updated_by = EXCLUDED.updated_by
		`, updatedBy)
		return execErr
	})
	return supportStoreError(err)
}

func (s *Source) EnqueueSupport(ctx context.Context, deliveries []platform.SupportDelivery) error {
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		for _, delivery := range deliveries {
			encoded, err := json.Marshal(delivery.Payload)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO public.support_realtime_outbox (message_id, topic, payload)
				VALUES ($1::uuid, $2, $3::jsonb)
				ON CONFLICT (message_id, topic) DO NOTHING
			`, delivery.MessageID, delivery.Topic, string(encoded)); err != nil {
				return err
			}
		}
		return nil
	})
	return supportStoreError(err)
}

func (s *Source) ClaimSupport(ctx context.Context, limit int) ([]platform.SupportDelivery, error) {
	if limit <= 0 || limit > 20 {
		limit = 20
	}
	var deliveries []platform.SupportDelivery
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT message_id::text, topic, payload, attempts
			FROM public.support_realtime_outbox
			WHERE delivered_at IS NULL AND next_attempt_at <= now()
			ORDER BY created_at
			LIMIT $1
		`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var delivery platform.SupportDelivery
			var payload []byte
			if err := rows.Scan(&delivery.MessageID, &delivery.Topic, &payload, &delivery.Attempts); err != nil {
				return err
			}
			if err := json.Unmarshal(payload, &delivery.Payload); err != nil {
				return err
			}
			deliveries = append(deliveries, delivery)
		}
		return rows.Err()
	})
	return deliveries, supportStoreError(err)
}

func (s *Source) FinishSupport(ctx context.Context, messageID, topic string, delivered bool) error {
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if delivered {
			_, execErr := tx.ExecContext(ctx, `
				UPDATE public.support_realtime_outbox
				SET delivered_at = now(), last_error = NULL
				WHERE message_id = $1::uuid AND topic = $2 AND delivered_at IS NULL
			`, messageID, topic)
			return execErr
		}
		_, execErr := tx.ExecContext(ctx, `
			UPDATE public.support_realtime_outbox
			SET attempts = attempts + 1,
				last_error = 'publish failed',
				next_attempt_at = now() + make_interval(secs => least(60, greatest(attempts + 1, 1) * 2))
			WHERE message_id = $1::uuid AND topic = $2 AND delivered_at IS NULL
		`, messageID, topic)
		return execErr
	})
	return supportStoreError(err)
}

func supportStoreError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
		return platform.NewError(http.StatusServiceUnavailable, "SUPPORT_SCHEMA_MISSING", "客服配置表还没有在本地库创建")
	}
	mapped := mapDB(err)
	if apiErr, ok := mapped.(*platform.Error); ok {
		return apiErr
	}
	return platform.NewError(http.StatusInternalServerError, "SUPPORT_STORE_FAILED", "客服数据暂时不可用")
}
