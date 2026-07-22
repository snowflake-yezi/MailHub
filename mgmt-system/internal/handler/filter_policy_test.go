package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	filtercontract "github.com/ticket/email-filter-contract"
	"github.com/ticket/email-mgmt-system/internal/apiregistry"
	"github.com/ticket/email-mgmt-system/internal/middleware"
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
		"GET /api/v1/admin/filter-quarantines",
		"GET /api/v1/admin/filter-quarantines/:quarantine_key/message",
		"GET /api/v1/admin/filter-quarantines/:quarantine_key/attachments/:index",
		"POST /api/v1/admin/filter-quarantines/:quarantine_key/release",
		"POST /api/v1/admin/filter-quarantines/:quarantine_key/allow-and-release",
		"POST /api/v1/admin/filter-quarantines/:quarantine_key/confirm-ad",
		"GET /api/v1/internal/filter-bundles/:policy_kind",
		"POST /api/v1/internal/filter-node-states",
		"POST /api/v1/internal/filter-decisions",
	} {
		if !routes[route] {
			t.Fatalf("required route %q is missing", route)
		}
	}
}

func TestFilterPolicyExternalRoutesUseIndependentPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registry := apiregistry.New("/api/v1")
	(&FilterPolicyHandler{}).RegisterExternalRoutes(registry, router.Group("/api/v1"))

	tests := []struct {
		method     string
		path       string
		permission string
	}{
		{http.MethodGet, "/manual-filter-revisions/active", "manual-filter:read"},
		{http.MethodPost, "/manual-filter-revisions", "manual-filter:draft"},
		{http.MethodGet, "/manual-filter-revisions/7", "manual-filter:read"},
		{http.MethodPost, "/manual-filter-revisions/7/rules", "manual-filter:draft"},
		{http.MethodPut, "/manual-filter-revisions/7/rules/rule-1", "manual-filter:draft"},
		{http.MethodDelete, "/manual-filter-revisions/7/rules/rule-1", "manual-filter:draft"},
		{http.MethodPost, "/manual-filter-revisions/7/validate", "manual-filter:draft"},
		{http.MethodPost, "/manual-filter-revisions/7/publish", "manual-filter:publish"},
		{http.MethodGet, "/ad-filter-revisions/active", "ad-filter:read"},
		{http.MethodPost, "/ad-filter-revisions", "ad-filter:draft"},
		{http.MethodGet, "/ad-filter-revisions/9", "ad-filter:read"},
		{http.MethodPost, "/ad-filter-revisions/9/detectors", "ad-filter:draft"},
		{http.MethodPut, "/ad-filter-revisions/9/detectors/detector-1", "ad-filter:draft"},
		{http.MethodDelete, "/ad-filter-revisions/9/detectors/detector-1", "ad-filter:draft"},
		{http.MethodPost, "/ad-filter-revisions/9/composites", "ad-filter:draft"},
		{http.MethodPut, "/ad-filter-revisions/9/composites/composite-1", "ad-filter:draft"},
		{http.MethodDelete, "/ad-filter-revisions/9/composites/composite-1", "ad-filter:draft"},
		{http.MethodPut, "/ad-filter-revisions/9/weights/AD_PROMO", "ad-filter:draft"},
		{http.MethodDelete, "/ad-filter-revisions/9/weights/AD_PROMO", "ad-filter:draft"},
		{http.MethodPost, "/ad-filter-revisions/9/validate", "ad-filter:draft"},
		{http.MethodPost, "/ad-filter-revisions/9/publish", "ad-filter:publish"},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "/api/v1"+test.path, nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "required: "+test.permission) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	if len(router.Routes()) != len(tests) {
		t.Fatalf("external route count = %d, want %d", len(router.Routes()), len(tests))
	}
}

func TestFilterPolicyExternalRoutesDoNotExposeEvidenceOrQuarantine(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registry := apiregistry.New("/api/v1")
	(&FilterPolicyHandler{}).RegisterExternalRoutes(registry, router.Group("/api/v1"))

	for _, path := range []string{
		"/api/v1/filter-decisions",
		"/api/v1/filter-decisions/decision-1",
		"/api/v1/filter-quarantines",
		"/api/v1/filter-quarantines/quarantine-1/message",
		"/api/v1/filter-quarantines/quarantine-1/attachments/0",
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 404", path, response.Code)
		}
	}
}

func TestFilterPolicyExternalRoutesRejectInvalidRevisionParameters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registry := apiregistry.New("/api/v1")
	group := router.Group("/api/v1")
	group.Use(func(c *gin.Context) {
		c.Set("api_principal", &middleware.APIPrincipal{Permissions: map[string]struct{}{"*": {}}})
	})
	(&FilterPolicyHandler{}).RegisterExternalRoutes(registry, group)

	for _, test := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/manual-filter-revisions/not-a-number"},
		{http.MethodPost, "/api/v1/manual-filter-revisions/0/publish"},
		{http.MethodGet, "/api/v1/ad-filter-revisions/not-a-number"},
		{http.MethodPost, "/api/v1/ad-filter-revisions/0/validate"},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "positive integer") {
			t.Fatalf("%s %s status=%d body=%s", test.method, test.path, response.Code, response.Body.String())
		}
	}
}

func TestFilterPolicyExternalPublishRejectsOversizedIdempotencyKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registry := apiregistry.New("/api/v1")
	group := router.Group("/api/v1")
	group.Use(func(c *gin.Context) {
		c.Set("api_principal", &middleware.APIPrincipal{Permissions: map[string]struct{}{"*": {}}})
	})
	(&FilterPolicyHandler{}).RegisterExternalRoutes(registry, group)

	for _, path := range []string{
		"/api/v1/manual-filter-revisions/7/publish",
		"/api/v1/ad-filter-revisions/9/publish",
	} {
		request := httptest.NewRequest(http.MethodPost, path, nil)
		request.Header.Set("Idempotency-Key", strings.Repeat("x", 65))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "64 characters") {
			t.Fatalf("POST %s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestPolicyActorPrefersAuthenticatedIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set("api_actor", "external-app:7:ticket")
	if got := policyActor(context); got != "external-app:7:ticket" {
		t.Fatalf("policyActor() = %q", got)
	}
	context.Set("admin_user", "admin")
	if got := policyActor(context); got != "admin" {
		t.Fatalf("admin policyActor() = %q", got)
	}
}

func TestReceiptFromProxyAcceptsDurableFailedReceipt(t *testing.T) {
	receipt := filtercontract.ReleaseReceipt{
		SchemaVersion: filtercontract.SchemaVersionV1, OperationID: "operation-1", QuarantineKey: "key",
		DecisionKey: "decision", Status: filtercontract.ReleaseStatusFailed, ErrorCode: "smtp_failed",
	}
	data, err := json.Marshal(map[string]any{"code": 0, "data": receipt})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := receiptFromProxy(data, nil)
	if err != nil || parsed.ErrorCode != "smtp_failed" {
		t.Fatalf("receiptFromProxy() = %#v, %v", parsed, err)
	}
}
