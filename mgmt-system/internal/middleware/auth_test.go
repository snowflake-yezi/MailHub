package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/model"
)

func TestHasScope(t *testing.T) {
	tests := []struct {
		name     string
		scopes   string
		required string
		want     bool
	}{
		{name: "wildcard", scopes: "*", required: "email:read", want: true},
		{name: "wildcard in list", scopes: "mailbox:create, *", required: "email:read", want: true},
		{name: "exact", scopes: "mailbox:create,email:read", required: "email:read", want: true},
		{name: "trim spaces", scopes: "mailbox:create, email:read ", required: "email:read", want: true},
		{name: "substring suffix denied", scopes: "email:readonly", required: "email:read", want: false},
		{name: "substring wrapper denied", scopes: "fooemail:readbar", required: "email:read", want: false},
		{name: "empty denied", scopes: "", required: "email:read", want: false},
		{name: "empty items ignored", scopes: ",,email:read,,", required: "email:read", want: true},
		{name: "empty required denied", scopes: "*", required: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasScope(tt.scopes, tt.required); got != tt.want {
				t.Fatalf("hasScope(%q, %q) = %v, want %v", tt.scopes, tt.required, got, tt.want)
			}
		})
	}
}

func TestRequireScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		token      *model.ApiToken
		wantStatus int
		wantCalled bool
	}{
		{name: "missing token context", token: nil, wantStatus: http.StatusForbidden},
		{name: "insufficient scope", token: &model.ApiToken{Scopes: "mailbox:create"}, wantStatus: http.StatusForbidden},
		{name: "substring scope denied", token: &model.ApiToken{Scopes: "email:readonly"}, wantStatus: http.StatusForbidden},
		{name: "exact scope allowed", token: &model.ApiToken{Scopes: "mailbox:create,email:read"}, wantStatus: http.StatusOK, wantCalled: true},
		{name: "wildcard allowed", token: &model.ApiToken{Scopes: "*"}, wantStatus: http.StatusOK, wantCalled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			r := gin.New()
			r.GET("/protected", func(c *gin.Context) {
				if tt.token != nil {
					c.Set("api_token", tt.token)
				}
			}, RequireScope("email:read"), func(c *gin.Context) {
				called = true
				c.Status(http.StatusOK)
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", w.Code, tt.wantStatus, w.Body.String())
			}
			if called != tt.wantCalled {
				t.Fatalf("handler called = %v, want %v", called, tt.wantCalled)
			}
		})
	}
}
