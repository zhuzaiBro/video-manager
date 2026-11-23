package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zood/video-manager/internal/gormplus"
	"github.com/zood/video-manager/internal/platform"
	"github.com/zood/video-manager/internal/platform/content"
	"gorm.io/gorm"
)

type item struct {
	ID      string `gorm:"column:id"`
	OwnerID string `gorm:"column:owner_id"`
	Active  bool   `gorm:"column:active"`
	Qty     int    `gorm:"column:qty"`
}

func (item) TableName() string { return "items" }

func TestPostsCategoriesAndOwnership(t *testing.T) {
	_, dsn := startPostgresOn(t, "55496")
	admin := openDB(t, dsn)
	for _, statement := range []string{
		`CREATE ROLE anon NOLOGIN NOSUPERUSER NOBYPASSRLS`,
		`CREATE ROLE authenticated NOLOGIN NOSUPERUSER NOBYPASSRLS`,
		`CREATE ROLE zood_app LOGIN NOSUPERUSER NOBYPASSRLS PASSWORD 'local-test-only'`,
		`GRANT anon, authenticated TO zood_app`,
		`CREATE DATABASE zood_gormplus`,
	} {
		if _, err := admin.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	db := openDB(t, "postgres://postgres@127.0.0.1:55496/zood_gormplus?sslmode=disable")
	for _, statement := range []string{
		`CREATE SCHEMA auth`,
		`CREATE FUNCTION auth.uid() RETURNS uuid LANGUAGE sql STABLE AS $$
			SELECT NULLIF(NULLIF(current_setting('request.jwt.claims', true), '')::jsonb ->> 'sub', '')::uuid $$`,
		`GRANT USAGE ON SCHEMA auth TO anon, authenticated`,
		`CREATE TABLE public.user_profiles (id uuid PRIMARY KEY, username text, display_name text, avatar_url text, bio text, is_admin boolean NOT NULL DEFAULT false)`,
		`CREATE TABLE public.posts (
			id uuid PRIMARY KEY, author_id uuid, title text, slug text, excerpt text, banner text, content text,
			published boolean NOT NULL DEFAULT false, is_public boolean NOT NULL DEFAULT false,
			created_at timestamptz DEFAULT now(), updated_at timestamptz
		)`,
		`CREATE TABLE public.post_cates (id uuid PRIMARY KEY, name text, slug text, description text, created_at timestamptz DEFAULT now())`,
		`CREATE TABLE public.post_cate_relations (post_id uuid, cate_id uuid)`,
		`CREATE TABLE public.job_listings (
			id uuid PRIMARY KEY, title text, company_name text, company_logo text, location text,
			office_mode_name text, work_type_name text, min_salary numeric, max_salary numeric,
			source_created_at timestamptz, last_synced_at timestamptz DEFAULT now(),
			description text, tags jsonb, status integer NOT NULL DEFAULT 0, email text
		)`,
		`CREATE TABLE public.items (id uuid PRIMARY KEY, owner_id uuid, active boolean NOT NULL DEFAULT true, qty integer NOT NULL DEFAULT 1)`,
		`ALTER TABLE public.posts ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE public.user_profiles ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE public.post_cates ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE public.post_cate_relations ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE public.job_listings ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE public.items ENABLE ROW LEVEL SECURITY`,
		`GRANT USAGE ON SCHEMA public TO anon, authenticated`,
		`GRANT SELECT ON public.posts, public.user_profiles, public.post_cates, public.post_cate_relations, public.job_listings, public.items TO anon, authenticated`,
		`GRANT UPDATE, DELETE ON public.posts TO authenticated`,
		`GRANT UPDATE, DELETE ON public.items TO authenticated`,
		`CREATE POLICY posts_public ON public.posts FOR SELECT TO anon, authenticated USING (published AND is_public)`,
		`CREATE POLICY posts_owner_read ON public.posts FOR SELECT TO authenticated USING (author_id = auth.uid())`,
		`CREATE POLICY posts_owner_write ON public.posts FOR UPDATE TO authenticated USING (author_id = auth.uid()) WITH CHECK (author_id = auth.uid())`,
		`CREATE POLICY posts_owner_delete ON public.posts FOR DELETE TO authenticated USING (author_id = auth.uid())`,
		`CREATE POLICY profiles_public ON public.user_profiles FOR SELECT TO anon, authenticated USING (true)`,
		`CREATE POLICY cates_public ON public.post_cates FOR SELECT TO anon, authenticated USING (true)`,
		`CREATE POLICY relations_public ON public.post_cate_relations FOR SELECT TO anon, authenticated USING (true)`,
		`CREATE POLICY jobs_public ON public.job_listings FOR SELECT TO anon, authenticated USING (true)`,
		`CREATE POLICY items_owner ON public.items FOR ALL TO authenticated USING (owner_id = auth.uid()) WITH CHECK (owner_id = auth.uid())`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%v\n%s", err, statement)
		}
	}
	author := uuid.NewString()
	other := uuid.NewString()
	adminUser := uuid.NewString()
	postID := uuid.NewString()
	draftID := uuid.NewString()
	cateID := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO public.user_profiles (id, username, display_name, is_admin) VALUES
		($1, 'ada', 'Ada', false), ($2, 'bob', 'Bob', false), ($3, 'root', 'Root', true)`, author, other, adminUser); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO public.posts (id, author_id, title, slug, content, published, is_public) VALUES
		($1, $2, 'Public', 'public', 'body', true, true),
		($3, $2, 'Draft', 'draft', 'body', false, false)`, postID, author, draftID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO public.post_cates (id, name, slug) VALUES ($1, '笔记', 'notes')`, cateID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO public.post_cate_relations (post_id, cate_id) VALUES ($1, $3), ($2, $3)`, postID, draftID, cateID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := db.Exec(`INSERT INTO public.job_listings (id, title, status, email) VALUES ($1, $2, $3, 'hidden@example.com')`,
			uuid.NewString(), "Role", i%2); err != nil {
			t.Fatal(err)
		}
	}
	itemID := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO public.items (id, owner_id, active, qty) VALUES ($1, $2, true, 4)`, itemID, author); err != nil {
		t.Fatal(err)
	}

	source, err := Open("postgres://zood_app:local-test-only@127.0.0.1:55496/zood_gormplus?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := gormplus.Use(source.Pool(), gormplus.Platform)
	if err != nil {
		t.Fatal(err)
	}
	videoPool, err := gormplus.Use(source.Pool(), gormplus.Video)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gormplus.NewRepo[content.Post](videoPool, []string{"id"}, nil); !errors.Is(err, gormplus.ErrWrongDatabase) {
		t.Fatalf("video kind: %v", err)
	}
	catalog, err := content.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	service := platform.NewService(source, nil, "postgres")
	service.SetCatalog(catalog)
	ctx := context.Background()

	anon, err := service.ListPosts(ctx, platform.Caller{}, 10)
	if err != nil || len(anon.Rows) != 1 || anon.Rows[0]["slug"] != "public" {
		t.Fatalf("anon posts: %+v %v", anon.Rows, err)
	}
	authorRow, _ := anon.Rows[0]["author"].(map[string]any)
	if authorRow["username"] != "ada" {
		t.Fatalf("author: %#v", anon.Rows[0]["author"])
	}
	categories, err := service.ListCategories(ctx, platform.Caller{})
	if err != nil || len(categories.Rows) != 1 {
		t.Fatalf("categories: %+v %v", categories.Rows, err)
	}
	relations, _ := categories.Rows[0]["post_cate_relations"].([]map[string]any)
	if len(relations) != 2 {
		t.Fatalf("relation count: %#v", categories.Rows[0]["post_cate_relations"])
	}
	detail, err := service.GetCategory(ctx, platform.Caller{}, "notes")
	if err != nil {
		t.Fatal(err)
	}
	visible, _ := detail["relations"].([]map[string]any)
	if len(visible) != 1 {
		t.Fatalf("anon category detail: %#v", detail["relations"])
	}
	ownerDetail, err := service.GetCategory(ctx, platform.Caller{UserID: author}, "notes")
	if err != nil {
		t.Fatal(err)
	}
	owned, _ := ownerDetail["relations"].([]map[string]any)
	if len(owned) != 2 {
		t.Fatalf("author category detail: %#v", ownerDetail["relations"])
	}

	affected, err := catalog.UpdatePost(ctx, author, postID, map[string]any{"published": false})
	if err != nil || affected != 1 {
		t.Fatalf("owner update: %d %v", affected, err)
	}
	var published bool
	if err := db.QueryRow(`SELECT published FROM public.posts WHERE id = $1`, postID).Scan(&published); err != nil || published {
		t.Fatalf("false was stored as %v (%v)", published, err)
	}
	affected, err = catalog.UpdatePost(ctx, other, postID, map[string]any{"published": true})
	if err != nil || affected != 0 {
		t.Fatalf("other user update: %d %v", affected, err)
	}
	affected, err = catalog.UpdatePost(ctx, adminUser, postID, map[string]any{"published": true})
	if err != nil || affected != 0 {
		t.Fatalf("admin flag update: %d %v", affected, err)
	}
	if err := db.QueryRow(`SELECT published FROM public.posts WHERE id = $1`, postID).Scan(&published); err != nil || published {
		t.Fatalf("published changed: %v %v", published, err)
	}
	if _, err := catalog.UpdatePost(ctx, author, postID, map[string]any{}); !errors.Is(err, gormplus.ErrEmptyCondition) {
		t.Fatalf("empty update: %v", err)
	}
	if _, err := catalog.UpdatePost(ctx, author, postID, map[string]any{"author_id": other}); !errors.Is(err, gormplus.ErrColumn) {
		t.Fatalf("column update: %v", err)
	}

	items, err := gormplus.NewRepo[item](pool, []string{"id", "owner_id"}, []string{"active", "qty"})
	if err != nil {
		t.Fatal(err)
	}
	affected, err = items.UpdateByID(ctx, author, itemID, map[string]any{"active": false, "qty": 0})
	if err != nil || affected != 1 {
		t.Fatalf("zero update: %d %v", affected, err)
	}
	var active bool
	var qty int
	if err := db.QueryRow(`SELECT active, qty FROM public.items WHERE id = $1`, itemID).Scan(&active, &qty); err != nil || active || qty != 0 {
		t.Fatalf("stored active=%v qty=%d (%v)", active, qty, err)
	}
	if _, err := items.DeleteByID(ctx, other, itemID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT qty FROM public.items WHERE id = $1`, itemID).Scan(&qty); err != nil {
		t.Fatalf("other user deleted the row: %v", err)
	}

	jobs, err := service.ListJobs(ctx, platform.Caller{}, `%' OR title ILIKE '%`, false, 1)
	if err != nil || len(jobs.Rows) != 0 {
		t.Fatalf("injection: %+v %v", jobs.Rows, err)
	}
	page, err := service.ListJobs(ctx, platform.Caller{}, "", false, 1)
	if err != nil || page.Count == nil || *page.Count != 5 {
		t.Fatalf("jobs: %+v %v", page, err)
	}
	for _, row := range page.Rows {
		if _, ok := row["email"]; ok {
			t.Fatalf("contact leaked: %#v", row)
		}
	}
	narrow, _, err := catalog.ListJobs(ctx, "", "", false, 2, 2)
	if err != nil || len(narrow) != 2 {
		t.Fatalf("page: %d %v", len(narrow), err)
	}
	posts, err := gormplus.NewRepo[content.Post](pool, []string{"created_at"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := posts.Page(ctx, "", 1, 10, func(q *gormplus.Query) {
		q.Order("created_at;drop table posts", true)
	}); !errors.Is(err, gormplus.ErrColumn) {
		t.Fatalf("order: %v", err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := service.ListPosts(canceled, platform.Caller{}, 10); err == nil {
		t.Fatal("canceled context was ignored")
	}

	var group sync.WaitGroup
	counts := make(chan int64, 2)
	group.Add(2)
	go func() {
		defer group.Done()
		_ = pool.Do(context.Background(), "", func(tx *gorm.DB) error {
			time.Sleep(150 * time.Millisecond)
			var count int64
			if err := tx.Raw(`SELECT count(*) FROM public.posts WHERE published = false`).Scan(&count).Error; err != nil {
				t.Errorf("anon count: %v", err)
			}
			counts <- count
			return nil
		})
	}()
	go func() {
		defer group.Done()
		_ = pool.Do(context.Background(), author, func(tx *gorm.DB) error {
			var count int64
			if err := tx.Raw(`SELECT count(*) FROM public.posts WHERE published = false`).Scan(&count).Error; err != nil {
				t.Errorf("author count: %v", err)
			}
			counts <- count
			return nil
		})
	}()
	group.Wait()
	close(counts)
	seen := []int64{}
	for count := range counts {
		seen = append(seen, count)
	}
	if len(seen) != 2 || (seen[0] == seen[1]) {
		t.Fatalf("role leaked across requests: %v", seen)
	}
}
