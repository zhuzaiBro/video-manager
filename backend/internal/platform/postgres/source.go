package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/zood/video-manager/internal/platform"
)

// Source is the platform database. It is a separate pool from the video
// service database and is used only when PLATFORM_DATA_SOURCE=postgres.
type Source struct {
	db *sql.DB
}

func (s *Source) Pool() *sql.DB { return s.db }

func Open(databaseURL string) (*Source, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	// The restored database is capped at 512MiB. Keep this pool separate from
	// the video service pool and small enough that both can coexist.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	var super, bypass bool
	err = db.QueryRowContext(ctx, `
		SELECT rolsuper, rolbypassrls
		FROM pg_roles
		WHERE rolname = current_user
	`).Scan(&super, &bypass)
	if err != nil || super || bypass {
		_ = db.Close()
		if err != nil {
			return nil, err
		}
		return nil, platform.NewError(http.StatusInternalServerError, "FORBIDDEN", "平台数据库连接不能是超级用户，也不能绕过行级安全")
	}
	return &Source{db: db}, nil
}

func (s *Source) Select(ctx context.Context, caller platform.Caller, query platform.Query) (platform.Result, error) {
	flat, embeds, err := platform.PrepareSelect(query)
	if err != nil {
		return platform.Result{}, err
	}
	query = flat
	statement, err := platform.SQL(query, false)
	if err != nil {
		return platform.Result{}, err
	}
	rows, err := s.queryMaps(ctx, caller, statement.Text, statement.Args)
	if err != nil {
		return platform.Result{}, err
	}
	result := platform.Result{Rows: rows}
	if query.Count {
		countStatement, err := platform.SQL(query, true)
		if err != nil {
			return platform.Result{}, err
		}
		var count int
		err = s.withScope(ctx, caller, func(tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, countStatement.Text, countStatement.Args...).Scan(&count)
		})
		if err != nil {
			return platform.Result{}, err
		}
		result.Count = &count
	}
	if err := s.attachEmbeds(ctx, caller, query.Table, embeds, result.Rows); err != nil {
		return platform.Result{}, err
	}
	return result, nil
}

func (s *Source) Write(ctx context.Context, caller platform.Caller, method string, query platform.Query, payload any) (platform.Result, error) {
	if err := platform.ValidateQuery(query); err != nil {
		return platform.Result{}, err
	}
	body, err := payloadMap(payload)
	if err != nil {
		return platform.Result{}, err
	}
	switch method {
	case http.MethodPost:
		return s.insert(ctx, caller, query, body)
	case http.MethodPatch:
		return s.update(ctx, caller, query, body)
	case http.MethodDelete:
		return s.remove(ctx, caller, query)
	default:
		return platform.Result{}, platform.NewError(http.StatusMethodNotAllowed, "METHOD", "不支持的写入方法")
	}
}

func (s *Source) insert(ctx context.Context, caller platform.Caller, query platform.Query, body map[string]any) (platform.Result, error) {
	columns := make([]string, 0, len(body))
	args := make([]any, 0, len(body))
	holders := make([]string, 0, len(body))
	for column, value := range body {
		if !safeIdent(column) {
			return platform.Result{}, platform.NewError(http.StatusBadRequest, "INVALID_INPUT", "写入字段不合法")
		}
		columns = append(columns, column)
		args = append(args, normalize(value))
		holders = append(holders, "$"+strconv.Itoa(len(args)))
	}
	text := "INSERT INTO " + query.Table + " (" + strings.Join(columns, ", ") + ") VALUES (" + strings.Join(holders, ", ") + ") RETURNING " + returning(query)
	rows, err := s.queryMaps(ctx, caller, text, args)
	return platform.Result{Rows: rows}, err
}

func (s *Source) update(ctx context.Context, caller platform.Caller, query platform.Query, body map[string]any) (platform.Result, error) {
	args := make([]any, 0, len(body)+4)
	assignments := make([]string, 0, len(body))
	for column, value := range body {
		if !safeIdent(column) {
			return platform.Result{}, platform.NewError(http.StatusBadRequest, "INVALID_INPUT", "写入字段不合法")
		}
		args = append(args, normalize(value))
		assignments = append(assignments, column+" = $"+strconv.Itoa(len(args)))
	}
	where, whereArgs, err := whereSQL(query, len(args))
	if err != nil {
		return platform.Result{}, err
	}
	args = append(args, whereArgs...)
	text := "UPDATE " + query.Table + " SET " + strings.Join(assignments, ", ") + " WHERE " + where + " RETURNING " + returning(query)
	rows, err := s.queryMaps(ctx, caller, text, args)
	if err != nil {
		return platform.Result{}, err
	}
	if len(rows) == 0 {
		return platform.Result{}, platform.NewError(http.StatusNotFound, "NOT_FOUND", "记录不存在或无权访问")
	}
	return platform.Result{Rows: rows}, nil
}

func (s *Source) remove(ctx context.Context, caller platform.Caller, query platform.Query) (platform.Result, error) {
	where, args, err := whereSQL(query, 0)
	if err != nil {
		return platform.Result{}, err
	}
	text := "DELETE FROM " + query.Table + " WHERE " + where + " RETURNING " + returning(query)
	rows, err := s.queryMaps(ctx, caller, text, args)
	if err != nil {
		return platform.Result{}, err
	}
	if len(rows) == 0 {
		return platform.Result{}, platform.NewError(http.StatusNotFound, "NOT_FOUND", "记录不存在或无权访问")
	}
	return platform.Result{Rows: rows}, nil
}

func (s *Source) queryMaps(ctx context.Context, caller platform.Caller, text string, args []any) ([]map[string]any, error) {
	var result []map[string]any
	err := s.withScope(ctx, caller, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, text, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		result, err = scanMaps(rows)
		return err
	})
	return result, err
}

func (s *Source) withScope(ctx context.Context, caller platform.Caller, fn func(*sql.Tx) error) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		role := "anon"
		if caller.UserID != "" {
			role = "authenticated"
		}
		// Role names are fixed. They are not taken from the request.
		statement := `SET LOCAL ROLE anon`
		if role == "authenticated" {
			statement = `SET LOCAL ROLE authenticated`
		}
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("application role cannot assume %s: %w", role, err)
		}
		if err := setJWTClaims(ctx, tx, caller); err != nil {
			return err
		}
		return fn(tx)
	})
}

func (s *Source) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func setJWTClaims(ctx context.Context, tx *sql.Tx, caller platform.Caller) error {
	claims := map[string]any{"role": "anon"}
	if caller.UserID != "" {
		claims["role"] = "authenticated"
		claims["sub"] = caller.UserID
		if caller.Email != "" {
			claims["email"] = caller.Email
		}
	}
	encoded, err := json.Marshal(claims)
	if err != nil {
		return err
	}
	sub := caller.UserID
	role, _ := claims["role"].(string)
	if _, err := tx.ExecContext(ctx, `
		SELECT
			set_config('request.jwt.claims', $1, true),
			set_config('request.jwt.claim.sub', $2, true),
			set_config('request.jwt.claim.role', $3, true),
			set_config('app.user_id', $2, true)
	`, string(encoded), sub, role); err != nil {
		return err
	}
	return nil
}

func scanMaps(rows *sql.Rows) ([]map[string]any, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(columns))
		for i, column := range columns {
			row[column] = normalizeSQL(values[i])
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func whereSQL(query platform.Query, offset int) (string, []any, error) {
	rendered, err := platform.SQL(platform.Query{Table: query.Table, Select: "id", Filters: query.Filters}, false)
	if err != nil {
		return "", nil, err
	}
	marker := " WHERE "
	idx := strings.Index(rendered.Text, marker)
	if idx < 0 {
		return "", nil, platform.NewError(http.StatusBadRequest, "INVALID_INPUT", "写入缺少过滤条件")
	}
	where := rendered.Text[idx+len(marker):]
	for _, suffix := range []string{" ORDER BY ", " LIMIT ", " OFFSET "} {
		if cut := strings.Index(where, suffix); cut >= 0 {
			where = where[:cut]
		}
	}
	if offset == 0 {
		return where, rendered.Args, nil
	}
	rewritten := where
	for i := len(rendered.Args); i >= 1; i-- {
		rewritten = strings.ReplaceAll(rewritten, "$"+strconv.Itoa(i), "$"+strconv.Itoa(i+offset))
	}
	return rewritten, rendered.Args, nil
}

func returning(query platform.Query) string {
	if query.Select != "" && !strings.ContainsAny(query.Select, "(*") {
		return query.Select
	}
	return "id"
}

func payloadMap(payload any) (map[string]any, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, platform.NewError(http.StatusBadRequest, "INVALID_INPUT", "写入内容必须是对象")
	}
	return body, nil
}

func normalize(value any) any {
	switch typed := value.(type) {
	case map[string]any, []any:
		encoded, _ := json.Marshal(typed)
		return string(encoded)
	default:
		return value
	}
}

func normalizeSQL(value any) any {
	switch typed := value.(type) {
	case []byte:
		var decoded any
		if json.Unmarshal(typed, &decoded) == nil {
			return decoded
		}
		return string(typed)
	default:
		return value
	}
}

func safeIdent(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}
