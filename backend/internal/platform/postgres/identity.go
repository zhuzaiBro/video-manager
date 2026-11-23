package postgres

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/zood/video-manager/internal/platform"
)

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
