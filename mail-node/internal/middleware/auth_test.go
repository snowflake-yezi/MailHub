package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestNodeAuthRequiredAcceptsDualModeCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		shared     string
		nodeUUID   string
		credential string
		headers    map[string]string
		wantStatus int
	}{
		{
			name: "node credential", shared: "legacy-secret", nodeUUID: "node-a", credential: "node-secret",
			headers: map[string]string{"Authorization": "Node node-secret", "X-MailHub-Node-UUID": "node-a"}, wantStatus: http.StatusNoContent,
		},
		{
			name: "legacy fallback while enrolled", shared: "legacy-secret", nodeUUID: "node-a", credential: "node-secret",
			headers: map[string]string{"X-Internal-Token": "legacy-secret"}, wantStatus: http.StatusNoContent,
		},
		{
			name: "valid fallback overrides wrong node credential", shared: "legacy-secret", nodeUUID: "node-a", credential: "node-secret",
			headers: map[string]string{"Authorization": "Node wrong", "X-MailHub-Node-UUID": "node-a", "X-Internal-Token": "legacy-secret"}, wantStatus: http.StatusNoContent,
		},
		{
			name: "valid fallback overrides wrong node UUID", shared: "legacy-secret", nodeUUID: "node-a", credential: "node-secret",
			headers: map[string]string{"Authorization": "Node node-secret", "X-MailHub-Node-UUID": "node-b", "X-Internal-Token": "legacy-secret"}, wantStatus: http.StatusNoContent,
		},
		{
			name: "legacy node", shared: "legacy-secret",
			headers: map[string]string{"X-Internal-Token": "legacy-secret"}, wantStatus: http.StatusNoContent,
		},
		{
			name: "wrong node credential cannot bypass", shared: "legacy-secret", nodeUUID: "node-a", credential: "node-secret",
			headers: map[string]string{"Authorization": "Node wrong", "X-MailHub-Node-UUID": "node-a"}, wantStatus: http.StatusUnauthorized,
		},
		{
			name: "wrong shared secret", shared: "legacy-secret", nodeUUID: "node-a", credential: "node-secret",
			headers: map[string]string{"X-Internal-Token": "wrong"}, wantStatus: http.StatusUnauthorized,
		},
		{
			name:    "empty configuration fails closed",
			headers: map[string]string{}, wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			router.Use(NodeAuthRequired(tt.shared, func() (string, string) { return tt.nodeUUID, tt.credential }))
			router.GET("/internal/health", func(c *gin.Context) { c.Status(http.StatusNoContent) })

			request := httptest.NewRequest(http.MethodGet, "/internal/health", nil)
			for name, value := range tt.headers {
				request.Header.Set(name, value)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tt.wantStatus, response.Body.String())
			}
		})
	}
}

func TestNodeAuthRequiredReadsRotatedCredentialPerRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	nodeUUID, credential := "node-a", "first-secret"
	router := gin.New()
	router.Use(NodeAuthRequired("", func() (string, string) { return nodeUUID, credential }))
	router.GET("/internal/health", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := func(secret string) int {
		req := httptest.NewRequest(http.MethodGet, "/internal/health", nil)
		req.Header.Set("Authorization", "Node "+secret)
		req.Header.Set("X-MailHub-Node-UUID", nodeUUID)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response.Code
	}
	if status := request("first-secret"); status != http.StatusNoContent {
		t.Fatalf("initial credential status = %d", status)
	}
	credential = "rotated-secret"
	if status := request("first-secret"); status != http.StatusUnauthorized {
		t.Fatalf("old credential status = %d", status)
	}
	if status := request("rotated-secret"); status != http.StatusNoContent {
		t.Fatalf("rotated credential status = %d", status)
	}
}
