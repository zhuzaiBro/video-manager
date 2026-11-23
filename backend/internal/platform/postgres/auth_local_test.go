package postgres

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/zood/video-manager/internal/platform"
)

func TestLocalPasswordAndAdminBoundary(t *testing.T) {
	base, dsn := startPostgresOn(t, "55498")
	_ = base
	admin := openDB(t, dsn)
	for _, statement := range []string{
		`CREATE ROLE schema_owner NOLOGIN NOSUPERUSER NOBYPASSRLS`,
		`CREATE ROLE anon NOLOGIN NOSUPERUSER NOBYPASSRLS`,
		`CREATE ROLE authenticated NOLOGIN NOSUPERUSER NOBYPASSRLS`,
		`CREATE ROLE zood_app LOGIN NOSUPERUSER NOBYPASSRLS PASSWORD 'local-test-only'`,
		`GRANT anon, authenticated TO zood_app`,
		`CREATE DATABASE zood_auth OWNER schema_owner`,
	} {
		if _, err := admin.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	db := openDB(t, "postgres://postgres@127.0.0.1:55498/zood_auth?sslmode=disable")
	for _, statement := range []string{
		`CREATE SCHEMA auth AUTHORIZATION schema_owner`,
		`CREATE TABLE auth.users (
			id uuid PRIMARY KEY,
			email text UNIQUE NOT NULL,
			encrypted_password text NOT NULL,
			email_confirmed_at timestamptz,
			phone text,
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE public.user_profiles (
			id uuid PRIMARY KEY,
			username text,
			display_name text,
			avatar_url text,
			is_admin boolean NOT NULL DEFAULT false,
			vip_level integer NOT NULL DEFAULT 0,
			vip_expires_at timestamptz
		)`,
		`CREATE TABLE public.interview_referral_rewards (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			referrer_user_id uuid NOT NULL,
			referred_user_id uuid UNIQUE NOT NULL,
			source_collection_id uuid,
			source_url text,
			reward_days integer,
			reward_vip_level integer
		)`,
		`CREATE TABLE public.video_access_logs (
			user_id text, video_id uuid, watch_seconds integer, created_at timestamptz default now()
		)`,
		`CREATE TABLE public.membership_consultation_requests (
			id integer PRIMARY KEY, status text, paid_at timestamptz, user_id uuid
		)`,
		`CREATE TABLE public.telegram_support_admins (
			chat_id bigint PRIMARY KEY, telegram_user_id bigint, username text, active boolean, updated_at timestamptz
		)`,
		`CREATE TABLE public.telegram_message_links (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(), session_id uuid, telegram_chat_id bigint,
			telegram_message_id bigint, telegram_update_id bigint, direction text
		)`,
		`CREATE TABLE public.messages (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(), session_id uuid, sender_type text, content text
		)`,
		`ALTER TABLE public.user_profiles ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE public.user_profiles OWNER TO schema_owner`,
		`ALTER TABLE auth.users OWNER TO schema_owner`,
		`CREATE OR REPLACE FUNCTION auth.uid() RETURNS uuid LANGUAGE sql STABLE AS $$
			SELECT NULLIF(NULLIF(current_setting('request.jwt.claims', true), '')::jsonb ->> 'sub', '')::uuid $$`,
		`GRANT USAGE ON SCHEMA public TO authenticated`,
		`GRANT SELECT, INSERT, UPDATE ON public.user_profiles TO authenticated`,
		`CREATE POLICY own_profile ON public.user_profiles FOR ALL TO authenticated USING (id = auth.uid()) WITH CHECK (id = auth.uid())`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%v\n%s", err, statement)
		}
	}
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "004_platform_local_auth.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		ALTER FUNCTION platform.password_hash_for_email(text) OWNER TO schema_owner;
		ALTER FUNCTION platform.register_password_user(uuid, text, text) OWNER TO schema_owner;
		ALTER FUNCTION platform.own_auth_account() OWNER TO schema_owner;
		ALTER FUNCTION platform.assert_admin() OWNER TO schema_owner;
		ALTER FUNCTION platform.admin_read_profile(uuid) OWNER TO schema_owner;
		ALTER FUNCTION platform.admin_set_vip(uuid, integer, timestamptz) OWNER TO schema_owner;
		ALTER FUNCTION platform.referral_exists(uuid) OWNER TO schema_owner;
		ALTER FUNCTION platform.insert_referral(uuid, uuid, uuid, text, integer, integer) OWNER TO schema_owner;
		ALTER FUNCTION platform.admin_list_auth_users() OWNER TO schema_owner;
		ALTER FUNCTION platform.admin_get_auth_user(uuid) OWNER TO schema_owner;
		ALTER FUNCTION platform.upsert_own_or_admin_profile(uuid, text, text, text) OWNER TO schema_owner;
		ALTER FUNCTION platform.admin_list_profiles() OWNER TO schema_owner;
		ALTER FUNCTION platform.admin_watch_logs(timestamptz) OWNER TO schema_owner;
		ALTER FUNCTION platform.admin_mark_consultation_paid(integer, uuid, timestamptz) OWNER TO schema_owner;
		GRANT USAGE ON SCHEMA auth, public, platform TO schema_owner;
	`); err != nil {
		t.Fatal(err)
	}
	source, err := Open("postgres://zood_app:local-test-only@127.0.0.1:55498/zood_auth?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.db.Close() })

	known := "$2a$05$CCCCCCCCCCCCCCCCCCCCC.E5YPO9kmyuRGyh0XouQYb4YMJKvyOeW"
	legacyID := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO auth.users (id, email, encrypted_password) VALUES ($1, 'legacy@example.com', $2)`, legacyID, known); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO public.user_profiles (id, is_admin) VALUES ($1, false)`, legacyID); err != nil {
		t.Fatal(err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(known), []byte("U*U")); err != nil {
		t.Fatalf("old bcrypt format rejected: %v", err)
	}
	newID := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO auth.users (id, email, encrypted_password, email_confirmed_at) VALUES ($1, 'new@example.com', $2, NULL)`, newID, known); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO public.user_profiles (id, is_admin) VALUES ($1, false)`, newID); err != nil {
		t.Fatal(err)
	}

	app := openDB(t, "postgres://zood_app:local-test-only@127.0.0.1:55498/zood_auth?sslmode=disable")
	if _, err := app.Exec(`SET ROLE authenticated`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Exec(`SELECT encrypted_password FROM auth.users`); err == nil {
		t.Fatal("authenticated role can read password hashes")
	}

	caller := platform.Caller{UserID: legacyID, Token: "session"}
	ctx := platform.WithCaller(context.Background(), caller)
	if err := source.UpdateVip(ctx, newID, 5, nil); err == nil {
		t.Fatal("non-admin changed another membership")
	}
	if _, err := db.Exec(`UPDATE public.user_profiles SET is_admin = true WHERE id = $1`, legacyID); err != nil {
		t.Fatal(err)
	}
	if err := source.UpdateVip(ctx, newID, 2, nil); err != nil {
		t.Fatalf("admin vip: %v", err)
	}
	users, err := source.ListAuthUsers(ctx)
	if err != nil || len(users) < 2 {
		t.Fatalf("admin list users: %d %v", len(users), err)
	}
}

func startPostgresOn(t *testing.T, port string) (string, string) {
	t.Helper()
	base := t.TempDir()
	data := filepath.Join(base, "data")
	bin := "/usr/lib/postgresql/16/bin"
	run(t, bin+"/initdb", "-D", data, "-U", "postgres", "--auth=trust", "--no-sync")
	run(t, bin+"/pg_ctl", "-D", data, "-o", "-h 127.0.0.1 -p "+port+" -k "+base, "-l", filepath.Join(base, "log"), "start")
	t.Cleanup(func() {
		_ = execStop(bin, data)
	})
	dsn := "postgres://postgres@127.0.0.1:" + port + "/postgres?sslmode=disable"
	waitReady(t, dsn)
	return base, dsn
}

func execStop(bin, data string) error {
	return exec.Command(bin+"/pg_ctl", "-D", data, "-m", "immediate", "stop").Run()
}
