package handler

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestFilterPolicyRoutesCoverAdminAndNodeContracts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := &FilterPolicyHandler{}
	handler.RegisterAdminRoutes(router.Group("/api/v1/admin"))
	handler.RegisterInternalRoutes(router.Group("/api/v1/internal"))
	routes := make(map[string]bool)
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, route := range []string{
		"GET /api/v1/admin/filter-policy-status",
		"GET /api/v1/admin/manual-filter-revisions",
		"POST /api/v1/admin/manual-filter-revisions",
		"POST /api/v1/admin/manual-filter-revisions/:revision/publish",
		"POST /api/v1/admin/manual-filter-revisions/:revision/clone",
		"GET /api/v1/admin/ad-filter-revisions",
		"POST /api/v1/admin/ad-filter-revisions",
		"POST /api/v1/admin/ad-filter-revisions/:revision/detectors",
		"POST /api/v1/admin/ad-filter-revisions/:revision/composites",
		"PUT /api/v1/admin/ad-filter-revisions/:revision/weights/:symbol",
		"POST /api/v1/admin/ad-filter-revisions/:revision/publish",
		"GET /api/v1/admin/filter-decisions",
		"GET /api/v1/admin/filter-decisions/:decision_key",
		"GET /api/v1/internal/filter-bundles/:policy_kind",
		"POST /api/v1/internal/filter-node-states",
		"POST /api/v1/internal/filter-decisions",
	} {
		if !routes[route] {
			t.Fatalf("required route %q is missing", route)
		}
	}
}
