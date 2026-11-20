package platform

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (h *Handler) registerAuth(r gin.IRoutes) {
	r.POST("/api/platform/auth/login", h.login)
	r.POST("/api/platform/auth/register", h.register)
	r.POST("/api/platform/auth/recovery", h.recoveryUnavailable)
	r.GET("/api/platform/auth/session", h.required(h.session))
	r.POST("/api/platform/auth/oauth/:provider", h.providerUnavailable)
	r.POST("/api/platform/auth/web3", h.providerUnavailable)
}

func (h *Handler) login(c *gin.Context)    { h.issuePasswordSession(c, false) }
func (h *Handler) register(c *gin.Context) { h.issuePasswordSession(c, true) }

func (h *Handler) issuePasswordSession(c *gin.Context, create bool) {
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	var userID, email string
	if create {
		userID, email, err = h.service.RegisterPassword(c.Request.Context(), stringField(body, "email"), stringField(body, "password"))
	} else {
		userID, email, err = h.service.LoginPassword(c.Request.Context(), stringField(body, "email"), stringField(body, "password"))
	}
	if err != nil {
		writeErr(c, err)
		return
	}
	if h.validator == nil {
		writeErr(c, NewError(http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "未配置 SUPABASE_JWT_SECRET"))
		return
	}
	token, expiresAt, err := h.validator.IssueSessionToken(userID, email)
	if err != nil {
		writeErr(c, NewError(http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "未配置 SUPABASE_JWT_SECRET"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token, "expiresAt": expiresAt, "user": gin.H{"id": userID, "email": email, "emailVerified": false}})
}

func (h *Handler) session(c *gin.Context, caller Caller) {
	c.JSON(http.StatusOK, gin.H{"user": gin.H{"id": caller.UserID, "email": caller.Email}})
}

func (h *Handler) recoveryUnavailable(c *gin.Context) {
	writeErr(c, NewError(http.StatusServiceUnavailable, "PROVIDER_UNAVAILABLE", "密码重置需要邮件服务凭据，邮箱不会被自动标成已验证"))
}

func (h *Handler) providerUnavailable(c *gin.Context) {
	writeErr(c, NewError(http.StatusServiceUnavailable, "PROVIDER_UNAVAILABLE", "OAuth 和 Web3 需要外部身份凭据，当前没有这些配置"))
}
