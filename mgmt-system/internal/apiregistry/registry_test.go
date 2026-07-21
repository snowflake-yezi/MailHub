package apiregistry

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRegisterUsesSpecificResourceNameWithoutChangingPermissionName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registry := New("/api/v1")
	group := gin.New().Group("/api/v1")

	registry.Register(group, Route{
		Method: http.MethodGet, Path: "/orders/:order_id/emails",
		PermissionCode: "email:list", GroupName: "email", Name: "List email",
		ResourceName: "List email by order", Handler: func(c *gin.Context) { c.Status(http.StatusOK) },
	})

	if got := registry.permissions["email:list"].Name; got != "List email" {
		t.Fatalf("permission name = %q, want %q", got, "List email")
	}
	if got := registry.resources[0].Name; got != "List email by order" {
		t.Fatalf("resource name = %q, want %q", got, "List email by order")
	}
}

func TestRegisterFallsBackToPermissionNameForResource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registry := New("/api/v1")
	group := gin.New().Group("/api/v1")

	registry.Register(group, Route{
		Method: http.MethodPost, Path: "/mailboxes", PermissionCode: "mailbox:create",
		GroupName: "mailbox", Name: "Create mailbox", Handler: func(c *gin.Context) { c.Status(http.StatusOK) },
	})

	if got := registry.resources[0].Name; got != "Create mailbox" {
		t.Fatalf("resource name = %q, want fallback %q", got, "Create mailbox")
	}
}

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
