package middleware

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/service"
	"github.com/ticket/email-mgmt-system/internal/store"
)

const (
	NodeServerIDContextKey = "node_server_id"
	NodeUUIDContextKey     = "node_uuid"
	NodeAuthTypeContextKey = "node_auth_type"
)

type APIPrincipal struct {
	ApplicationID   uint64
	ApplicationName string
	CredentialID    uint64
	Permissions     map[string]struct{}
}

type APIAuthStore interface {
	AuthenticateAPICredential(tokenHash string, now time.Time) (*store.AuthenticatedAPIClient, error)
	UpdateAPICredentialUsage(id uint64, usedAt time.Time, clientIP string)
	CreateAPIAccessLog(entry *model.APIAccessLog)
}

// AuthRequired 验证 Bearer Token。
// 外部 API（/api/v1/mailboxes、/api/v1/emails 等）需要此中间件。
// 管理后台 API 由独立的 session 鉴权 group 保护，不再经过此中间件。
func AuthRequired(store APIAuthStore) gin.HandlerFunc {
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
		tokenHash := service.HashAPIToken(tokenStr)
		client, err := store.AuthenticateAPICredential(tokenHash, startedAt)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": 1004, "message": "invalid or disabled token",
			})
			return
		}

		permissions := make(map[string]struct{}, len(client.Permissions))
		for _, permission := range client.Permissions {
			permissions[permission] = struct{}{}
		}
		principal := &APIPrincipal{
			ApplicationID: client.Application.ID, ApplicationName: client.Application.Name,
			CredentialID: client.Credential.ID, Permissions: permissions,
		}
		c.Set("api_principal", principal)
		c.Set("api_actor", fmt.Sprintf("external-app:%d:%s", client.Application.ID, strings.TrimSpace(client.Application.Name)))
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
	}
}

func fmtString(value interface{}) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

// RequirePermission checks normalized application permissions.
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

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code": 1005, "message": "insufficient permission, required: " + permission,
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

type NodeCredentialAuthenticator interface {
	AuthenticateCredential(rawCredential, nodeUUID string, usedAt time.Time) (*service.NodePrincipal, error)
}

// InternalNodeAuthRequired preserves the legacy shared-secret path while
// allowing enrolled nodes to authenticate with an independently revocable
// credential. Credential-authenticated requests are bound to their server ID
// anywhere it appears in the URL, query, or top-level JSON body.
func InternalNodeAuthRequired(sharedSecret string, authenticator NodeCredentialAuthenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		legacyToken := c.GetHeader("X-Internal-Token")
		if sharedSecret != "" && legacyToken != "" && subtle.ConstantTimeCompare([]byte(legacyToken), []byte(sharedSecret)) == 1 {
			c.Set(NodeAuthTypeContextKey, "shared_secret")
			c.Next()
			return
		}

		header := strings.TrimSpace(c.GetHeader("Authorization"))
		const scheme = "Node "
		if authenticator == nil || !strings.HasPrefix(header, scheme) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 1003, "message": "valid shared secret or node credential required"})
			return
		}
		rawCredential := strings.TrimSpace(strings.TrimPrefix(header, scheme))
		nodeUUID := strings.TrimSpace(c.GetHeader("X-MailHub-Node-UUID"))
		if rawCredential == "" || nodeUUID == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 1003, "message": "node credential and X-MailHub-Node-UUID are required"})
			return
		}
		principal, err := authenticator.AuthenticateCredential(rawCredential, nodeUUID, time.Now())
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 1004, "message": "invalid or revoked node credential"})
			return
		}
		if err := enforceNodeServerBinding(c, principal.ServerID); err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 1005, "message": err.Error()})
			return
		}
		c.Set(NodeServerIDContextKey, principal.ServerID)
		c.Set(NodeUUIDContextKey, principal.NodeUUID)
		c.Set(NodeAuthTypeContextKey, "node_credential")
		c.Next()
	}
}

func enforceNodeServerBinding(c *gin.Context, authenticatedServerID uint64) error {
	for _, raw := range []string{c.Param("id"), c.Query("server_id"), c.Query("node_id")} {
		if raw == "" {
			continue
		}
		if err := requireMatchingServerID(raw, authenticatedServerID); err != nil {
			return err
		}
	}
	contentType := strings.ToLower(c.GetHeader("Content-Type"))
	if c.Request.Body == nil || !strings.Contains(contentType, "application/json") {
		return nil
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return fmt.Errorf("cannot validate node request ownership")
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil // The handler remains responsible for malformed JSON.
	}
	return enforceJSONNodeBinding(payload, authenticatedServerID)
}

func enforceJSONNodeBinding(value any, authenticatedServerID uint64) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == "server_id" || key == "node_id" {
				if err := requireMatchingServerID(fmt.Sprint(child), authenticatedServerID); err != nil {
					return err
				}
			}
			if err := enforceJSONNodeBinding(child, authenticatedServerID); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := enforceJSONNodeBinding(child, authenticatedServerID); err != nil {
				return err
			}
		}
	}
	return nil
}

func requireMatchingServerID(raw string, authenticatedServerID uint64) error {
	raw = strings.TrimSpace(strings.TrimSuffix(raw, ".0"))
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return fmt.Errorf("invalid server ID in node request")
	}
	if value != authenticatedServerID {
		return fmt.Errorf("node credential cannot access server %d", value)
	}
	return nil
}
