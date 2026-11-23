package postgres

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/zood/video-manager/internal/platform"
)

func TestInterviewRoleMigrationAssignsOnlyTheProgrammerHotList(t *testing.T) {
	_, dsn := startPostgresOn(t, "55493")
	admin := openDB(t, dsn)
	for _, statement := range []string{
		`CREATE ROLE anon NOLOGIN NOSUPERUSER NOBYPASSRLS`,
		`CREATE ROLE authenticated NOLOGIN NOSUPERUSER NOBYPASSRLS`,
		`CREATE ROLE zood_app LOGIN NOSUPERUSER NOBYPASSRLS PASSWORD 'local-test-only'`,
		`GRANT anon, authenticated TO zood_app`,
		`CREATE DATABASE zood_interview_roles`,
	} {
		if _, err := admin.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	owner := openDB(t, "postgres://postgres@127.0.0.1:55493/zood_interview_roles?sslmode=disable")
	for _, statement := range []string{
		`CREATE TABLE public.interview_collections (
			id uuid PRIMARY KEY,
			title text NOT NULL,
			description text,
			icon text,
			sort integer,
			created_at timestamptz DEFAULT now()
		)`,
		`CREATE TABLE public.interview_question (
			id uuid PRIMARY KEY,
			collection_id uuid REFERENCES public.interview_collections (id) ON DELETE SET NULL,
			title text NOT NULL,
			content text,
			sort integer
		)`,
		`CREATE TABLE public.interview_tags (
			id uuid PRIMARY KEY,
			name text NOT NULL,
			slug text NOT NULL UNIQUE,
			sort integer,
			created_at timestamptz DEFAULT now()
		)`,
		`CREATE TABLE public.interview_collection_tags (
			id uuid PRIMARY KEY,
			collection_id uuid NOT NULL REFERENCES public.interview_collections (id) ON DELETE CASCADE,
			tag_id uuid NOT NULL REFERENCES public.interview_tags (id) ON DELETE CASCADE
		)`,
		`GRANT USAGE ON SCHEMA public TO anon, authenticated`,
		`GRANT SELECT ON public.interview_collections, public.interview_question, public.interview_tags, public.interview_collection_tags TO anon, authenticated`,
		`ALTER TABLE public.interview_collections ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE public.interview_question ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE public.interview_tags ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE public.interview_collection_tags ENABLE ROW LEVEL SECURITY`,
		`CREATE POLICY interview_collections_select ON public.interview_collections FOR SELECT TO PUBLIC USING (true)`,
		`CREATE POLICY interview_question_select ON public.interview_question FOR SELECT TO PUBLIC USING (true)`,
		`CREATE POLICY interview_tags_select ON public.interview_tags FOR SELECT TO PUBLIC USING (true)`,
		`CREATE POLICY interview_collection_tags_select ON public.interview_collection_tags FOR SELECT TO PUBLIC USING (true)`,
	} {
		if _, err := owner.Exec(statement); err != nil {
			t.Fatalf("%v\n%s", err, statement)
		}
	}
	hotA := "11111111-1111-4111-8111-111111111111"
	hotB := "44444444-4444-4444-8444-444444444444"
	contractID := "f1a2b3c4-d5e6-4f78-9abc-def010000001"
	backendOnly := "55555555-5555-4555-8555-555555555555"
	designID := "22222222-2222-4222-8222-222222222222"
	for _, statement := range []string{
		`INSERT INTO public.interview_tags (id, name, slug, sort) VALUES
			('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1', '热门', 'hot', 0),
			('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa2', 'Web3', 'web3', 1),
			('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa3', 'web3后端', 'web3Backend', 2)`,
		`INSERT INTO public.interview_collections (id, title, sort) VALUES
			('` + hotA + `', 'Go 热门', 1),
			('` + hotB + `', 'React 热门', 2),
			('` + contractID + `', '智能合约面试题', 3),
			('` + backendOnly + `', '仅后端标签', 4),
			('` + designID + `', '设计作品集', 5)`,
		`INSERT INTO public.interview_collection_tags (id, collection_id, tag_id) VALUES
			('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb1', '` + hotA + `', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1'),
			('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb2', '` + hotB + `', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1'),
			('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb3', '` + contractID + `', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa2'),
			('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb4', '` + backendOnly + `', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa3')`,
		`INSERT INTO public.interview_question (id, collection_id, title) VALUES
			('cccccccc-cccc-4ccc-8ccc-ccccccccccc1', '` + hotA + `', '旧热门题'),
			('cccccccc-cccc-4ccc-8ccc-ccccccccccc2', '` + contractID + `', '旧合约题'),
			('cccccccc-cccc-4ccc-8ccc-ccccccccccc3', '` + designID + `', '旧设计题')`,
	} {
		if _, err := owner.Exec(statement); err != nil {
			t.Fatalf("%v\n%s", err, statement)
		}
	}

	script, err := os.ReadFile("../../../migrations/006_interview_collection_roles.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "DROP TABLE IF EXISTS public.interview_collection_roles;") {
		t.Fatal("rollback is not documented")
	}
	for _, line := range strings.Split(string(script), "\n") {
		code := strings.TrimSpace(line)
		if strings.HasPrefix(code, "--") {
			continue
		}
		if strings.Contains(strings.ToLower(code), "drop table") || strings.Contains(strings.ToLower(code), "delete from") {
			t.Fatalf("migration body changes existing rows: %s", code)
		}
	}
	if _, err := owner.Exec(string(script)); err != nil {
		t.Fatal(err)
	}

	var programmerRows, otherRows, contractRows int
	if err := owner.QueryRow(`SELECT count(*) FROM public.interview_collection_roles WHERE role_slug = 'programmer'`).Scan(&programmerRows); err != nil {
		t.Fatal(err)
	}
	if programmerRows != 2 {
		t.Fatalf("programmer backfill = %d", programmerRows)
	}
	if err := owner.QueryRow(`SELECT count(*) FROM public.interview_collection_roles WHERE role_slug <> 'programmer'`).Scan(&otherRows); err != nil {
		t.Fatal(err)
	}
	if otherRows != 0 {
		t.Fatalf("other roles were backfilled: %d", otherRows)
	}
	if err := owner.QueryRow(`SELECT count(*) FROM public.interview_collection_roles WHERE collection_id = $1`, contractID).Scan(&contractRows); err != nil {
		t.Fatal(err)
	}
	if contractRows != 0 {
		t.Fatal("web3-only contract collection was assigned")
	}

	appDSN := "postgres://zood_app:local-test-only@127.0.0.1:55493/zood_interview_roles?sslmode=disable"
	source, err := Open(appDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Pool().Close() })
	service := platform.NewService(source, nil, "postgres")
	home, err := service.InterviewHome(context.Background(), platform.Caller{}, "", "programmer")
	if err != nil {
		t.Fatal(err)
	}
	if home["effectiveTag"] != "hot" || !titlesAre(home["collections"], "Go 热门", "React 热门") {
		t.Fatalf("programmer default = %#v tag %#v", home["collections"], home["effectiveTag"])
	}
	web3, err := service.InterviewHome(context.Background(), platform.Caller{}, "web3", "programmer")
	if err != nil {
		t.Fatal(err)
	}
	if !titlesAre(web3["collections"], "智能合约面试题") {
		t.Fatalf("web3 = %#v", web3["collections"])
	}
	design, err := service.InterviewHome(context.Background(), platform.Caller{}, "", "design")
	if err != nil {
		t.Fatal(err)
	}
	if !titlesAre(design["collections"]) || design["effectiveTag"] != "" {
		t.Fatalf("design before assignment = %#v", design)
	}

	app := openDB(t, appDSN)
	if _, err := app.Exec(`INSERT INTO public.interview_collection_roles (collection_id, role_slug) VALUES ($1, 'design')`, designID); err != nil {
		t.Fatalf("app role could not assign design: %v", err)
	}
	anonTx, err := app.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := anonTx.Exec(`SET LOCAL ROLE anon`); err != nil {
		t.Fatal(err)
	}
	if _, err := anonTx.Exec(`INSERT INTO public.interview_collection_roles (collection_id, role_slug) VALUES ($1, 'crypto-ops')`, hotA); err == nil {
		t.Fatal("anon inserted a job assignment")
	}
	_ = anonTx.Rollback()

	design, err = service.InterviewHome(context.Background(), platform.Caller{}, "web3", "design")
	if err != nil {
		t.Fatal(err)
	}
	if !titlesAre(design["collections"], "设计作品集") || len(design["tags"].([]map[string]any)) != 0 {
		t.Fatalf("design = %#v", design)
	}
	counts := design["questionCounts"].(map[string]int)
	if counts[designID] != 1 || len(counts) != 1 {
		t.Fatalf("design counts = %#v", counts)
	}
	home, err = service.InterviewHome(context.Background(), platform.Caller{}, "", "programmer")
	if err != nil {
		t.Fatal(err)
	}
	if !titlesAre(home["collections"], "Go 热门", "React 热门") {
		t.Fatalf("programmer default after design assignment = %#v", home["collections"])
	}
	for _, role := range []string{"cross-border-ops", "crypto-bd", "cross-border-bd", "crypto-ops"} {
		other, err := service.InterviewHome(context.Background(), platform.Caller{}, "hot", role)
		if err != nil {
			t.Fatal(err)
		}
		if !titlesAre(other["collections"]) {
			t.Fatalf("%s = %#v", role, other["collections"])
		}
	}

	throwaway := "66666666-6666-4666-8666-666666666666"
	if _, err := owner.Exec(`INSERT INTO public.interview_collections (id, title) VALUES ($1, '临时题集')`, throwaway); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(`INSERT INTO public.interview_question (id, collection_id, title) VALUES ('dddddddd-dddd-4ddd-8ddd-ddddddddddd1', $1, '临时题')`, throwaway); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(`INSERT INTO public.interview_collection_roles (collection_id, role_slug) VALUES ($1, 'programmer')`, throwaway); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(`DELETE FROM public.interview_collections WHERE id = $1`, throwaway); err != nil {
		t.Fatal(err)
	}
	var questionTitle string
	var questionCollection interface{}
	if err := owner.QueryRow(`SELECT title, collection_id FROM public.interview_question WHERE id = 'dddddddd-dddd-4ddd-8ddd-ddddddddddd1'`).Scan(&questionTitle, &questionCollection); err != nil {
		t.Fatal(err)
	}
	if questionTitle != "临时题" || questionCollection != nil {
		t.Fatalf("question changed with its collection: %s %#v", questionTitle, questionCollection)
	}
	var leftover int
	if err := owner.QueryRow(`SELECT count(*) FROM public.interview_collection_roles WHERE collection_id = $1`, throwaway).Scan(&leftover); err != nil {
		t.Fatal(err)
	}
	if leftover != 0 {
		t.Fatal("assignment survived the collection delete")
	}

	if _, err := owner.Exec(`DROP TABLE IF EXISTS public.interview_collection_roles`); err != nil {
		t.Fatal(err)
	}
	var collections, questions int
	if err := owner.QueryRow(`SELECT count(*) FROM public.interview_collections`).Scan(&collections); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRow(`SELECT count(*) FROM public.interview_question`).Scan(&questions); err != nil {
		t.Fatal(err)
	}
	if collections != 5 || questions != 4 {
		t.Fatalf("rollback changed old rows: collections %d questions %d", collections, questions)
	}
	var oldTitle string
	if err := owner.QueryRow(`SELECT title FROM public.interview_question WHERE id = 'cccccccc-cccc-4ccc-8ccc-ccccccccccc1'`).Scan(&oldTitle); err != nil {
		t.Fatal(err)
	}
	if oldTitle != "旧热门题" {
		t.Fatalf("old question = %s", oldTitle)
	}
	home, err = service.InterviewHome(context.Background(), platform.Caller{}, "", "programmer")
	if err != nil {
		t.Fatal(err)
	}
	if !titlesAre(home["collections"], "Go 热门", "React 热门") {
		t.Fatalf("programmer default after rollback = %#v", home["collections"])
	}
	design, err = service.InterviewHome(context.Background(), platform.Caller{}, "", "design")
	if err != nil {
		t.Fatal(err)
	}
	if !titlesAre(design["collections"]) {
		t.Fatalf("design after rollback = %#v", design["collections"])
	}
}

func titlesAre(value any, want ...string) bool {
	rows, _ := value.([]map[string]any)
	if len(rows) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, row := range rows {
		title, _ := row["title"].(string)
		seen[title]++
	}
	for _, title := range want {
		seen[title]--
		if seen[title] < 0 {
			return false
		}
	}
	return true
}
