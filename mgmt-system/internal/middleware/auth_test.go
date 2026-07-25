package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/service"
	"github.com/ticket/email-mgmt-system/internal/store"
)

type stubNodeAuthenticator struct{}

func (stubNodeAuthenticator) AuthenticateCredential(credential, nodeUUID string, _ time.Time) (*service.NodePrincipal, error) {
	if credential != "node-secret" || nodeUUID != "6ba7b810-9dad-41d1-80b4-00c04fd430c8" {
		return nil, service.ErrNodeCredentialInvalid
	}
	return &service.NodePrincipal{ServerID: 42, NodeUUID: nodeUUID, CredentialID: 7, CredentialVer: 1}, nil
}

type stubAPIAuthStore struct {
	client     *store.AuthenticatedAPIClient
	err        error
	usageCalls int
	logs       []*model.APIAccessLog
}

func (s *stubAPIAuthStore) AuthenticateAPICredential(string, time.Time) (*store.AuthenticatedAPIClient, error) {
	return s.client, s.err
}

func (s *stubAPIAuthStore) UpdateAPICredentialUsage(uint64, time.Time, string) {
	s.usageCalls++
}

func (s *stubAPIAuthStore) CreateAPIAccessLog(entry *model.APIAccessLog) {
	s.logs = append(s.logs, entry)
}

func TestAuthRequiredFailsClosedWithoutLegacyFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, testErr := range []error{store.ErrInvalidAPICredential, errors.New("database unavailable")} {
		st := &stubAPIAuthStore{err: testErr}
		called := false
		router := gin.New()
		router.GET("/protected", AuthRequired(st), func(c *gin.Context) {
			called = true
			c.Status(http.StatusOK)
		})

		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/protected", nil)
		request.Header.Set("Authorization", "Bearer legacy-plaintext-token")
		router.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized || called {
			t.Fatalf("err=%v status=%d called=%v body=%s", testErr, response.Code, called, response.Body.String())
		}
		if st.usageCalls != 0 || len(st.logs) != 0 {
			t.Fatalf("invalid credential produced usage=%d logs=%d", st.usageCalls, len(st.logs))
		}
	}
}

func TestAuthRequiredUsesNormalizedPrincipal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	st := &stubAPIAuthStore{client: &store.AuthenticatedAPIClient{
		Application: model.APIApplication{ID: 7, Name: "ticket"},
		Credential:  model.APICredential{ID: 11},
		Permissions: []string{"email:list"},
	}}
	var auditActor string
	router := gin.New()
	router.GET("/protected", AuthRequired(st), RequirePermission("email:list"), func(c *gin.Context) {
		auditActor = c.GetString("api_actor")
		c.Status(http.StatusNoContent)
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer normalized-token")
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if st.usageCalls != 1 || len(st.logs) != 1 || st.logs[0].CredentialID != 11 {
		t.Fatalf("usage=%d logs=%+v", st.usageCalls, st.logs)
	}
	if auditActor != "external-app:7:ticket" {
		t.Fatalf("api_actor = %q", auditActor)
	}
}

func TestRequirePermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		principal  *APIPrincipal
		permission string
		wantStatus int
	}{
		{name: "normalized exact permission", principal: &APIPrincipal{Permissions: map[string]struct{}{"email:list": {}}}, permission: "email:list", wantStatus: http.StatusOK},
		{name: "normalized substring denied", principal: &APIPrincipal{Permissions: map[string]struct{}{"email:list:all": {}}}, permission: "email:list", wantStatus: http.StatusForbidden},
		{name: "normalized wildcard", principal: &APIPrincipal{Permissions: map[string]struct{}{"*": {}}}, permission: "mailbox:disable", wantStatus: http.StatusOK},
		{name: "missing principal", permission: "mailbox:create", wantStatus: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/protected", func(c *gin.Context) {
				if tt.principal != nil {
					c.Set("api_principal", tt.principal)
				}
			}, RequirePermission(tt.permission), func(c *gin.Context) { c.Status(http.StatusOK) })

			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/protected", nil))
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", response.Code, tt.wantStatus, response.Body.String())
			}
		})
	}
}

func TestInternalNodeAuthSupportsLegacyAndBoundNodeCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/internal/servers/:id/report", InternalNodeAuthRequired("legacy-secret", stubNodeAuthenticator{}), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"auth": c.GetString(NodeAuthTypeContextKey), "server_id": c.GetUint64(NodeServerIDContextKey)})
	})

	legacy := httptest.NewRequest(http.MethodPost, "/internal/servers/99/report", strings.NewReader(`{"server_id":99}`))
	legacy.Header.Set("Content-Type", "application/json")
	legacy.Header.Set("X-Internal-Token", "legacy-secret")
	legacyResponse := httptest.NewRecorder()
	router.ServeHTTP(legacyResponse, legacy)
	if legacyResponse.Code != http.StatusOK || !strings.Contains(legacyResponse.Body.String(), "shared_secret") {
		t.Fatalf("legacy status=%d body=%s", legacyResponse.Code, legacyResponse.Body.String())
	}

	node := httptest.NewRequest(http.MethodPost, "/internal/servers/42/report?node_id=42", strings.NewReader(`{"server_id":42}`))
	node.Header.Set("Content-Type", "application/json")
	node.Header.Set("Authorization", "Node node-secret")
	node.Header.Set("X-MailHub-Node-UUID", "6ba7b810-9dad-41d1-80b4-00c04fd430c8")
	nodeResponse := httptest.NewRecorder()
	router.ServeHTTP(nodeResponse, node)
	if nodeResponse.Code != http.StatusOK || !strings.Contains(nodeResponse.Body.String(), "node_credential") || !strings.Contains(nodeResponse.Body.String(), "42") {
		t.Fatalf("node status=%d body=%s", nodeResponse.Code, nodeResponse.Body.String())
	}
}

func TestInternalNodeAuthRejectsCrossNodeIdentifiers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/internal/servers/:id/report", InternalNodeAuthRequired("legacy-secret", stubNodeAuthenticator{}), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	tests := []struct {
		name string
		url  string
		body string
	}{
		{name: "path", url: "/internal/servers/41/report", body: `{}`},
		{name: "query", url: "/internal/servers/42/report?server_id=41", body: `{}`},
		{name: "body", url: "/internal/servers/42/report", body: `{"node_id":41}`},
		{name: "nested body", url: "/internal/servers/42/report", body: `{"reports":[{"server_id":41}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, tt.url, strings.NewReader(tt.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Node node-secret")
			request.Header.Set("X-MailHub-Node-UUID", "6ba7b810-9dad-41d1-80b4-00c04fd430c8")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
