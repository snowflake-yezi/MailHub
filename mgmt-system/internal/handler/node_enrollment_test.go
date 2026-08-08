package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	nodecontract "github.com/ticket/email-node-contract"
)

type recordingSessionRevoker struct {
	serverID uint64
	cause    error
	result   bool
}

func (revoker *recordingSessionRevoker) DisconnectServer(serverID uint64, cause error) bool {
	revoker.serverID = serverID
	revoker.cause = cause
	return revoker.result
}

func TestNodeEnrollmentRoutesMatchFrozenContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewNodeEnrollmentHandler(nil)
	handler.RegisterAdminRoutes(router.Group("/api/v1/admin"))
	handler.RegisterBootstrapRoutes(router.Group("/api/v1/node-enrollments"))

	registered := make(map[string]struct{})
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = struct{}{}
	}
	for _, route := range nodecontract.EnrollmentRoutesV1 {
		if _, ok := registered[route.Method+" "+route.Path]; !ok {
			t.Errorf("missing frozen enrollment route %s %s", route.Method, route.Path)
		}
	}
	if _, ok := registered["GET /api/v1/admin/servers/:id/credentials"]; !ok {
		t.Error("missing credential metadata route")
	}
}

func TestDisconnectNodeRequiresAnActiveControlSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	revoker := &recordingSessionRevoker{result: true}
	handler := NewNodeEnrollmentHandler(nil)
	handler.ConfigureSessionRevoker(revoker)
	router := gin.New()
	handler.RegisterAdminRoutes(router.Group("/api/v1/admin"))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/servers/42/disconnect", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || revoker.serverID != 42 || revoker.cause == nil {
		t.Fatalf("disconnect response = %d, revoker = %+v", recorder.Code, revoker)
	}
	if revoker.cause.Error() != "node control session disconnected by administrator" {
		t.Fatalf("disconnect cause = %q", revoker.cause)
	}

	revoker.result = false
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/servers/42/disconnect", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("offline disconnect response = %d, want %d", recorder.Code, http.StatusConflict)
	}
}
