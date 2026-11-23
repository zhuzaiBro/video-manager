package gormplus

import (
	"context"
	"errors"
	"testing"
)

type sample struct {
	ID string
}

func (sample) TableName() string { return "samples" }

func TestRepositoryRejectsTheVideoPoolAndEmptyConditions(t *testing.T) {
	_, err := NewRepo[sample](&DB{kind: Video}, []string{"id"}, []string{"id"})
	if !errors.Is(err, ErrWrongDatabase) {
		t.Fatalf("video pool: %v", err)
	}
	repo, err := NewRepo[sample](&DB{kind: Platform}, []string{"id"}, []string{"active"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateByID(context.Background(), "user", "", map[string]any{"active": false}); !errors.Is(err, ErrEmptyCondition) {
		t.Fatalf("empty id: %v", err)
	}
	if _, err := repo.UpdateByID(context.Background(), "user", "row", map[string]any{}); !errors.Is(err, ErrEmptyCondition) {
		t.Fatalf("empty values: %v", err)
	}
	if _, err := repo.DeleteByID(context.Background(), "user", " "); !errors.Is(err, ErrEmptyCondition) {
		t.Fatalf("empty delete: %v", err)
	}
	if _, err := repo.UpdateByID(context.Background(), "user", "row", map[string]any{"id": "other"}); !errors.Is(err, ErrColumn) {
		t.Fatalf("column: %v", err)
	}
}
