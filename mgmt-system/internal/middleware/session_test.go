package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/model"
)

type fakeAdminReader struct {
	user *model.AdminUser
	err  error
}

func (f *fakeAdminReader) GetByID(uint64) (*model.AdminUser, error) { return f.user, f.err }

func TestAdminAuthRequiredChecksCredentialVersionAndStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		user *model.AdminUser
		err  error
		want int
	}{
		{name: "valid", user: &model.AdminUser{ID: 7, Username: "admin", Status: "active", CredentialVersion: 2}, want: http.StatusOK},
		{name: "version changed", user: &model.AdminUser{ID: 7, Username: "admin", Status: "active", CredentialVersion: 3}, want: http.StatusUnauthorized},
		{name: "disabled", user: &model.AdminUser{ID: 7, Username: "admin", Status: "disabled", CredentialVersion: 2}, want: http.StatusUnauthorized},
		{name: "missing", err: errors.New("missing"), want: http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := NewSessionManager(time.Hour, "test_session", time.Hour)
			token, err := sm.CreateSession(7, "admin", 2)
			if err != nil {
				t.Fatal(err)
			}
			r := gin.New()
			r.GET("/api/v1/admin/check", AdminAuthRequired(sm, &fakeAdminReader{user: tt.user, err: tt.err}), func(c *gin.Context) { c.Status(http.StatusOK) })
			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/check", nil)
			req.AddCookie(&http.Cookie{Name: "test_session", Value: token})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d, body=%s", w.Code, tt.want, w.Body.String())
			}
		})
	}
}
