package handler

import (
	"encoding/json"
	"testing"

	"github.com/gin-gonic/gin"
	filtercontract "github.com/ticket/email-filter-contract"
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
