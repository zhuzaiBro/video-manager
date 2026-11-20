package postgres

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/zood/video-manager/internal/platform"
)

func (s *Source) PasswordAccount(ctx context.Context, email string) (id, storedEmail, hash string, created time.Time, ok bool, err error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return "", "", "", time.Time{}, false, nil
	}
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		scanErr := tx.QueryRowContext(ctx, `SELECT id::text, email, encrypted_password, created_at FROM platform.password_hash_for_email($1)`, email).
			Scan(&id, &storedEmail, &hash, &created)
		if errors.Is(scanErr, sql.ErrNoRows) {
			return nil
		}
		if scanErr != nil {
			return scanErr
		}
		ok = true
		return nil
	})
	return id, storedEmail, hash, created, ok, err
}

func (s *Source) RegisterPassword(ctx context.Context, id, email, hash string) error {
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		_, execErr := tx.ExecContext(ctx, `SELECT platform.register_password_user($1::uuid, $2, $3)`, id, strings.ToLower(strings.TrimSpace(email)), hash)
		return execErr
	})
	return mapDB(err)
}

func (s *Source) FetchSelf(ctx context.Context, caller platform.Caller) (platform.AuthAccount, error) {
	var account platform.AuthAccount
	err := s.withScope(ctx, caller, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT id::text, COALESCE(email, ''), COALESCE(phone, ''), created_at FROM platform.own_auth_account()`).
			Scan(&account.ID, &account.Email, &account.Phone, &account.CreatedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return platform.AuthAccount{}, platform.NewError(http.StatusUnauthorized, "UNAUTHORIZED", "登录身份不存在")
	}
	return account, mapDB(err)
}

func mapDB(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "42501":
			return platform.NewError(http.StatusForbidden, "FORBIDDEN", "无权访问")
		case "23505":
			return platform.NewError(http.StatusConflict, "CONFLICT", "记录已存在")
		}
	}
	return err
}
