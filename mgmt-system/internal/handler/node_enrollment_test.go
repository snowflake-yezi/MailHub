package handler

import (
	"testing"

	"github.com/gin-gonic/gin"
	nodecontract "github.com/ticket/email-node-contract"
)

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
