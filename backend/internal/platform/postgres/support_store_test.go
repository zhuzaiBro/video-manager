package postgres

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/zood/video-manager/internal/platform"
)

func TestSupportSettingsAreInvisibleToRequestRoles(t *testing.T) {
	_, dsn := startPostgresOn(t, "55499")
	admin := openDB(t, dsn)
	for _, statement := range []string{
		`CREATE ROLE anon NOLOGIN NOSUPERUSER NOBYPASSRLS`,
		`CREATE ROLE authenticated NOLOGIN NOSUPERUSER NOBYPASSRLS`,
		`CREATE ROLE zood_app LOGIN NOSUPERUSER NOBYPASSRLS PASSWORD 'local-test-only'`,
		`GRANT anon, authenticated TO zood_app`,
		`CREATE DATABASE zood_support OWNER zood_app`,
	} {
		if _, err := admin.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	owner := openDB(t, "postgres://postgres@127.0.0.1:55499/zood_support?sslmode=disable")
	script, err := os.ReadFile("../../../migrations/005_support_settings.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(string(script)); err != nil {
		t.Fatal(err)
	}
	appDSN := "postgres://zood_app:local-test-only@127.0.0.1:55499/zood_support?sslmode=disable"
	source, err := Open(appDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Pool().Close() })

	const token = "123456789:AAH12345678901234567890123456789012"
	ciphertext, nonce, hint, err := platform.EncryptToken(bytes.Repeat([]byte{7}, 32), token)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.SaveTelegramToken(context.Background(), ciphertext, nonce, hint, "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10"); err != nil {
		t.Fatal(err)
	}
	record, err := source.ReadTelegramToken(context.Background())
	if err != nil || !record.Configured || record.Hint != "9012" || bytes.Contains(record.Ciphertext, []byte(token)) {
		t.Fatalf("record configured %v hint %s err %v", record.Configured, record.Hint, err)
	}
	plain, err := platform.DecryptToken(bytes.Repeat([]byte{7}, 32), record.Ciphertext, record.Nonce)
	if err != nil || plain != token {
		t.Fatalf("decrypt = %q %v", plain, err)
	}
	app := openDB(t, appDSN)
	t.Cleanup(func() { _ = app.Close() })
	for _, role := range []string{"anon", "authenticated"} {
		tx, err := app.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`SET LOCAL ROLE ` + role); err != nil {
			t.Fatal(err)
		}
		var ignored []byte
		err = tx.QueryRow(`SELECT telegram_bot_token_ciphertext FROM public.support_settings WHERE id = 1`).Scan(&ignored)
		_ = tx.Rollback()
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("%s read ciphertext: %v", role, err)
		}
		tx, err = app.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(`SET LOCAL ROLE ` + role); err != nil {
			t.Fatal(err)
		}
		err = tx.QueryRow(`SELECT payload::text FROM public.support_realtime_outbox`).Scan(&ignored)
		_ = tx.Rollback()
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("%s read outbox: %v", role, err)
		}
	}

	ctx := context.Background()
	messageID := "44444444-4444-4444-8444-444444444444"
	topic := "support.v1." + strings.Repeat("ab", 32)
	delivery := platform.SupportDelivery{
		MessageID: messageID,
		Topic:     topic,
		Payload: map[string]any{
			"id": messageID, "session_id": "33333333-3333-4333-8333-333333333333",
			"sender_type": "user", "content": "hello", "created_at": "2026-11-22T00:00:00Z",
		},
	}
	if err := source.EnqueueSupport(ctx, []platform.SupportDelivery{delivery, delivery}); err != nil {
		t.Fatal(err)
	}
	claimed, err := source.ClaimSupport(ctx, 20)
	if err != nil || len(claimed) != 1 || claimed[0].Payload["content"] != "hello" {
		t.Fatalf("claimed %#v %v", claimed, err)
	}
	if err = source.FinishSupport(ctx, messageID, topic, false); err != nil {
		t.Fatal(err)
	}
	var lastError string
	if err = app.QueryRow(`SELECT last_error FROM public.support_realtime_outbox WHERE message_id = $1`, messageID).Scan(&lastError); err != nil {
		t.Fatal(err)
	}
	if lastError != "publish failed" {
		t.Fatalf("last_error = %q", lastError)
	}
	claimed, err = source.ClaimSupport(ctx, 20)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("backoff did not hold the retry: %#v %v", claimed, err)
	}
	if _, err = app.Exec(`UPDATE public.support_realtime_outbox SET next_attempt_at = now() - interval '1 second' WHERE message_id = $1`, messageID); err != nil {
		t.Fatal(err)
	}
	claimed, err = source.ClaimSupport(ctx, 20)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("retry claim %#v %v", claimed, err)
	}
	if err = source.FinishSupport(ctx, messageID, topic, true); err != nil {
		t.Fatal(err)
	}
	if err = source.EnqueueSupport(ctx, []platform.SupportDelivery{delivery}); err != nil {
		t.Fatal(err)
	}
	claimed, err = source.ClaimSupport(ctx, 20)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("delivered event was queued again: %#v %v", claimed, err)
	}
}
