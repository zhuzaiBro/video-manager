package auth

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// ExtractBearerToken 从 Authorization 头提取 token，兼容 Apifox 等工具重复加 Bearer 的情况。
func ExtractBearerToken(authHeader string) string {
	token := strings.TrimSpace(authHeader)
	for strings.HasPrefix(strings.ToLower(token), "bearer ") {
		token = strings.TrimSpace(token[7:])
	}
	return token
}

// ExtractToken 优先从 query token 读取，否则从 Authorization 头读取。
func ExtractToken(c *gin.Context) string {
	if token := strings.TrimSpace(c.Query("token")); token != "" {
		return token
	}
	return ExtractBearerToken(c.GetHeader("Authorization"))
}

