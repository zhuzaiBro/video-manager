package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/zood/video-manager/internal/platform"
)

func TestCollectionDetailUsesExistingColumns(t *testing.T) {
	_, dsn := startPostgresOn(t, "55494")
	admin := openDB(t, dsn)
	for _, statement := range []string{
		`CREATE ROLE anon NOLOGIN NOSUPERUSER NOBYPASSRLS`,
		`CREATE ROLE authenticated NOLOGIN NOSUPERUSER NOBYPASSRLS`,
		`CREATE ROLE zood_app LOGIN NOSUPERUSER NOBYPASSRLS PASSWORD 'local-test-only'`,
		`GRANT anon, authenticated TO zood_app`,
		`CREATE DATABASE zood_collection`,
	} {
		if _, err := admin.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	db := openDB(t, "postgres://postgres@127.0.0.1:55494/zood_collection?sslmode=disable")
	for _, statement := range []string{
		`CREATE TABLE public.interview_collections (
			id uuid PRIMARY KEY,
			title text NOT NULL,
			description text,
			icon text,
			sort integer,
			created_at timestamptz DEFAULT now(),
			updated_at timestamptz DEFAULT now()
		)`,
		`CREATE TABLE public.interview_question (
			id uuid PRIMARY KEY,
			collection_id uuid,
			title text NOT NULL,
			content text,
			difficulty text,
			is_vip boolean,
			vip_level_required integer,
			sort integer,
			created_at timestamptz DEFAULT now(),
			updated_at timestamptz DEFAULT now()
		)`,
		`CREATE TABLE public.interview_tags (
			id uuid PRIMARY KEY,
			name text NOT NULL,
			slug text NOT NULL,
			sort integer,
			created_at timestamptz DEFAULT now()
		)`,
		`CREATE TABLE public.interview_question_tags (
			question_id uuid NOT NULL,
			tag_id uuid NOT NULL
		)`,
		`GRANT USAGE ON SCHEMA public TO anon, authenticated`,
		`GRANT SELECT ON public.interview_collections, public.interview_question, public.interview_tags, public.interview_question_tags TO anon, authenticated`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%v\n%s", err, statement)
		}
	}
	collectionID := uuid.NewString()
	questionID := uuid.NewString()
	tagID := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO public.interview_collections (id, title, description, icon) VALUES ($1, 'Go 基础', '旧题集', 'go')`, collectionID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO public.interview_question (id, collection_id, title, content, difficulty, is_vip, sort) VALUES ($1, $2, '旧题还在', '答案', '中等', false, 1)`, questionID, collectionID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO public.interview_tags (id, name, slug) VALUES ($1, 'Go', 'go')`, tagID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO public.interview_question_tags (question_id, tag_id) VALUES ($1, $2)`, questionID, tagID); err != nil {
		t.Fatal(err)
	}
	var missing string
	err := db.QueryRow(`SELECT column_name FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'interview_collections' AND column_name = 'cover_image'`).Scan(&missing)
	if err == nil {
		t.Fatal("fixture unexpectedly has cover_image")
	}

	source, err := Open("postgres://zood_app:local-test-only@127.0.0.1:55494/zood_collection?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	service := platform.NewService(source, nil, "postgres")
	detail, err := service.GetCollection(context.Background(), platform.Caller{}, collectionID)
	if err != nil {
		t.Fatalf("collection detail: %v", err)
	}
	collection := detail["collection"].(map[string]any)
	if collection["title"] != "Go 基础" || collection["icon"] != "go" {
		t.Fatalf("collection = %#v", collection)
	}
	if _, ok := collection["cover_image"]; ok {
		t.Fatalf("detail invented cover_image: %#v", collection)
	}
	questions := detail["questions"].([]map[string]any)
	if len(questions) != 1 || questions[0]["title"] != "旧题还在" {
		t.Fatalf("questions = %#v", questions)
	}

	question, err := service.GetQuestion(context.Background(), platform.Caller{}, questionID)
	if err != nil {
		t.Fatalf("question: %v", err)
	}
	row := question["question"].(map[string]any)
	if row["title"] != "旧题还在" {
		t.Fatalf("question row = %#v", row)
	}
	navigation := question["navigation"].(map[string]any)
	if navigation["count"] != 1 {
		t.Fatalf("navigation = %#v", navigation)
	}
}
