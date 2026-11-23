package platform

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/zood/video-manager/internal/support"
)

func (h *Handler) supportAdmin(action func(*gin.Context, *support.Service)) gin.HandlerFunc {
	return h.required(func(c *gin.Context, caller Caller) {
		if err := h.service.requireAdmin(c.Request.Context(), caller); err != nil {
			writeErr(c, err)
			return
		}
		c.Header("Cache-Control", "private, no-store")
		if h.service.support == nil {
			writeErr(c, NewError(503, "SUPPORT_UNAVAILABLE", "客服服务暂时不可用"))
			return
		}
		action(c, h.service.support)
	})
}
func supportResult(c *gin.Context, data any, err error) {
	if err == nil {
		c.JSON(http.StatusOK, data)
		return
	}
	switch {
	case errors.Is(err, support.ErrTelegram):
		writeErr(c, NewError(502, "TELEGRAM_UNAVAILABLE", "Telegram 未确认机器人或回调地址，请检查 Token 后重试"))
	case errors.Is(err, sql.ErrNoRows):
		writeErr(c, notFound("未找到对应的会话或机器人"))
	default:
		writeErr(c, NewError(503, "SUPPORT_UNAVAILABLE", "客服服务暂时不可用"))
	}
}
func (h *Handler) registerSupport(r gin.IRoutes) {
	r.GET("/api/platform/admin/support/threads", h.supportAdmin(func(c *gin.Context, s *support.Service) {
		page, _ := strconv.Atoi(c.Query("page"))
		if page < 1 {
			page = 1
		}
		if page > 100000 {
			page = 100000
		}
		rows, more, err := s.Threads(c.Request.Context(), page)
		supportResult(c, gin.H{"threads": rows, "more": more, "topic": s.Topic("inbox")}, err)
	}))
	r.GET("/api/platform/admin/support/threads/:id", h.supportAdmin(func(c *gin.Context, s *support.Service) {
		id, before := c.Param("id"), c.Query("before")
		if !IsUUID(id) || (before != "" && !IsUUID(before)) {
			writeErr(c, invalid("会话参数无效"))
			return
		}
		rows, more, err := s.History(c.Request.Context(), id, before)
		supportResult(c, gin.H{"messages": rows, "more": more}, err)
	}))
	r.POST("/api/platform/admin/support/threads/:id", h.supportAdmin(func(c *gin.Context, s *support.Service) {
		var body struct {
			Content string `json:"content"`
		}
		id := c.Param("id")
		if !IsUUID(id) || c.ShouldBindJSON(&body) != nil {
			writeErr(c, invalid("会话参数无效"))
			return
		}
		text := strings.TrimSpace(body.Content)
		if text == "" || len([]rune(text)) > 2000 {
			writeErr(c, invalid("回复需为 1 至 2000 个字符"))
			return
		}
		supportResult(c, gin.H{"ok": true}, s.Reply(c.Request.Context(), id, text))
	}))
	r.GET("/api/platform/admin/support/telegram", h.supportAdmin(func(c *gin.Context, s *support.Service) {
		data, err := s.Config(c.Request.Context())
		supportResult(c, data, err)
	}))
	r.PUT("/api/platform/admin/support/telegram", h.supportAdmin(func(c *gin.Context, s *support.Service) {
		var body struct {
			Token string `json:"token"`
		}
		if c.ShouldBindJSON(&body) != nil || !support.BotTokenPattern.MatchString(strings.TrimSpace(body.Token)) {
			writeErr(c, invalid("请输入 BotFather 提供的完整机器人 Token"))
			return
		}
		supportResult(c, gin.H{"ok": true}, s.Configure(c.Request.Context(), strings.TrimSpace(body.Token)))
	}))
	r.POST("/api/platform/admin/support/telegram/binding", h.supportAdmin(func(c *gin.Context, s *support.Service) {
		link, err := s.Binding(c.Request.Context())
		supportResult(c, gin.H{"url": link}, err)
	}))
}
