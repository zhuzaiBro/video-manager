package handler

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/zood/video-manager/internal/auth"
	"github.com/zood/video-manager/internal/config"
	"github.com/zood/video-manager/internal/middleware"
	"github.com/zood/video-manager/internal/service"
)

type AdminHandler struct {
	cfg    *config.Config
	videos *service.VideoService
}

func NewAdminHandler(cfg *config.Config, videos *service.VideoService) *AdminHandler {
	return &AdminHandler{cfg: cfg, videos: videos}
}

func (h *AdminHandler) Upload(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": "file is required"})
		return
	}

	title := c.PostForm("title")
	if title == "" {
		title = file.Filename
	}
	var description *string
	if desc := c.PostForm("description"); desc != "" {
		description = &desc
	}

	filename := fmt.Sprintf("%d_%s", time.Now().UnixNano(), filepath.Base(file.Filename))
	dest := filepath.Join(h.cfg.UploadDir, filename)
	if err := c.SaveUploadedFile(file, dest); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "UPLOAD_FAILED", "message": err.Error()})
		return
	}

	videoID, err := h.videos.Upload(c.Request.Context(), title, description, dest)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "UPLOAD_FAILED", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"videoId": videoID})
}

type VideoHandler struct {
	videos   *service.VideoService
	playback *service.PlaybackService
}

func NewVideoHandler(videos *service.VideoService, playback *service.PlaybackService) *VideoHandler {
	return &VideoHandler{videos: videos, playback: playback}
}

func (h *VideoHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))

	videos, total, err := h.videos.List(page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "INTERNAL_ERROR", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"items":    videos,
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	})
}

func (h *VideoHandler) Detail(c *gin.Context) {
	id, err := parseVideoID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_ID", "message": "invalid video id"})
		return
	}

	video, err := h.videos.GetByID(id)
	if err != nil {
		writeVideoError(c, err)
		return
	}

	c.JSON(http.StatusOK, video)
}

func (h *VideoHandler) Play(c *gin.Context) {
	id, err := parseVideoID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_ID", "message": "invalid video id"})
		return
	}

	userID := middleware.GetUserID(c)
	token := auth.ExtractToken(c)
	result, err := h.playback.Play(c.Request.Context(), userID, id, token)
	if err != nil {
		writePlaybackError(c, err)
		return
	}

	c.JSON(http.StatusOK, result)
}

func (h *VideoHandler) Media(c *gin.Context) {
	id, err := parseVideoID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_ID", "message": "invalid video id"})
		return
	}

	filename := strings.TrimPrefix(c.Param("filepath"), "/")
	userID := middleware.GetUserID(c)
	token := auth.ExtractToken(c)

	obj, err := h.playback.ServeMedia(c.Request.Context(), userID, id, filename, token, c.ClientIP())
	if err != nil {
		writePlaybackError(c, err)
		return
	}

	if obj.RedirectURL != "" {
		c.Header("Cache-Control", "no-store")
		c.Redirect(http.StatusTemporaryRedirect, obj.RedirectURL)
		return
	}

	defer obj.Body.Close()
	c.Header("Cache-Control", "no-store")
	c.DataFromReader(http.StatusOK, -1, obj.ContentType, obj.Body, nil)
}

func (h *VideoHandler) WatchStats(c *gin.Context) {
	id, err := parseVideoID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_ID", "message": "invalid video id"})
		return
	}

	result, err := h.playback.WatchStats(c.Request.Context(), id)
	if err != nil {
		writeVideoError(c, err)
		return
	}

	c.JSON(http.StatusOK, result)
}

type SegmentReportRequest struct {
	SegmentName string `json:"segmentName" binding:"required"`
}

func (h *VideoHandler) ReportSegment(c *gin.Context) {
	id, err := parseVideoID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_ID", "message": "invalid video id"})
		return
	}

	var req SegmentReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": err.Error()})
		return
	}

	userID := middleware.GetUserID(c)
	ip := c.ClientIP()
	if err := h.playback.ReportSegment(c.Request.Context(), userID, id, req.SegmentName, ip); err != nil {
		writePlaybackError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type AuthHandler struct {
	auth *service.AuthService
}

func NewAuthHandler(authSvc *service.AuthService) *AuthHandler {
	return &AuthHandler{auth: authSvc}
}

func (h *AuthHandler) ExchangeTencentToken(c *gin.Context) {
	token := auth.ExtractBearerToken(c.GetHeader("Authorization"))
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": "Authorization Bearer token required"})
		return
	}

	result, err := h.auth.ExchangeTencentToken(token)
	if err != nil {
		writeAuthError(c, err)
		return
	}

	c.JSON(http.StatusOK, result)
}

func parseVideoID(raw string) (uuid.UUID, error) {
	return uuid.Parse(raw)
}

func writeVideoError(c *gin.Context, err error) {
	if errors.Is(err, service.ErrVideoNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"code": "NOT_FOUND", "message": "video not found"})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"code": "INTERNAL_ERROR", "message": err.Error()})
}

func writePlaybackError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrVideoNotFound):
		c.JSON(http.StatusNotFound, gin.H{"code": "NOT_FOUND", "message": "video not found"})
	case errors.Is(err, service.ErrVideoNotReady):
		c.JSON(http.StatusConflict, gin.H{"code": "VIDEO_NOT_READY", "message": "video is not ready for playback"})
	case errors.Is(err, service.ErrWatchLimitExceeded):
		c.JSON(http.StatusForbidden, gin.H{"code": "WATCH_LIMIT_EXCEEDED", "message": "Daily watch limit exceeded"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"code": "INTERNAL_ERROR", "message": err.Error()})
	}
}

func writeAuthError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrAuthMissingSecret):
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    "CONFIG_ERROR",
			"message": "SUPABASE_JWT_SECRET not configured, get it from Supabase Dashboard → Project Settings → API → JWT Secret",
		})
	case errors.Is(err, service.ErrAuthInvalidToken):
		c.JSON(http.StatusUnauthorized, gin.H{"code": "INVALID_TOKEN", "message": "Invalid supabase token, check SUPABASE_JWT_SECRET and token expiry"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"code": "INTERNAL_ERROR", "message": err.Error()})
	}
}

func Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
