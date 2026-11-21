package postgres

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zood/video-manager/internal/platform"
)

// The previous regression used two profiles and one log. Both fit under the
// generic 200-row cap, so production (287 profiles, more than 200 logs) still
// failed closed. This test uses that production cardinality.
func TestAdminUserListPagesRealScaleWithoutPlatformSchema(t *testing.T) {
	_, dsn := startPostgresOn(t, "55496")
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
	db := openDB(t, "postgres://postgres@127.0.0.1:55496/zood_admin_users?sslmode=disable")
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
			is_admin boolean,
			vip_level integer,
			vip_expires_at timestamptz,
			created_at timestamptz,
			updated_at timestamptz
		)`,
		`CREATE TABLE public.video_access_logs (
			id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			user_id uuid NOT NULL,
			video_id uuid NOT NULL,
			segment_name text,
			watch_seconds integer,
			ip text,
			created_at timestamptz
		)`,
		`CREATE TABLE public.posts (
			id uuid PRIMARY KEY,
			title text,
			slug text,
			content text,
			published boolean NOT NULL DEFAULT false,
			is_public boolean NOT NULL DEFAULT false,
			created_at timestamptz DEFAULT now()
		)`,
		`ALTER TABLE public.user_profiles ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE public.user_profiles FORCE ROW LEVEL SECURITY`,
		`ALTER TABLE public.video_access_logs ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE public.video_access_logs FORCE ROW LEVEL SECURITY`,
		`ALTER TABLE public.posts ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE public.posts FORCE ROW LEVEL SECURITY`,
		`GRANT USAGE ON SCHEMA public TO anon, authenticated`,
		`GRANT SELECT ON public.user_profiles, public.video_access_logs, public.posts TO anon, authenticated`,
		`CREATE POLICY profiles_read ON public.user_profiles FOR SELECT TO anon, authenticated USING (true)`,
		`CREATE POLICY posts_read ON public.posts FOR SELECT TO anon, authenticated USING (published AND is_public)`,
		`CREATE POLICY logs_admin ON public.video_access_logs FOR SELECT TO authenticated USING (
			EXISTS (SELECT 1 FROM public.user_profiles WHERE id = auth.uid() AND is_admin IS TRUE)
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

	const total = 287
	tieAt := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	ids := make([]string, total)
	adminID := ""
	memberID := ""
	nullAdminID := ""
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	insertProfile, err := tx.Prepare(`INSERT INTO public.user_profiles
		(id, username, display_name, is_admin, vip_level, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < total; i++ {
		id := uuid.NewString()
		ids[i] = id
		username := "user-" + leftPad(i)
		display := "成员 " + leftPad(i)
		var isAdmin any
		var vip any = int64(0)
		var created any = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second)
		switch {
		case i < 6:
			isAdmin = true
			created = tieAt
			if adminID == "" {
				adminID = id
			}
		case i < 264:
			isAdmin = false
			if i == 6 {
				username = `100%_weird`
				display = "literal"
				memberID = id
			}
			if i >= 10 && i < 40 {
				display = "cohort member " + leftPad(i)
			}
			if i >= 40 && i < 50 {
				vip = nil
			}
			if i >= 50 && i < 56 {
				vip = int64(2)
			}
		default:
			isAdmin = nil
			created = nil
			if nullAdminID == "" {
				nullAdminID = id
			}
		}
		if _, err := insertProfile.Exec(id, username, display, isAdmin, vip, created); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	yesterdayStart, todayStart := shanghaiWindow(now, -1)
	_, todayEnd := shanghaiWindow(now, 0)
	if !todayStart.Equal(yesterdayStart.Add(24*time.Hour)) || !todayEnd.Equal(todayStart.Add(24*time.Hour)) {
		t.Fatal("shanghai window mismatch")
	}
	heavyID := ids[7]
	videos := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	insertLog, err := tx.Prepare(`INSERT INTO public.video_access_logs
		(user_id, video_id, segment_name, watch_seconds, ip, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 220; i++ {
		if _, err := insertLog.Exec(heavyID, videos[i%3], "secret-segment", 1, "203.0.113.50", todayStart.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	for _, log := range []struct {
		user    string
		seconds any
		at      time.Time
	}{
		{ids[8], 0, todayStart.Add(2 * time.Hour)},
		{ids[9], nil, todayStart.Add(3 * time.Hour)},
		{ids[11], 7, yesterdayStart},
		{ids[12], 8, todayStart},
		{ids[13], 999, yesterdayStart.Add(-time.Second)},
		{ids[14], 9, todayEnd},
	} {
		if _, err := insertLog.Exec(log.user, videos[0], "secret-segment", log.seconds, "203.0.113.50", log.at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO public.posts (id, title, slug, content, published, is_public)
		VALUES ($1, 'Public', 'public', 'body', true, true)`, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var ordered []string
	orderRows, err := db.Query(`SELECT id::text FROM public.user_profiles ORDER BY created_at DESC NULLS LAST, id ASC`)
	if err != nil {
		t.Fatal(err)
	}
	for orderRows.Next() {
		var id string
		if err := orderRows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ordered = append(ordered, id)
	}
	if err := orderRows.Err(); err != nil || len(ordered) != total {
		t.Fatalf("ordered = %d %v", len(ordered), err)
	}
	var tied []string
	tieRows, err := db.Query(`SELECT id::text FROM public.user_profiles WHERE created_at = $1 ORDER BY id ASC`, tieAt)
	if err != nil {
		t.Fatal(err)
	}
	for tieRows.Next() {
		var id string
		if err := tieRows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		tied = append(tied, id)
	}
	_ = tieRows.Close()
	if len(tied) != 6 {
		t.Fatalf("tied admins = %d", len(tied))
	}

	source, err := Open("postgres://zood_app:local-test-only@127.0.0.1:55496/zood_admin_users?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	service := platform.NewService(source, nil, "postgres")
	service.SetPrivileged(source)
	ctx := context.Background()
	admin := platform.Caller{UserID: adminID, Token: "session"}

	if _, err := service.AdminStudy(ctx, platform.Caller{}, url.Values{}); statusOf(err) != 401 {
		t.Fatalf("anonymous: %v", err)
	}
	if _, err := service.AdminStudy(ctx, platform.Caller{UserID: memberID, Token: "session"}, url.Values{"pageSize": {"999"}, "sort": {"phone"}}); statusOf(err) != 403 {
		t.Fatalf("member: %v", err)
	}
	if _, err := service.AdminStudy(ctx, platform.Caller{UserID: nullAdminID, Token: "session"}, url.Values{}); statusOf(err) != 403 {
		t.Fatalf("null admin: %v", err)
	}
	if _, err := service.AdminStudy(ctx, admin, url.Values{"pageSize": {"200"}}); statusOf(err) != 400 {
		t.Fatalf("page cap: %v", err)
	}

	seen := map[string]int{}
	var adminTrue, vipZero, vipNull int
	concat := make([]string, 0, total)
	for page := 1; ; page++ {
		result, err := service.AdminStudy(ctx, admin, url.Values{"page": {strconv.Itoa(page)}, "pageSize": {"50"}})
		if err != nil {
			t.Fatal(err)
		}
		rows := usersOf(t, result)
		summary := result["summary"].(map[string]any)
		pageInfo := result["page"].(map[string]any)
		if intOf(t, summary["userCount"]) != total || intOf(t, pageInfo["total"]) != total {
			t.Fatalf("totals = %#v %#v", summary, pageInfo)
		}
		if page == 1 {
			if intOf(t, summary["todayStudySeconds"]) != 228 || intOf(t, summary["yesterdayStudySeconds"]) != 7 {
				t.Fatalf("study summary = %#v", summary)
			}
			if intOf(t, summary["activeTodayCount"]) != 4 || intOf(t, summary["activeYesterdayCount"]) != 1 {
				t.Fatalf("active summary = %#v", summary)
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "203.0.113.50") || strings.Contains(string(encoded), "secret-segment") {
				t.Fatal("log secret leaked into the user list")
			}
		}
		if page > 6 && len(rows) != 0 {
			t.Fatalf("page %d = %d", page, len(rows))
		}
		if page == 6 && len(rows) != 37 {
			t.Fatalf("last page = %d", len(rows))
		}
		if page < 6 && len(rows) != 50 {
			t.Fatalf("page %d = %d", page, len(rows))
		}
		if page == 7 {
			break
		}
		for _, row := range rows {
			id := row["id"].(string)
			seen[id]++
			concat = append(concat, id)
			if row["isAdmin"] == true {
				adminTrue++
			}
			if row["vipLevel"] == int64(0) {
				vipZero++
			}
			if row["vipLevel"] == nil {
				vipNull++
			}
			if row["phone"] != nil || row["email"] != nil {
				t.Fatalf("auth field = %#v", row)
			}
		}
	}
	if len(seen) != total {
		t.Fatalf("unique users = %d", len(seen))
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("duplicate %s x%d", id, count)
		}
	}
	if adminTrue != 6 || vipZero != 271 || vipNull != 10 {
		t.Fatalf("flags true=%d vip0=%d vipNull=%d", adminTrue, vipZero, vipNull)
	}
	for i, id := range ordered {
		if concat[i] != id {
			t.Fatalf("order drifted at %d", i)
		}
	}
	again, err := service.AdminStudy(ctx, admin, url.Values{"page": {"3"}, "pageSize": {"50"}})
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range usersOf(t, again) {
		if row["id"] != ordered[(3-1)*50+i] {
			t.Fatal("page 3 was not stable")
		}
	}
	tiePage, err := service.AdminStudy(ctx, admin, url.Values{"pageSize": {"6"}})
	if err != nil {
		t.Fatal(err)
	}
	tieRowsPage := usersOf(t, tiePage)
	if len(tieRowsPage) != 6 {
		t.Fatal(len(tieRowsPage))
	}
	for i, row := range tieRowsPage {
		if row["id"] != tied[i] || row["createdAt"] == nil {
			t.Fatalf("tie order = %#v", row["id"])
		}
	}
	if _, ok := tieRowsPage[0]["createdAt"].(string); !ok {
		t.Fatal(tieRowsPage[0]["createdAt"])
	}

	ui := make([]string, 0, total)
	for page := 1; page <= 24; page++ {
		result, err := service.AdminStudy(ctx, admin, url.Values{"page": {strconv.Itoa(page)}, "pageSize": {"12"}})
		if err != nil {
			t.Fatal(err)
		}
		rows := usersOf(t, result)
		if page < 24 && len(rows) != 12 {
			t.Fatalf("ui page %d = %d", page, len(rows))
		}
		if page == 24 && len(rows) != 11 {
			t.Fatalf("ui last = %d", len(rows))
		}
		for _, row := range rows {
			ui = append(ui, row["id"].(string))
		}
	}
	if len(ui) != total {
		t.Fatalf("ui rows = %d", len(ui))
	}
	for i, id := range ordered {
		if ui[i] != id {
			t.Fatal("ui page order drifted")
		}
	}

	sorted, err := service.AdminStudy(ctx, admin, url.Values{"sort": {"today"}, "order": {"desc"}, "pageSize": {"1"}})
	if err != nil {
		t.Fatal(err)
	}
	top := usersOf(t, sorted)[0]
	if top["id"] != heavyID || top["todayStudySeconds"] != int64(220) || top["uniqueVideoCount"] != int64(3) || top["yesterdayStudySeconds"] != int64(0) {
		t.Fatalf("heavy = %#v", top)
	}
	quiet, err := service.AdminStudy(ctx, admin, url.Values{"q": {"user-0020"}, "pageSize": {"12"}})
	if err != nil {
		t.Fatal(err)
	}
	quietRows := usersOf(t, quiet)
	if len(quietRows) != 1 || quietRows[0]["todayStudySeconds"] != int64(0) || quietRows[0]["yesterdayStudySeconds"] != int64(0) || quietRows[0]["uniqueVideoCount"] != int64(0) || quietRows[0]["recentStudyAt"] != nil {
		t.Fatalf("quiet = %#v", quietRows)
	}
	if intOf(t, quiet["summary"].(map[string]any)["userCount"]) != total {
		t.Fatal("search changed the global user total")
	}
	literal, err := service.AdminStudy(ctx, admin, url.Values{"q": {`100%_weird`}})
	if err != nil {
		t.Fatal(err)
	}
	if intOf(t, literal["page"].(map[string]any)["total"]) != 1 || usersOf(t, literal)[0]["isAdmin"] != false || usersOf(t, literal)[0]["vipLevel"] != int64(0) {
		t.Fatalf("literal = %#v", literal["page"])
	}
	injected, err := service.AdminStudy(ctx, admin, url.Values{"q": {`%' OR username ILIKE '%`}})
	if err != nil {
		t.Fatal(err)
	}
	if len(usersOf(t, injected)) != 0 || intOf(t, injected["page"].(map[string]any)["total"]) != 0 {
		t.Fatal("filter injection changed the result")
	}
	cohortSeen := map[string]int{}
	for page := 1; page <= 4; page++ {
		result, err := service.AdminStudy(ctx, admin, url.Values{"q": {"cohort member"}, "page": {strconv.Itoa(page)}, "pageSize": {"12"}})
		if err != nil {
			t.Fatal(err)
		}
		rows := usersOf(t, result)
		want := 12
		if page == 3 {
			want = 6
		}
		if page == 4 {
			want = 0
		}
		if len(rows) != want || intOf(t, result["page"].(map[string]any)["total"]) != 30 {
			t.Fatalf("cohort page %d = %d %#v", page, len(rows), result["page"])
		}
		for _, row := range rows {
			cohortSeen[row["id"].(string)]++
		}
	}
	if len(cohortSeen) != 30 {
		t.Fatalf("cohort unique = %d", len(cohortSeen))
	}

	if _, err := db.Exec(`DELETE FROM public.video_access_logs`); err != nil {
		t.Fatal(err)
	}
	empty, err := service.AdminStudy(ctx, admin, url.Values{"pageSize": {"12"}})
	if err != nil {
		t.Fatal(err)
	}
	emptySummary := empty["summary"].(map[string]any)
	if intOf(t, emptySummary["userCount"]) != total || intOf(t, emptySummary["todayStudySeconds"]) != 0 || intOf(t, emptySummary["yesterdayStudySeconds"]) != 0 || intOf(t, emptySummary["activeTodayCount"]) != 0 {
		t.Fatalf("empty logs = %#v", emptySummary)
	}
	member, err := service.AdminStudy(ctx, admin, url.Values{"q": {memberID}})
	if err != nil {
		t.Fatal(err)
	}
	memberRow := usersOf(t, member)[0]
	if memberRow["isAdmin"] != false || memberRow["vipLevel"] != int64(0) || memberRow["todayStudySeconds"] != int64(0) {
		t.Fatalf("member after empty logs = %#v", memberRow)
	}

	memberLogs, err := source.Select(ctx, platform.Caller{UserID: memberID, Token: "session"}, platform.Query{
		Table: "video_access_logs", Select: "user_id,video_id,watch_seconds,created_at", Limit: 10,
	})
	if err != nil || len(memberLogs.Rows) != 0 {
		t.Fatalf("member logs = %+v %v", memberLogs.Rows, err)
	}
	posts, err := source.Select(ctx, platform.Caller{}, platform.Query{
		Table: "posts", Select: "id,title", Filters: []platform.Filter{{Column: "published", Op: "eq", Value: "true"}}, Limit: 10,
	})
	if err != nil || len(posts.Rows) != 1 || posts.Rows[0]["title"] != "Public" {
		t.Fatalf("public posts = %+v %v", posts.Rows, err)
	}
}

func statusOf(err error) int {
	apiErr, ok := err.(*platform.Error)
	if !ok {
		return 0
	}
	return apiErr.Status
}

func shanghaiWindow(now time.Time, offset int) (time.Time, time.Time) {
	shanghai := now.UTC().Add(8 * time.Hour)
	year, month, day := shanghai.Date()
	start := time.Date(year, month, day+offset, 0, 0, 0, 0, time.UTC).Add(-8 * time.Hour)
	return start, start.Add(24 * time.Hour)
}

func usersOf(t *testing.T, result map[string]any) []map[string]any {
	t.Helper()
	rows, ok := result["users"].([]map[string]any)
	if !ok {
		t.Fatalf("users type %T", result["users"])
	}
	return rows
}

func intOf(t *testing.T, value any) int64 {
	t.Helper()
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	default:
		t.Fatalf("number %T %#v", value, value)
		return 0
	}
}

func leftPad(value int) string {
	text := strconv.Itoa(value)
	for len(text) < 4 {
		text = "0" + text
	}
	return text
}
