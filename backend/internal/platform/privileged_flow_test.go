package platform

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zood/video-manager/internal/auth"
	"github.com/zood/video-manager/internal/config"
)

type recordingPrivileged struct {
	called []string
	admin  bool
}

func (p *recordingPrivileged) mark(name string) { p.called = append(p.called, name) }

func (p *recordingPrivileged) GetAuthUser(context.Context, string) (AuthAccount, bool, error) {
	p.mark("get-auth-user")
	return AuthAccount{}, false, nil
}
func (p *recordingPrivileged) ListAuthUsers(context.Context) ([]AuthAccount, error) {
	p.mark("list-auth-users")
	return nil, nil
}
func (p *recordingPrivileged) UpsertProfiles(context.Context, []map[string]any, bool) error {
	p.mark("upsert-profiles")
	return nil
}
func (p *recordingPrivileged) ReferralExists(context.Context, string) (bool, error) {
	p.mark("referral-exists")
	return false, nil
}
func (p *recordingPrivileged) InsertReferral(context.Context, map[string]any) error {
	p.mark("insert-referral")
	return nil
}
func (p *recordingPrivileged) ReadProfile(context.Context, string) (map[string]any, bool, error) {
	p.mark("read-profile")
	return map[string]any{"id": "referrer", "vip_level": float64(0)}, true, nil
}
func (p *recordingPrivileged) UpdateVip(context.Context, string, int, any) error {
	p.mark("update-vip")
	return nil
}
func (p *recordingPrivileged) MarkConsultationPaid(context.Context, int, string, string) error {
	p.mark("mark-paid")
	return nil
}
func (p *recordingPrivileged) ListProfiles(context.Context) ([]map[string]any, error) {
	p.mark("list-profiles")
	return nil, nil
}
func (p *recordingPrivileged) ListWatchLogsSince(context.Context, time.Time) ([]map[string]any, error) {
	p.mark("list-logs")
	return nil, nil
}
func (p *recordingPrivileged) ListActiveTelegramAdmins(context.Context) ([]int64, error) {
	p.mark("list-telegram")
	return nil, nil
}
func (p *recordingPrivileged) UpsertTelegramAdmin(context.Context, int64, int64, string) error {
	p.mark("upsert-telegram")
	return nil
}
func (p *recordingPrivileged) TelegramAdminActive(context.Context, int64, int64) (bool, error) {
	return false, nil
}
func (p *recordingPrivileged) TelegramUpdateSeen(context.Context, int64) (bool, error) {
	return false, nil
}
func (p *recordingPrivileged) TelegramReplySession(context.Context, int64, int64) (string, bool, error) {
	return "", false, nil
}
func (p *recordingPrivileged) InsertAdminMessage(context.Context, string, string) (string, error) {
	p.mark("insert-admin-message")
	return "", nil
}
func (p *recordingPrivileged) InsertTelegramLink(context.Context, map[string]any) error {
	p.mark("insert-link")
	return nil
}

type selfSource struct {
	fakeSource
	self AuthAccount
}

func (s *selfSource) FetchSelf(context.Context, Caller) (AuthAccount, error) { return s.self, nil }

func privilegedRouter(source Source, privileged Privileged) *gin.Engine {
	gin.SetMode(gin.TestMode)
	validator := auth.NewValidator(&config.Config{SupabaseJWTSecret: "test-secret"})
	service := NewService(source, nil, "supabase")
	service.SetPrivileged(privileged)
	service.SetTelegramSecrets("hook-secret", "setup-secret")
	r := gin.New()
	NewHandler(service, validator).Register(r)
	return r
}

func TestMembershipRejectsAnonymousAndNonAdminBeforePrivilegedCall(t *testing.T) {
	privileged := &recordingPrivileged{}
	source := &fakeSource{tables: map[string][]map[string]any{
		"user_profiles": {{"id": "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10", "is_admin": false}},
	}}
	router := privilegedRouter(source, privileged)
	target := "/api/platform/admin/users/22222222-2222-4222-8222-222222222222/membership"
	anonymous := perform(router, http.MethodPatch, target, `{"vipLevel":1}`, "")
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status %d", anonymous.Code)
	}
	if len(privileged.called) != 0 {
		t.Fatalf("privileged client used without a caller: %#v", privileged.called)
	}
	user := perform(router, http.MethodPatch, target, `{"vipLevel":1}`, signUser(t, "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10"))
	if user.Code != http.StatusForbidden {
		t.Fatalf("non-admin status %d body %s", user.Code, user.Body.String())
	}
	if len(privileged.called) != 0 {
		t.Fatalf("privileged client used for a non-admin: %#v", privileged.called)
	}
}

func TestReferralUsesCallerNotSuppliedUser(t *testing.T) {
	privileged := &recordingPrivileged{}
	callerID := "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10"
	source := &selfSource{self: AuthAccount{ID: callerID, CreatedAt: time.Now().Add(-time.Hour), Email: "new@example.com"}}
	router := privilegedRouter(source, privileged)
	response := perform(router, http.MethodPost, "/api/platform/me/referrals/claim", `{"referrerUserId":"33333333-3333-4333-8333-333333333333","referredUserId":"44444444-4444-4444-8444-444444444444"}`, signUser(t, callerID))
	if response.Code != http.StatusOK {
		t.Fatalf("status %d body %s", response.Code, response.Body.String())
	}
	joined := strings.Join(privileged.called, ",")
	if !strings.Contains(joined, "insert-referral") || strings.Contains(response.Body.String(), "44444444-4444-4444-8444-444444444444") {
		t.Fatalf("calls %s body %s", joined, response.Body.String())
	}
}

func TestTelegramWebhookRejectsBadSecretBeforeAdminClient(t *testing.T) {
	privileged := &recordingPrivileged{}
	router := privilegedRouter(&fakeSource{}, privileged)
	req := httptest.NewRequest(http.MethodPost, "/api/platform/telegram/webhook", strings.NewReader(`{"update_id":1}`))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "wrong")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", w.Code)
	}
	if len(privileged.called) != 0 {
		t.Fatalf("admin client called: %#v", privileged.called)
	}
}

func TestQuestionNavigationAndCourseStats(t *testing.T) {
	courseID := "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10"
	chapterID := "33333333-3333-4333-8333-333333333333"
	questionID := "55555555-5555-4555-8555-555555555555"
	source := &fakeSource{tables: map[string][]map[string]any{
		"courses":  {{"id": courseID, "title": "Course", "status": "published"}},
		"chapters": {{"id": chapterID, "course_id": courseID}},
		"lessons":  {{"id": "66666666-6666-4666-8666-666666666666", "chapter_id": chapterID, "duration": float64(90), "video_id": ""}},
		"interview_question": {
			{"id": questionID, "title": "Middle", "collection_id": "77777777-7777-4777-8777-777777777777", "sort": float64(2), "created_at": "2026-01-02T00:00:00Z"},
			{"id": "88888888-8888-4888-8888-888888888888", "title": "First", "collection_id": "77777777-7777-4777-8777-777777777777", "sort": float64(1), "created_at": "2026-01-01T00:00:00Z"},
			{"id": "99999999-9999-4999-8999-999999999999", "title": "Last", "collection_id": "77777777-7777-4777-8777-777777777777", "sort": float64(3), "created_at": "2026-01-03T00:00:00Z"},
		},
	}}
	router := privilegedRouter(source, nil)
	courses := perform(router, http.MethodGet, "/api/platform/courses?status=published", "", "")
	if courses.Code != http.StatusOK || !strings.Contains(courses.Body.String(), `"lessonCount":1`) || !strings.Contains(courses.Body.String(), `"totalDuration":90`) {
		t.Fatalf("course stats %d %s", courses.Code, courses.Body.String())
	}
	question := perform(router, http.MethodGet, "/api/platform/interview/questions/"+questionID, "", "")
	if question.Code != http.StatusOK || !strings.Contains(question.Body.String(), `"title":"First"`) || !strings.Contains(question.Body.String(), `"title":"Last"`) {
		t.Fatalf("navigation %d %s", question.Code, question.Body.String())
	}
}
