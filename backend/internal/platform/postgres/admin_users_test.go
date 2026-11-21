package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/zood/video-manager/internal/platform"
)

func TestAdminUserListWithoutPlatformSchema(t *testing.T) {
	_, dsn := startPostgresOn(t, "55493")
	adminDB := openDB(t, dsn)
	for _, statement := range []string{
		`CREATE ROLE anon NOLOGIN NOSUPERUSER NOBYPASSRLS`,
		`CREATE ROLE authenticated NOLOGIN NOSUPERUSER NOBYPASSRLS`,
		`CREATE ROLE zood_app LOGIN NOSUPERUSER NOBYPASSRLS PASSWORD 'local-test-only'`,
		`GRANT anon, authenticated TO zood_app`,
		`CREATE DATABASE zood_admin_users`,
	} {
		if _, err := adminDB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	db := openDB(t, "postgres://postgres@127.0.0.1:55493/zood_admin_users?sslmode=disable")
	for _, statement := range []string{
		`CREATE SCHEMA auth`,
		`CREATE FUNCTION auth.uid() RETURNS uuid LANGUAGE sql STABLE AS $$
			SELECT NULLIF(NULLIF(current_setting('request.jwt.claims', true), '')::jsonb ->> 'sub', '')::uuid $$`,
		`GRANT USAGE ON SCHEMA auth TO anon, authenticated`,
		`CREATE TABLE public.user_profiles (
			id uuid PRIMARY KEY,
			username text NOT NULL,
			display_name text,
			avatar_url text,
			bio text,
			is_admin boolean NOT NULL DEFAULT false,
			vip_level integer NOT NULL DEFAULT 0,
			vip_expires_at timestamptz,
			created_at timestamptz DEFAULT now(),
			updated_at timestamptz
		)`,
		`CREATE TABLE public.video_access_logs (
			id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			user_id uuid NOT NULL,
			video_id uuid NOT NULL,
			segment_name text,
			watch_seconds integer,
			ip text,
			created_at timestamptz DEFAULT now()
		)`,
		`CREATE TABLE public.posts (
			id uuid PRIMARY KEY,
			author_id uuid,
			title text,
			slug text,
			content text,
			published boolean NOT NULL DEFAULT false,
			is_public boolean NOT NULL DEFAULT false,
			created_at timestamptz DEFAULT now()
		)`,
		`ALTER TABLE public.user_profiles ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE public.video_access_logs ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE public.posts ENABLE ROW LEVEL SECURITY`,
		`GRANT USAGE ON SCHEMA public TO anon, authenticated`,
		`GRANT SELECT ON public.user_profiles, public.video_access_logs, public.posts TO anon, authenticated`,
		`CREATE POLICY profiles_read ON public.user_profiles FOR SELECT TO anon, authenticated USING (true)`,
		`CREATE POLICY posts_read ON public.posts FOR SELECT TO anon, authenticated USING (published AND is_public)`,
		`CREATE POLICY logs_read ON public.video_access_logs FOR SELECT TO authenticated USING (
			user_id = auth.uid() OR EXISTS (
				SELECT 1 FROM public.user_profiles WHERE id = auth.uid() AND is_admin IS TRUE
			)
		)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%v\n%s", err, statement)
		}
	}
	var schemaCount int
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.schemata WHERE schema_name = 'platform'`).Scan(&schemaCount); err != nil || schemaCount != 0 {
		t.Fatalf("platform schema = %d (%v)", schemaCount, err)
	}
	adminID := uuid.NewString()
	memberID := uuid.NewString()
	postID := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO public.user_profiles (id, username, display_name, is_admin, vip_level) VALUES
		($1, 'root', 'Root', true, 0),
		($2, 'member', 'Member', false, 0)`, adminID, memberID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO public.video_access_logs (user_id, video_id, watch_seconds) VALUES ($1, $2, 12)`, memberID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO public.posts (id, title, slug, content, published, is_public) VALUES ($1, 'Public', 'public', 'body', true, true)`, postID); err != nil {
		t.Fatal(err)
	}

	source, err := Open("postgres://zood_app:local-test-only@127.0.0.1:55493/zood_admin_users?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	service := platform.NewService(source, nil, "postgres")
	service.SetPrivileged(source)
	ctx := context.Background()

	if _, err := service.AdminStudy(ctx, platform.Caller{}); statusOf(err) != 401 {
		t.Fatalf("anonymous: %v", err)
	}
	if _, err := service.AdminStudy(ctx, platform.Caller{UserID: memberID, Token: "session"}); statusOf(err) != 403 {
		t.Fatalf("member: %v", err)
	}
	result, err := service.AdminStudy(ctx, platform.Caller{UserID: adminID, Token: "session"})
	if err != nil {
		t.Fatal(err)
	}
	users := result["users"].([]map[string]any)
	if len(users) != 2 {
		t.Fatalf("users = %#v", users)
	}
	var member map[string]any
	for _, user := range users {
		if user["id"] == memberID {
			member = user
		}
		if user["email"] != nil || user["phone"] != nil {
			t.Fatalf("auth field leaked: %#v", user)
		}
	}
	if member == nil || member["isAdmin"] != false || member["vipLevel"] != int64(0) {
		t.Fatalf("member row = %#v", member)
	}
	if member["todayStudySeconds"] == 0 && member["yesterdayStudySeconds"] == 0 && member["totalStudySeconds"] == 0 {
		t.Fatal("admin could not read the member study log through the existing policy")
	}
	posts, err := source.Select(ctx, platform.Caller{}, platform.Query{
		Table:   "posts",
		Select:  "id,title",
		Filters: []platform.Filter{{Column: "published", Op: "eq", Value: "true"}},
		Limit:   10,
	})
	if err != nil || len(posts.Rows) != 1 || posts.Rows[0]["title"] != "Public" {
		t.Fatalf("public posts regressed: %+v %v", posts.Rows, err)
	}
}

func statusOf(err error) int {
	apiErr, ok := err.(*platform.Error)
	if !ok {
		return 0
	}
	return apiErr.Status
}
