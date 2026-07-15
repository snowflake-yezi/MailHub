package apiregistry

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRegisterRejectsPathBeyondDatabaseLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registry := New("/api/v1")
	group := gin.New().Group("/api/v1")

	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("Register did not reject an overlong external API path")
		}
	}()
	registry.Register(group, Route{
		Method: http.MethodGet, Path: "/" + strings.Repeat("a", 175),
		PermissionCode: "test:read", GroupName: "测试", Name: "测试接口",
		Handler: func(c *gin.Context) { c.Status(http.StatusOK) },
	})
}
