package postgres

import (
	"context"
	"database/sql"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/zood/video-manager/internal/platform"
)

func TestSessionUsesRealAuthUID(t *testing.T) {
	dsn := startIsolatedPostgres(t)
	if _, err := Open(dsn); err == nil {
		t.Fatal("superuser connection was accepted")
	}
	appDSN := createFixture(t, dsn)
	source, err := Open(appDSN)
	if err != nil {
		t.Fatalf("open application role: %v", err)
	}
	t.Cleanup(func() { _ = source.db.Close() })

	known := "$2a$05$CCCCCCCCCCCCCCCCCCCCC.E5YPO9kmyuRGyh0XouQYb4YMJKvyOeW"
	if err := bcrypt.CompareHashAndPassword([]byte(known), []byte("U*U")); err != nil {
		t.Fatalf("bcrypt test vector is not compatible: %v", err)
	}

	ada := uuid.NewString()
	bea := uuid.NewString()
	admin := openDB(t, "postgres://postgres@127.0.0.1:55499/zood_test?sslmode=disable")
	if _, err := admin.Exec(`INSERT INTO public.user_profiles (id) VALUES ($1), ($2)`, ada, bea); err != nil {
		t.Fatal(err)
	}

	var leaked int
	if err := source.db.QueryRow(`SELECT count(*) FROM public.user_profiles`).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatalf("query without a verified session saw %d rows", leaked)
	}

	seen := map[string]string{}
	for _, id := range []string{ada, bea} {
		result, err := source.Select(context.Background(), platform.Caller{UserID: id, Token: "session"}, platform.Query{
			Table: "user_profiles", Select: "id", Limit: 10,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Rows) != 1 {
			t.Fatalf("user %s saw %#v", id, result.Rows)
		}
		seen[id], _ = result.Rows[0]["id"].(string)
	}
	if seen[ada] != ada || seen[bea] != bea {
		t.Fatalf("sessions crossed: %#v", seen)
	}
}

func startIsolatedPostgres(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	data := filepath.Join(base, "data")
	bin := "/usr/lib/postgresql/16/bin"
	run(t, bin+"/initdb", "-D", data, "-U", "postgres", "--auth=trust", "--no-sync")
	run(t, bin+"/pg_ctl", "-D", data, "-o", "-h 127.0.0.1 -p 55499 -k "+base, "-l", filepath.Join(base, "log"), "start")
	t.Cleanup(func() {
		_ = exec.Command(bin+"/pg_ctl", "-D", data, "-m", "immediate", "stop").Run()
	})
	dsn := "postgres://postgres@127.0.0.1:55499/postgres?sslmode=disable"
	waitReady(t, dsn)
	return dsn
}

func waitReady(t *testing.T, dsn string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		db, err := sql.Open("pgx", dsn)
		if err == nil {
			last = db.Ping()
			_ = db.Close()
			if last == nil {
				return
			}
		} else {
			last = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("postgres did not start: %v", last)
}

func openDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func createFixture(t *testing.T, adminDSN string) string {
	t.Helper()
	admin := openDB(t, adminDSN)
	for _, statement := range []string{
		`CREATE ROLE anon NOLOGIN NOSUPERUSER NOBYPASSRLS`,
		`CREATE ROLE authenticated NOLOGIN NOSUPERUSER NOBYPASSRLS`,
		`CREATE ROLE zood_app LOGIN NOSUPERUSER NOBYPASSRLS PASSWORD 'local-test-only'`,
		`GRANT anon, authenticated TO zood_app`,
		`CREATE DATABASE zood_test`,
	} {
		if _, err := admin.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	db := openDB(t, "postgres://postgres@127.0.0.1:55499/zood_test?sslmode=disable")
	for _, statement := range []string{
		`CREATE SCHEMA IF NOT EXISTS auth`,
		`CREATE TABLE public.user_profiles (id uuid PRIMARY KEY)`,
		`ALTER TABLE public.user_profiles ENABLE ROW LEVEL SECURITY`,
		`CREATE OR REPLACE FUNCTION auth.uid() RETURNS uuid LANGUAGE sql STABLE AS $$
			SELECT NULLIF(NULLIF(current_setting('request.jwt.claims', true), '')::jsonb ->> 'sub', '')::uuid $$`,
		`GRANT USAGE ON SCHEMA public TO authenticated`,
		`GRANT SELECT ON public.user_profiles TO authenticated`,
		`CREATE POLICY own_profile ON public.user_profiles FOR SELECT TO authenticated USING (id = auth.uid())`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	return "postgres://zood_app:local-test-only@127.0.0.1:55499/zood_test?sslmode=disable"
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", name, err, out)
	}
}
