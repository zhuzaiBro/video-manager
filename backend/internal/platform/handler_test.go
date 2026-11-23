package platform

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/zood/video-manager/internal/auth"
	"github.com/zood/video-manager/internal/config"
)

type fakeSource struct {
	mu      sync.Mutex
	queries []Query
	writes  []storedWrite
	tables  map[string][]map[string]any
}

type storedWrite struct {
	Method  string
	Query   Query
	Payload any
}

func (f *fakeSource) Select(_ context.Context, _ Caller, query Query) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, query)
	return Result{Rows: f.tables[query.Table]}, nil
}

func (f *fakeSource) Write(_ context.Context, _ Caller, method string, query Query, payload any) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, storedWrite{Method: method, Query: query, Payload: payload})
	return Result{Rows: []map[string]any{{"id": "ok"}}}, nil
}

func testRouter(source *fakeSource) *gin.Engine {
	gin.SetMode(gin.TestMode)
	validator := auth.NewValidator(&config.Config{
		SupabaseJWTSecret: "test-secret",
		SupabaseURL:       "https://gmfuorleragagsqbkajh.supabase.co",
	})
	r := gin.New()
	NewHandler(NewService(source, nil, "supabase"), validator).Register(r)
	return r
}

func signUser(t *testing.T, userID string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.SupabaseClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			Issuer:    "https://gmfuorleragagsqbkajh.supabase.co/auth/v1",
			Audience:  []string{"authenticated"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		Role: "authenticated",
	})
	signed, err := token.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func perform(r http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestProgressAndContactRejectAnonymous(t *testing.T) {
	r := testRouter(&fakeSource{})
	progress := perform(r, http.MethodPost, "/api/platform/me/progress", `{"lessonId":"8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10"}`, "")
	if progress.Code != http.StatusUnauthorized {
		t.Fatalf("progress status = %d, body %s", progress.Code, progress.Body.String())
	}
	contact := perform(r, http.MethodPost, "/api/platform/jobs/8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10/contact", `{}`, "")
	if contact.Code != http.StatusUnauthorized {
		t.Fatalf("contact status = %d", contact.Code)
	}
}

func TestFavoriteIsScopedToCaller(t *testing.T) {
	source := &fakeSource{}
	r := testRouter(source)
	userID := "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10"
	other := "11111111-1111-4111-8111-111111111111"
	response := perform(r, http.MethodPut, "/api/platform/me/favorites/"+other, `{"userId":"`+other+`"}`, signUser(t, userID))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body %s", response.Code, response.Body.String())
	}
	if len(source.writes) != 1 {
		t.Fatalf("writes = %d", len(source.writes))
	}
	payload := source.writes[0].Payload.(map[string]any)
	if payload["user_id"] != userID || payload["question_id"] != other {
		t.Fatalf("payload scoped incorrectly: %#v", payload)
	}
}

func TestProgressCannotQueryAnotherUser(t *testing.T) {
	source := &fakeSource{tables: map[string][]map[string]any{}}
	r := testRouter(source)
	userID := "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10"
	response := perform(r, http.MethodGet, "/api/platform/me/progress?courseId=22222222-2222-4222-8222-222222222222", "", signUser(t, userID))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if len(source.queries) != 1 || source.queries[0].Filters[0].Value != userID {
		t.Fatalf("query was not forced to caller: %#v", source.queries)
	}
}

func TestAdminUserListRejectsAnonymousAndNormalUsers(t *testing.T) {
	userID := "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10"
	source := &fakeSource{tables: map[string][]map[string]any{
		"user_profiles": {{"id": userID, "is_admin": false}},
	}}
	router := testRouter(source)
	anonymous := perform(router, http.MethodGet, "/api/platform/admin/users/study", "", "")
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d %s", anonymous.Code, anonymous.Body.String())
	}
	normal := perform(router, http.MethodGet, "/api/platform/admin/users/study", "", signUser(t, userID))
	if normal.Code != http.StatusForbidden {
		t.Fatalf("normal = %d %s", normal.Code, normal.Body.String())
	}
}

func TestAdminStudyAuthorizesBeforeReadingQuery(t *testing.T) {
	userID := "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10"
	source := &fakeSource{tables: map[string][]map[string]any{
		"user_profiles": {{"id": userID, "is_admin": false}},
	}}
	response := perform(testRouter(source), http.MethodGet, "/api/platform/admin/users/study?pageSize=999&sort=phone", "", signUser(t, userID))
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
}

func TestPublicJobQueryOmitsContacts(t *testing.T) {
	source := &fakeSource{tables: map[string][]map[string]any{
		"job_listings": {{"id": "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10", "title": "Engineer", "email": "hidden@example.com"}},
	}}
	r := testRouter(source)
	response := perform(r, http.MethodGet, "/api/platform/jobs?q=engineer", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body %s", response.Code, response.Body.String())
	}
	if strings.Contains(source.queries[0].Select, "email") || strings.Contains(response.Body.String(), "hidden@example.com") {
		t.Fatalf("contact leaked: select %s body %s", source.queries[0].Select, response.Body.String())
	}
}

func TestCourseDetailHidesVideoWithoutAccess(t *testing.T) {
	courseID := "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10"
	chapterID := "33333333-3333-4333-8333-333333333333"
	source := &fakeSource{tables: map[string][]map[string]any{
		"courses":  {{"id": courseID, "title": "Course", "is_free": false, "price": 10, "status": "published"}},
		"chapters": {{"id": chapterID, "course_id": courseID, "title": "Chapter", "sort_order": 1}},
		"lessons":  {{"id": "44444444-4444-4444-8444-444444444444", "chapter_id": chapterID, "title": "Lesson", "video_id": "secret-video", "video_url": "https://cdn.example/secret", "is_free": false}},
	}}
	r := testRouter(source)
	response := perform(r, http.MethodGet, "/api/platform/courses/"+courseID, "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "secret-video") || strings.Contains(response.Body.String(), "cdn.example") {
		t.Fatalf("video leaked: %s", response.Body.String())
	}
}

func TestProfileUpdateDropsPrivilegedFields(t *testing.T) {
	source := &fakeSource{}
	r := testRouter(source)
	userID := "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10"
	response := perform(r, http.MethodPatch, "/api/platform/me/profile", `{"display_name":"Ada","is_admin":true,"vip_level":5}`, signUser(t, userID))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body %s", response.Code, response.Body.String())
	}
	payload := source.writes[0].Payload.(map[string]any)
	if _, ok := payload["is_admin"]; ok {
		t.Fatal("is_admin was forwarded")
	}
	if _, ok := payload["vip_level"]; ok {
		t.Fatal("vip_level was forwarded")
	}
	if payload["display_name"] != "Ada" {
		t.Fatalf("display name = %#v", payload["display_name"])
	}
}

func TestNonAdminCannotUpdateCourse(t *testing.T) {
	source := &fakeSource{tables: map[string][]map[string]any{
		"user_profiles": {{"id": "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10", "is_admin": false}},
	}}
	r := testRouter(source)
	response := perform(r, http.MethodPatch, "/api/platform/courses/8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10", `{"title":"Hijack"}`, signUser(t, "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10"))
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d body %s", response.Code, response.Body.String())
	}
	if len(source.writes) != 0 {
		t.Fatal("non-admin write was issued")
	}
}

func TestJobWithoutContactsConsumesNoUnlock(t *testing.T) {
	id := "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10"
	source := &fakeSource{tables: map[string][]map[string]any{
		"job_listings": {{"id": id, "email": " "}},
	}}
	s := NewService(source, nil, "postgres")
	for _, statusOnly := range []bool{true, false} {
		result, err := s.JobContact(context.Background(), Caller{UserID: id, Token: "test-session"}, id, statusOnly)
		if err != nil || result["available"] != false || len(source.writes) != 0 {
			t.Fatalf("empty contacts: result=%v err=%v writes=%v", result, err, source.writes)
		}
	}
}
