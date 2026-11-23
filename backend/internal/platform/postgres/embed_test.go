package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/zood/video-manager/internal/platform"
)

func TestPublicPostsCategoriesAndJobs(t *testing.T) {
	_, dsn := startPostgresOn(t, "55497")
	admin := openDB(t, dsn)
	for _, statement := range []string{
		`CREATE ROLE anon NOLOGIN NOSUPERUSER NOBYPASSRLS`,
		`CREATE ROLE authenticated NOLOGIN NOSUPERUSER NOBYPASSRLS`,
		`CREATE ROLE zood_app LOGIN NOSUPERUSER NOBYPASSRLS PASSWORD 'local-test-only'`,
		`GRANT anon, authenticated TO zood_app`,
		`CREATE DATABASE zood_content`,
	} {
		if _, err := admin.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	db := openDB(t, "postgres://postgres@127.0.0.1:55497/zood_content?sslmode=disable")
	for _, statement := range []string{
		`CREATE TABLE public.user_profiles (id uuid PRIMARY KEY, username text, display_name text, avatar_url text, bio text, is_admin boolean NOT NULL DEFAULT false)`,
		`CREATE TABLE public.posts (
			id uuid PRIMARY KEY, author_id uuid, title text, slug text, excerpt text, banner text, content text,
			published boolean, is_public boolean, created_at timestamptz DEFAULT now(), updated_at timestamptz
		)`,
		`CREATE TABLE public.post_cates (id uuid PRIMARY KEY, name text, slug text, description text, created_at timestamptz DEFAULT now())`,
		`CREATE TABLE public.post_cate_relations (post_id uuid, cate_id uuid)`,
		`CREATE TABLE public.job_listings (
			id uuid PRIMARY KEY, title text, company_name text, company_logo text, location text,
			office_mode_name text, work_type_name text, min_salary numeric, max_salary numeric,
			source_created_at timestamptz, last_synced_at timestamptz DEFAULT now(),
			description text, tags jsonb, status integer NOT NULL DEFAULT 0, email text
		)`,
		`ALTER TABLE public.posts ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE public.user_profiles ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE public.post_cates ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE public.post_cate_relations ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE public.job_listings ENABLE ROW LEVEL SECURITY`,
		`GRANT USAGE ON SCHEMA public TO anon, authenticated`,
		`GRANT SELECT ON public.posts, public.user_profiles, public.post_cates, public.post_cate_relations, public.job_listings TO anon, authenticated`,
		`CREATE POLICY posts_public ON public.posts FOR SELECT TO anon, authenticated USING (published IS TRUE AND is_public IS TRUE)`,
		`CREATE POLICY profiles_public ON public.user_profiles FOR SELECT TO anon, authenticated USING (true)`,
		`CREATE POLICY cates_public ON public.post_cates FOR SELECT TO anon, authenticated USING (true)`,
		`CREATE POLICY relations_public ON public.post_cate_relations FOR SELECT TO anon, authenticated USING (true)`,
		`CREATE POLICY jobs_public ON public.job_listings FOR SELECT TO anon, authenticated USING (true)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%v\n%s", err, statement)
		}
	}
	author := uuid.NewString()
	postID := uuid.NewString()
	cateID := uuid.NewString()
	hidden := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO public.user_profiles (id, username, display_name) VALUES ($1, 'ada', 'Ada')`, author); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO public.posts (id, author_id, title, slug, content, published, is_public) VALUES
		($1, $2, 'Public', 'public', 'body', true, true),
		($3, $2, 'Draft', 'draft', 'body', false, false)`, postID, author, hidden); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO public.post_cates (id, name, slug) VALUES ($1, '笔记', 'notes')`, cateID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO public.post_cate_relations (post_id, cate_id) VALUES ($1, $2)`, postID, cateID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO public.job_listings (id, title, status, email) VALUES
		($1, 'Open', 0, 'hidden@example.com'),
		($2, 'Active', 1, 'also-hidden@example.com')`, uuid.NewString(), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	source, err := Open("postgres://zood_app:local-test-only@127.0.0.1:55497/zood_content?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.db.Close() })
	service := platform.NewService(source, nil, "postgres")
	anon := platform.Caller{}
	posts, err := service.ListPosts(context.Background(), anon, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts.Rows) != 1 {
		t.Fatalf("posts = %#v", posts.Rows)
	}
	authorRow, _ := posts.Rows[0]["author"].(map[string]any)
	if authorRow["username"] != "ada" {
		t.Fatalf("author = %#v", posts.Rows[0]["author"])
	}
	categories, err := service.ListCategories(context.Background(), anon)
	if err != nil {
		t.Fatal(err)
	}
	relations, _ := categories.Rows[0]["post_cate_relations"].([]map[string]any)
	if len(relations) != 1 {
		t.Fatalf("categories = %#v", categories.Rows)
	}
	detail, err := service.GetCategory(context.Background(), anon, "notes")
	if err != nil {
		t.Fatal(err)
	}
	linked, _ := detail["relations"].([]map[string]any)
	if len(linked) != 1 || linked[0]["posts"] == nil {
		t.Fatalf("category detail = %#v", detail)
	}
	jobs, err := service.ListJobs(context.Background(), anon, "", false, 1)
	if err != nil {
		t.Fatal(err)
	}
	if jobs.Count == nil || *jobs.Count != 2 || len(jobs.Rows) != 2 {
		t.Fatalf("jobs count/rows = %v %#v", jobs.Count, jobs.Rows)
	}
	for _, row := range jobs.Rows {
		if _, ok := row["email"]; ok {
			t.Fatalf("contact leaked: %#v", row)
		}
	}
	if _, err := platform.SQL(platform.Query{Table: "posts", Select: "id,secret(password)"}, false); err == nil {
		t.Fatal("unknown embed was accepted")
	}
}
