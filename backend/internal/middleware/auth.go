package middleware

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zood/video-manager/internal/auth"
)

const ContextUserIDKey = "userId"

type AuthMiddleware struct {
	validator *auth.Validator
}

func NewAuthMiddleware(validator *auth.Validator) *AuthMiddleware {
	return &AuthMiddleware{validator: validator}
}

func (m *AuthMiddleware) RequireAuth() gin.HandlerFunc {
	return m.requireAuth(false)
}

// RequireAuthFromQuery play 接口：token 通过 ?token= 传递，也兼容 Authorization 头。
func (m *AuthMiddleware) RequireAuthFromQuery() gin.HandlerFunc {
	return m.requireAuth(true)
}

func (m *AuthMiddleware) requireAuth(queryPreferred bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var token string
		if queryPreferred {
			token = auth.ExtractToken(c)
		} else {
			token = auth.ExtractBearerToken(c.GetHeader("Authorization"))
		}

		if token == "" {
			msg := "Authentication required, use Authorization: Bearer {token}"
			if queryPreferred {
				msg = "Authentication required, use ?token={tencentToken} or Authorization header"
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    "UNAUTHORIZED",
				"message": msg,
			})
			return
		}

		result, err := m.validator.ResolveUser(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    "UNAUTHORIZED",
				"message": authErrorMessage(err),
			})
			return
		}

		c.Set(ContextUserIDKey, result.UserID)
		c.Next()
	}
}

func authErrorMessage(err error) string {
	switch {
	case errors.Is(err, auth.ErrUnresolvedVar):
		return "Token variable not resolved (Apifox: set bearerToken to tencentToken or supabase access_token, without Bearer prefix)"
	case errors.Is(err, auth.ErrTokenExpired):
		return "Token expired, login again or call /api/auth/tencent-token"
	case errors.Is(err, auth.ErrInvalidToken):
		return "Invalid token, use tencentToken from /api/auth/tencent-token or a valid supabase access_token"
	default:
		return "Invalid or expired token"
	}
}

func GetUserID(c *gin.Context) string {
	v, _ := c.Get(ContextUserIDKey)
	id, _ := v.(string)
	return id
}
