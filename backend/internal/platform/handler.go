package platform

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/zood/video-manager/internal/auth"
)

type BlobStore interface {
	PutObject(ctx context.Context, key string, body []byte, contentType string) error
	PublicObjectURL(key string) string
}

type Handler struct {
	service   *Service
	validator *auth.Validator
	blob      BlobStore
}

func NewHandler(service *Service, validator *auth.Validator) *Handler {
	return &Handler{service: service, validator: validator}
}

func (h *Handler) SetBlob(blob BlobStore) { h.blob = blob }

func (h *Handler) Register(r gin.IRoutes) {
	r.POST("/api/platform/uploads", h.required(h.upload))
	r.GET("/api/platform/health", h.health)
	r.GET("/api/platform/posts", h.optional(h.listPosts))
	r.GET("/api/platform/posts/by-slug", h.optional(h.getPost))
	r.GET("/api/platform/categories", h.optional(h.listCategories))
	r.GET("/api/platform/categories/by-slug", h.optional(h.getCategory))
	r.GET("/api/platform/posts/:id/categories", h.optional(h.postCategories))
	r.GET("/api/platform/posts/:id/comments", h.optional(h.listComments))
	r.POST("/api/platform/posts", h.required(h.createPost))
	r.POST("/api/platform/comments", h.required(h.createComment))
	r.GET("/api/platform/me/profile", h.required(h.getProfile))
	r.PUT("/api/platform/me/profile", h.required(h.createProfile))
	r.PATCH("/api/platform/me/profile", h.required(h.updateProfile))
	r.GET("/api/platform/courses", h.optional(h.listCourses))
	r.GET("/api/platform/courses/:id", h.optional(h.courseDetail))
	r.PATCH("/api/platform/courses/:id", h.required(h.updateCourse))
	r.GET("/api/platform/jobs", h.optional(h.listJobs))
	r.GET("/api/platform/jobs/:id", h.optional(h.getJob))
	r.GET("/api/platform/jobs/:id/contact", h.required(h.jobContact))
	r.POST("/api/platform/jobs/:id/contact", h.required(h.jobContact))
	r.GET("/api/platform/interview/questions/search", h.optional(h.searchQuestions))
	r.GET("/api/platform/me/favorites/:questionId", h.required(h.hasFavorite))
	r.PUT("/api/platform/me/favorites/:questionId", h.required(h.addFavorite))
	r.DELETE("/api/platform/me/favorites/:questionId", h.required(h.removeFavorite))
	r.GET("/api/platform/messages", h.optional(h.listMessages))
	r.POST("/api/platform/messages", h.optional(h.createMessage))
	r.POST("/api/platform/messages/realtime", h.optional(h.supportRealtime))
	r.GET("/api/platform/admin/support/telegram", h.required(h.getTelegramConfig))
	r.PUT("/api/platform/admin/support/telegram", h.required(h.putTelegramConfig))
	r.GET("/api/platform/me/progress", h.optional(h.listProgress))
	r.POST("/api/platform/me/progress", h.required(h.saveProgress))
	r.POST("/api/platform/course-purchase-requests", h.optional(h.purchase))
	r.POST("/api/platform/membership-consultations", h.optional(h.consultation))
	r.GET("/api/platform/interview", h.optional(h.interviewHome))
	r.GET("/api/platform/interview/collections/:id", h.optional(h.getCollection))
	r.GET("/api/platform/interview/questions/:id", h.optional(h.getQuestion))
	r.GET("/api/platform/sitemap", h.optional(h.sitemap))
	r.PUT("/api/platform/posts/:id/categories", h.required(h.setPostCategories))
	r.GET("/api/platform/me/center", h.required(h.profileCenter))
	r.GET("/api/platform/admin/interview/questions", h.required(h.adminQuestions))
	r.POST("/api/platform/admin/interview/questions", h.required(h.createQuestion))
	r.PATCH("/api/platform/admin/interview/questions/:id", h.required(h.updateQuestion))
	r.DELETE("/api/platform/admin/interview/questions/:id", h.required(h.deleteQuestion))
	r.POST("/api/platform/interview/submissions", h.required(h.createSubmission))
	r.POST("/api/platform/me/resume-records", h.required(h.saveResume))
	r.POST("/api/platform/faucet/reservations", h.optional(h.reserveFaucet))
	r.POST("/api/platform/faucet/reservations/:id/confirm", h.optional(h.confirmFaucet))
	r.DELETE("/api/platform/faucet/reservations/:id", h.optional(h.releaseFaucet))
	r.POST("/api/platform/me/referrals/claim", h.required(h.claimReferral))
	r.GET("/api/platform/admin/users/study", h.required(h.adminStudy))
	r.POST("/api/platform/admin/users/sync-profiles", h.required(h.syncProfiles))
	r.PATCH("/api/platform/admin/users/:id/membership", h.required(h.grantMembership))
	r.POST("/api/platform/telegram/webhook", h.telegramWebhook)
	r.POST("/api/platform/admin/courses", h.required(h.createCourse))
	r.POST("/api/platform/admin/chapters", h.required(h.createChapter))
	r.PATCH("/api/platform/admin/chapters/:id", h.required(h.updateChapter))
	r.DELETE("/api/platform/admin/chapters/:id", h.required(h.deleteChapter))
	r.POST("/api/platform/admin/lessons", h.required(h.createLesson))
	r.PATCH("/api/platform/admin/lessons/:id", h.required(h.updateLesson))
	r.DELETE("/api/platform/admin/lessons/:id", h.required(h.deleteLesson))
	r.POST("/api/platform/admin/reorder", h.required(h.reorder))
	r.GET("/api/platform/admin/videos", h.required(h.listVideos))
	r.GET("/api/platform/admin/purchase-requests", h.required(h.listPurchases))
	r.PATCH("/api/platform/admin/purchase-requests/:id", h.required(h.updatePurchase))
	r.GET("/api/platform/admin/consultations", h.required(h.listConsultations))
	r.PATCH("/api/platform/admin/consultations/:id", h.required(h.updateConsultation))
	r.GET("/api/platform/admin/interview/submissions", h.required(h.listSubmissions))
	r.GET("/api/platform/admin/courses/:id/access", h.required(h.courseAccess))
	r.POST("/api/platform/admin/courses/:id/access", h.required(h.grantAccess))
	r.DELETE("/api/platform/admin/courses/:id/access/:userId", h.required(h.revokeAccess))
}

func (h *Handler) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "dataSource": h.service.DataSource()})
}

func (h *Handler) listPosts(c *gin.Context, caller Caller) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	result, err := h.service.ListPosts(c.Request.Context(), caller, limit)
	writeResult(c, gin.H{"posts": result.Rows, "count": result.Count}, err)
}

func (h *Handler) getPost(c *gin.Context, caller Caller) {
	row, err := h.service.GetPost(c.Request.Context(), caller, c.Query("slug"))
	writeResult(c, gin.H{"post": row}, err)
}

func (h *Handler) listCategories(c *gin.Context, caller Caller) {
	result, err := h.service.ListCategories(c.Request.Context(), caller)
	writeResult(c, gin.H{"categories": result.Rows}, err)
}

func (h *Handler) getCategory(c *gin.Context, caller Caller) {
	row, err := h.service.GetCategory(c.Request.Context(), caller, c.Query("slug"))
	writeResult(c, row, err)
}

func (h *Handler) postCategories(c *gin.Context, caller Caller) {
	result, err := h.service.PostCategories(c.Request.Context(), caller, c.Param("id"))
	writeResult(c, gin.H{"categories": result.Rows}, err)
}

func (h *Handler) listComments(c *gin.Context, caller Caller) {
	result, err := h.service.ListComments(c.Request.Context(), caller, c.Param("id"))
	writeResult(c, gin.H{"comments": result.Rows}, err)
}

func (h *Handler) createPost(c *gin.Context, caller Caller) {
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	row, err := h.service.CreatePost(c.Request.Context(), caller, body)
	writeResult(c, gin.H{"post": row}, err)
}

func (h *Handler) createComment(c *gin.Context, caller Caller) {
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	row, err := h.service.CreateComment(c.Request.Context(), caller, stringField(body, "postId"), stringField(body, "content"), stringField(body, "parentId"))
	writeResult(c, gin.H{"comment": row}, err)
}

func (h *Handler) getProfile(c *gin.Context, caller Caller) {
	row, err := h.service.GetProfile(c.Request.Context(), caller)
	writeResult(c, gin.H{"profile": row}, err)
}

func (h *Handler) createProfile(c *gin.Context, caller Caller) {
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	row, err := h.service.SaveProfile(c.Request.Context(), caller, body, true)
	writeResult(c, gin.H{"profile": row}, err)
}

func (h *Handler) updateProfile(c *gin.Context, caller Caller) {
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	row, err := h.service.SaveProfile(c.Request.Context(), caller, body, false)
	writeResult(c, gin.H{"profile": row}, err)
}

func (h *Handler) listCourses(c *gin.Context, caller Caller) {
	result, err := h.service.ListCourses(c.Request.Context(), caller, c.Query("status"), c.Query("scope") == "admin")
	writeResult(c, gin.H{"courses": result.Rows}, err)
}

func (h *Handler) courseDetail(c *gin.Context, caller Caller) {
	row, err := h.service.CourseDetail(c.Request.Context(), caller, c.Param("id"))
	writeResult(c, row, err)
}

func (h *Handler) updateCourse(c *gin.Context, caller Caller) {
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	row, err := h.service.UpdateCourse(c.Request.Context(), caller, c.Param("id"), body)
	writeResult(c, gin.H{"course": row}, err)
}

func (h *Handler) listJobs(c *gin.Context, caller Caller) {
	page, _ := strconv.Atoi(c.Query("page"))
	result, err := h.service.ListJobs(c.Request.Context(), caller, c.Query("q"), c.Query("remote") == "1", page)
	writeResult(c, gin.H{"jobs": result.Rows, "count": result.Count, "pageSize": jobPageSize}, err)
}

func (h *Handler) getJob(c *gin.Context, caller Caller) {
	row, err := h.service.GetJob(c.Request.Context(), caller, c.Param("id"))
	writeResult(c, gin.H{"job": row}, err)
}

func (h *Handler) jobContact(c *gin.Context, caller Caller) {
	row, err := h.service.JobContact(c.Request.Context(), caller, c.Param("id"), c.Query("mode") == "status" || c.Request.Method == http.MethodGet)
	writeResult(c, row, err)
}

func (h *Handler) searchQuestions(c *gin.Context, caller Caller) {
	result, err := h.service.SearchQuestions(c.Request.Context(), caller, c.Query("q"))
	writeResult(c, gin.H{"questions": result.Rows}, err)
}

func (h *Handler) hasFavorite(c *gin.Context, caller Caller) {
	ok, err := h.service.HasFavorite(c.Request.Context(), caller, c.Param("questionId"))
	writeResult(c, gin.H{"favorite": ok}, err)
}

func (h *Handler) addFavorite(c *gin.Context, caller Caller) {
	err := h.service.Favorite(c.Request.Context(), caller, c.Param("questionId"), true)
	writeResult(c, gin.H{"favorite": true}, err)
}

func (h *Handler) removeFavorite(c *gin.Context, caller Caller) {
	err := h.service.Favorite(c.Request.Context(), caller, c.Param("questionId"), false)
	writeResult(c, gin.H{"favorite": false}, err)
}

func (h *Handler) listMessages(c *gin.Context, caller Caller) {
	result, err := h.service.ListMessages(c.Request.Context(), caller, c.Query("sessionId"))
	writeResult(c, gin.H{"messages": result.Rows}, err)
}

func (h *Handler) createMessage(c *gin.Context, caller Caller) {
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	row, err := h.service.CreateMessage(c.Request.Context(), caller, stringField(body, "sessionId"), stringField(body, "content"), stringField(body, "pageUrl"))
	if err != nil {
		writeErr(c, err)
		return
	}
	forwarded, _ := row["forwarded"].(int)
	delete(row, "forwarded")
	writeResult(c, gin.H{"message": row, "forwarded": forwarded}, nil)
}

func (h *Handler) supportRealtime(c *gin.Context, caller Caller) {
	if c.GetHeader("X-Zood-Request") != supportRealtimeHeader {
		writeErr(c, NewError(http.StatusForbidden, "CSRF_REJECTED", "请求来源未通过校验"))
		return
	}
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	result, err := h.service.SupportRealtimeToken(c.Request.Context(), caller, stringField(body, "sessionId"), stringField(body, "scope"))
	writeResult(c, result, err)
}

func (h *Handler) getTelegramConfig(c *gin.Context, caller Caller) {
	view, err := h.service.GetTelegramConfig(c.Request.Context(), caller)
	writeResult(c, gin.H{"telegramBotToken": view}, err)
}

func (h *Handler) putTelegramConfig(c *gin.Context, caller Caller) {
	if c.GetHeader("X-Zood-Request") != supportConfigHeader {
		writeErr(c, NewError(http.StatusForbidden, "CSRF_REJECTED", "请求来源未通过校验"))
		return
	}
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	view, err := h.service.UpdateTelegramConfig(c.Request.Context(), caller, body)
	writeResult(c, gin.H{"telegramBotToken": view}, err)
}

func (h *Handler) listProgress(c *gin.Context, caller Caller) {
	row, err := h.service.ListProgress(c.Request.Context(), caller, c.Query("courseId"), c.Query("lessonId"))
	writeResult(c, row, err)
}

func (h *Handler) saveProgress(c *gin.Context, caller Caller) {
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	row, err := h.service.SaveProgress(c.Request.Context(), caller, body)
	writeResult(c, gin.H{"progress": row}, err)
}

func (h *Handler) purchase(c *gin.Context, caller Caller) {
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	row, err := h.service.CreatePurchaseRequest(c.Request.Context(), caller, body)
	writeResult(c, gin.H{"request": row}, err)
}

func (h *Handler) consultation(c *gin.Context, caller Caller) {
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	row, err := h.service.CreateConsultation(c.Request.Context(), caller, body)
	writeResult(c, gin.H{"request": row}, err)
}

func (h *Handler) optional(next func(*gin.Context, Caller)) gin.HandlerFunc {
	return func(c *gin.Context) {
		caller, err := h.callerFrom(c, false)
		if err != nil {
			writeErr(c, err)
			return
		}
		next(c, caller)
	}
}

func (h *Handler) required(next func(*gin.Context, Caller)) gin.HandlerFunc {
	return func(c *gin.Context) {
		caller, err := h.callerFrom(c, true)
		if err != nil {
			writeErr(c, err)
			return
		}
		next(c, caller)
	}
}

func (h *Handler) callerFrom(c *gin.Context, required bool) (Caller, error) {
	token := auth.ExtractBearerToken(c.GetHeader("Authorization"))
	if token == "" {
		if required {
			return Caller{}, unauthorized("未登录")
		}
		return Caller{}, nil
	}
	if h.validator == nil {
		return Caller{}, unauthorized("认证未配置")
	}
	claims, err := h.validator.ParseSupabaseToken(token)
	if err != nil || claims.Subject == "" {
		return Caller{}, unauthorized("登录状态无效")
	}
	if claims.Role != "" && claims.Role != "authenticated" {
		return Caller{}, unauthorized("登录状态无效")
	}
	return Caller{UserID: claims.Subject, Email: claims.Email, Token: token}, nil
}

func readBody(c *gin.Context) (map[string]any, error) {
	defer c.Request.Body.Close()
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		return nil, invalid("请求体无法读取")
	}
	if len(body) == 0 {
		return map[string]any{}, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, invalid("请求体必须是 JSON 对象")
	}
	return payload, nil
}

func writeResult(c *gin.Context, body any, err error) {
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, body)
}

func writeErr(c *gin.Context, err error) {
	apiErr := asError(err, http.StatusInternalServerError)
	c.JSON(apiErr.Status, gin.H{"error": apiErr.Message, "code": apiErr.Code})
}
