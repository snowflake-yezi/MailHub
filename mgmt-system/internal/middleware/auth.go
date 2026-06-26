package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/store"
)

// AuthRequired 验证 Bearer Token。
// 外部 API（/api/v1/mailboxes、/api/v1/emails 等）需要此中间件。
// 管理后台 API 由独立的 session 鉴权 group 保护，不再经过此中间件。
func AuthRequired(store *store.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": 1003, "message": "missing authorization header",
			})
			return
		}

		tokenStr := strings.TrimPrefix(header, "Bearer ")
		if tokenStr == header {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": 1003, "message": "invalid authorization format, expected Bearer token",
			})
			return
		}

		token, err := store.FindToken(tokenStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": 1004, "message": "invalid or disabled token",
			})
			return
		}

		store.UpdateTokenLastUsed(token.ID)

		c.Set("api_token", token)
		c.Set("api_token_name", token.Name)
		c.Next()
	}
}

// RequireScope 检查 Token 权限范围
func RequireScope(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenVal, exists := c.Get("api_token")
		if !exists {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code": 1005, "message": "no token context",
			})
			return
		}

		token := tokenVal.(*model.ApiToken)

		// 简单 scope 检查：支持 "*" 通配和精确匹配
		scopes := token.Scopes // 实际中用逗号分隔的字符串
		if scopes == "*" || strings.Contains(scopes, scope) {
			c.Next()
			return
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code": 1005, "message": "insufficient scope, required: " + scope,
		})
	}
}

// InternalAuthRequired validates the X-Internal-Token header against the configured
// shared secret. Used on mgmt-side /api/v1/internal/* routes that are called by
// mail-node instances. If sharedSecret is empty, the middleware fails closed.
func InternalAuthRequired(sharedSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sharedSecret == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": 1003, "message": "internal auth not configured (empty shared_secret)",
			})
			return
		}

		token := c.GetHeader("X-Internal-Token")
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": 1003, "message": "missing X-Internal-Token header",
			})
			return
		}

		if subtle.ConstantTimeCompare([]byte(token), []byte(sharedSecret)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": 1003, "message": "invalid internal token",
			})
			return
		}

		c.Next()
	}
}
