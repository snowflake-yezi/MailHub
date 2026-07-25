package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
)

// NodeAuthRequired accepts a per-node credential and retains shared-secret
// compatibility for legacy nodes. The credential provider is evaluated per
// request so credential rotation takes effect without restarting the node.
func NodeAuthRequired(sharedSecret string, credentialProvider func() (string, string)) gin.HandlerFunc {
	return func(c *gin.Context) {
		if credentialProvider != nil {
			nodeUUID, credential := credentialProvider()
			if nodeUUID != "" && credential != "" {
				if c.GetHeader("X-MailHub-Node-UUID") != nodeUUID || subtle.ConstantTimeCompare([]byte(c.GetHeader("Authorization")), []byte("Node "+credential)) != 1 {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 1003, "message": "invalid node credential"})
					return
				}
				c.Next()
				return
			}
		}
		InternalAuthRequired(sharedSecret)(c)
	}
}

// InternalAuthRequired validates the X-Internal-Token header against the configured
// shared secret. Used on mail-node's /internal/* routes that are called by mgmt-system.
// If sharedSecret is empty, the middleware fails closed (rejects all requests).
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
