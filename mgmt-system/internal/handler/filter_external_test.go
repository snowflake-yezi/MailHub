package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/apiregistry"
)

func TestRegisterExternalFilterRoutesRequiresExpectedPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/v1")
	registry := apiregistry.New("/api/v1")
	(&FilterHandler{}).RegisterExternalRoutes(registry, group)

	tests := []struct {
		method     string
		target     string
		permission string
	}{
		{method: http.MethodGet, target: "/api/v1/filters", permission: "filter:read"},
		{method: http.MethodPost, target: "/api/v1/filters", permission: "filter:create"},
		{method: http.MethodPut, target: "/api/v1/filters/12", permission: "filter:update"},
		{method: http.MethodDelete, target: "/api/v1/filters/12", permission: "filter:delete"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.target, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, tt.target, nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
			}
			var body struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Code != ErrCodeInsufficientScope {
				t.Fatalf("code = %d, want %d", body.Code, ErrCodeInsufficientScope)
			}
			if !strings.Contains(body.Message, "required: "+tt.permission) {
				t.Fatalf("message = %q, want required permission %q", body.Message, tt.permission)
			}
		})
	}
}
