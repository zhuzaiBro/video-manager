package gormplus

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

var (
	ErrEmptyCondition = errors.New("update or delete requires a condition")
	ErrColumn         = errors.New("column is not allowed")
)

type Page[T any] struct {
	Records []T
	Total   int64
	Page    int
	Size    int
}

type Query struct {
	tx       *gorm.DB
	allowed  map[string]struct{}
	where    int
	rejected error
}

func (q *Query) Eq(column string, value any) *Query {
	if q.reject(column) {
		return q
	}
	q.tx = q.tx.Where(column+" = ?", value)
	q.where++
	return q
}

func (q *Query) ILike(columns []string, term string) *Query {
	term = strings.TrimSpace(term)
	if term == "" {
		return q
	}
	parts := make([]string, 0, len(columns))
	args := make([]any, 0, len(columns))
	for _, column := range columns {
		if q.reject(column) {
			return q
		}
		parts = append(parts, column+" ILIKE ?")
		args = append(args, "%"+term+"%")
	}
	q.tx = q.tx.Where("("+strings.Join(parts, " OR ")+")", args...)
	q.where++
	return q
}

func (q *Query) Order(column string, desc bool) *Query {
	if q.reject(column) {
		return q
	}
	direction := "ASC"
	if desc {
		direction = "DESC"
	}
	q.tx = q.tx.Order(column + " " + direction)
	return q
}

func (q *Query) reject(column string) bool {
	if _, ok := q.allowed[column]; ok && column != "" {
		return false
	}
	q.rejected = fmt.Errorf("%w: %s", ErrColumn, column)
	return true
}

// Repo is the shared list/update behavior. The table comes from T, not from a request.
type Repo[T any] struct {
	db       *DB
	readable map[string]struct{}
	writable map[string]struct{}
}

func NewRepo[T interface{ TableName() string }](db *DB, readable, writable []string) (Repo[T], error) {
	if db == nil || db.Kind() != Platform {
		return Repo[T]{}, ErrWrongDatabase
	}
	return Repo[T]{db: db, readable: set(readable), writable: set(writable)}, nil
}

func (r Repo[T]) DB() *DB { return r.db }

func (r Repo[T]) Page(ctx context.Context, userID string, page, size int, build func(*Query)) (Page[T], error) {
	page, size = bounds(page, size)
	var out Page[T]
	err := r.db.Do(ctx, userID, func(tx *gorm.DB) error {
		pageResult, err := r.PageTx(tx, page, size, build)
		out = pageResult
		return err
	})
	return out, err
}

func (r Repo[T]) PageTx(tx *gorm.DB, page, size int, build func(*Query)) (Page[T], error) {
	page, size = bounds(page, size)
	q := &Query{tx: tx.Model(new(T)), allowed: r.readable}
	if build != nil {
		build(q)
	}
	if q.rejected != nil {
		return Page[T]{}, q.rejected
	}
	base := q.tx.Session(&gorm.Session{})
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return Page[T]{}, err
	}
	var records []T
	if err := q.tx.Limit(size).Offset((page - 1) * size).Find(&records).Error; err != nil {
		return Page[T]{}, err
	}
	if records == nil {
		records = []T{}
	}
	return Page[T]{Records: records, Total: total, Page: page, Size: size}, nil
}

func (r Repo[T]) DeleteByID(ctx context.Context, userID, id string) (int64, error) {
	if strings.TrimSpace(id) == "" {
		return 0, ErrEmptyCondition
	}
	var affected int64
	err := r.db.Do(ctx, userID, func(tx *gorm.DB) error {
		result := tx.Where("id = ?", id).Delete(new(T))
		affected = result.RowsAffected
		return result.Error
	})
	return affected, err
}

func (r Repo[T]) UpdateByID(ctx context.Context, userID, id string, values map[string]any) (int64, error) {
	if strings.TrimSpace(id) == "" {
		return 0, ErrEmptyCondition
	}
	filtered := map[string]any{}
	columns := make([]string, 0, len(values))
	for column, value := range values {
		if _, ok := r.writable[column]; !ok {
			return 0, fmt.Errorf("%w: %s", ErrColumn, column)
		}
		filtered[column] = value
		columns = append(columns, column)
	}
	if len(filtered) == 0 {
		return 0, ErrEmptyCondition
	}
	var affected int64
	err := r.db.Do(ctx, userID, func(tx *gorm.DB) error {
		result := tx.Model(new(T)).Where("id = ?", id).Select(columns).Updates(filtered)
		affected = result.RowsAffected
		return result.Error
	})
	return affected, err
}

func bounds(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if page > 400 {
		page = 400
	}
	if size < 1 {
		size = 10
	}
	if size > 50 {
		size = 50
	}
	return page, size
}

func set(columns []string) map[string]struct{} {
	out := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		out[column] = struct{}{}
	}
	return out
}
