package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/store"
)

// AuthRequired 验证 Bearer Token
func AuthRequired(store *store.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 管理后台 API（/api/v1/admin/*）由 Web 页面访问，不走 Token 鉴权
		// （htmx 表单不便带 Bearer header）。对外 API（/api/v1/mailboxes、/api/v1/emails 等）仍需 Token。
		// 生产环境靠 Nginx 反代加 IP 白名单 / Basic Auth 保护 /admin。
		if strings.HasPrefix(c.Request.URL.Path, "/api/v1/admin/") {
			c.Next()
			return
		}

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

		token := tokenVal.(*struct {
			ID      uint64
			Name    string
			Token   string
			Scopes  string
			Enabled bool
		})

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
