package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/service"
	"github.com/ticket/email-mgmt-system/internal/store"
)

type APIPrincipal struct {
	ApplicationID   uint64
	ApplicationName string
	CredentialID    uint64
	Permissions     map[string]struct{}
}

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

		startedAt := time.Now()
		client, err := store.AuthenticateAPICredential(service.HashAPIToken(tokenStr), startedAt)
		if err == nil {
			permissions := make(map[string]struct{}, len(client.Permissions))
			for _, permission := range client.Permissions {
				permissions[permission] = struct{}{}
			}
			principal := &APIPrincipal{
				ApplicationID: client.Application.ID, ApplicationName: client.Application.Name,
				CredentialID: client.Credential.ID, Permissions: permissions,
			}
			c.Set("api_principal", principal)
			store.UpdateAPICredentialUsage(client.Credential.ID, startedAt, c.ClientIP())
			c.Next()
			path := c.FullPath()
			if path == "" {
				path = c.Request.URL.Path
			}
			permission, _ := c.Get("api_permission_code")
			store.CreateAPIAccessLog(&model.APIAccessLog{
				ApplicationID: client.Application.ID, CredentialID: client.Credential.ID,
				PermissionCode: fmtString(permission), Method: c.Request.Method, Path: path,
				StatusCode: c.Writer.Status(), ClientIP: c.ClientIP(), DurationMS: time.Since(startedAt).Milliseconds(),
			})
			return
		}

		// Compatibility fallback for legacy plaintext api_tokens.
		token, legacyErr := store.FindToken(tokenStr)
		if legacyErr != nil {
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

func fmtString(value interface{}) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func hasScope(scopes, required string) bool {
	if required == "" {
		return false
	}

	for _, item := range strings.Split(scopes, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if item == "*" || item == required {
			return true
		}
	}
	return false
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
		if hasScope(token.Scopes, scope) {
			c.Next()
			return
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code": 1005, "message": "insufficient scope, required: " + scope,
		})
	}
}

// RequirePermission checks normalized application permissions and retains a
// strict legacy scope mapping during the migration window.
func RequirePermission(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("api_permission_code", permission)
		if value, ok := c.Get("api_principal"); ok {
			principal, valid := value.(*APIPrincipal)
			if valid {
				if _, all := principal.Permissions["*"]; all {
					c.Next()
					return
				}
				if _, allowed := principal.Permissions[permission]; allowed {
					c.Next()
					return
				}
			}
		}

		if value, ok := c.Get("api_token"); ok {
			if token, valid := value.(*model.ApiToken); valid && legacyAllows(token.Scopes, permission) {
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code": 1005, "message": "insufficient permission, required: " + permission,
		})
	}
}

func legacyAllows(scopes, permission string) bool {
	for _, scope := range strings.Split(scopes, ",") {
		switch strings.TrimSpace(scope) {
		case "*":
			return true
		case "mailbox:create":
			if permission == "mailbox:create" || permission == "mailbox:disable" {
				return true
			}
		case "mailbox:read":
			if permission == "mailbox:read" {
				return true
			}
		case "email:read":
			if permission == "email:list" || permission == "email:body" || permission == "email:attachment" {
				return true
			}
		}
	}
	return false
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
