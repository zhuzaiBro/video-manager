// Package gormplus is a small helper over the existing GORM connection.
// It is not a second ORM and it is not a public CRUD endpoint.
package gormplus

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type Kind string

const (
	Platform Kind = "platform"
	Video    Kind = "video"
)

var ErrWrongDatabase = errors.New("gormplus repository is bound to the platform database")

// DB is one pool plus the caller scope for a single transaction.
// Video and platform pools are different values and cannot be substituted.
type DB struct {
	kind Kind
	g    *gorm.DB
}

func Use(pool *sql.DB, kind Kind) (*DB, error) {
	if pool == nil || (kind != Platform && kind != Video) {
		return nil, fmt.Errorf("invalid database")
	}
	g, err := gorm.Open(postgres.New(postgres.Config{
		Conn:                 pool,
		PreferSimpleProtocol: true,
	}), &gorm.Config{PrepareStmt: false})
	if err != nil {
		return nil, err
	}
	return &DB{kind: kind, g: g}, nil
}

func (db *DB) Kind() Kind { return db.kind }

// Do runs fn in a transaction after setting the caller's database role.
// The role is local to this transaction and is not stored on DB.
func (db *DB) Do(ctx context.Context, userID string, fn func(tx *gorm.DB) error) error {
	if db == nil || db.g == nil {
		return fmt.Errorf("database is not open")
	}
	return db.g.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		statement := "SET LOCAL ROLE anon"
		role := "anon"
		if userID != "" {
			statement = "SET LOCAL ROLE authenticated"
			role = "authenticated"
		}
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("application role cannot assume %s: %w", role, err)
		}
		claims := map[string]any{"role": role}
		if userID != "" {
			claims["sub"] = userID
		}
		encoded, err := json.Marshal(claims)
		if err != nil {
			return err
		}
		if err := tx.Exec(
			`SELECT set_config('request.jwt.claims', ?, true), set_config('request.jwt.claim.sub', ?, true), set_config('request.jwt.claim.role', ?, true)`,
			string(encoded), userID, role,
		).Error; err != nil {
			return err
		}
		return fn(tx)
	})
}
