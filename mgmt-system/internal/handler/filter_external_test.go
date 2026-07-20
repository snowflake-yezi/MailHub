package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/model"
)

func TestLegacyFilterRoutesAreAdminOrInternalOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := &FilterHandler{}
	handler.RegisterAdminRoutes(router.Group("/api/v1/admin"))
	handler.RegisterInternalRoutes(router.Group("/api/v1/internal"))

	routes := make(map[string]bool)
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, route := range []string{
		"GET /api/v1/admin/filters",
		"POST /api/v1/admin/filters",
		"PUT /api/v1/admin/filters/:id",
		"DELETE /api/v1/admin/filters/:id",
		"GET /api/v1/internal/filters",
	} {
		if !routes[route] {
			t.Fatalf("required migration route %q is missing", route)
		}
	}

	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
		target := "/api/v1/filters"
		if method == http.MethodPut || method == http.MethodDelete {
			target += "/12"
		}
		request := httptest.NewRequest(method, target, nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s %s status = %d, want 404", method, target, response.Code)
		}
	}
}

func TestValidateLegacyFilterRule(t *testing.T) {
	valid := model.FilterRule{
		Name: "Promotion subject", RuleType: "regex", Pattern: `(?i)summer sale`,
		Action: "flag", Priority: 10, Enabled: true,
	}
	if err := validateLegacyFilterRule(&valid); err != nil {
		t.Fatalf("valid rule rejected: %v", err)
	}

	tests := []struct {
		name string
		rule model.FilterRule
	}{
		{name: "missing name", rule: model.FilterRule{RuleType: "keyword", Pattern: "sale", Action: "pass"}},
		{name: "long name", rule: model.FilterRule{Name: strings.Repeat("n", 129), RuleType: "keyword", Pattern: "sale", Action: "pass"}},
		{name: "missing pattern", rule: model.FilterRule{Name: "name", RuleType: "keyword", Pattern: " ", Action: "pass"}},
		{name: "long pattern", rule: model.FilterRule{Name: "name", RuleType: "keyword", Pattern: strings.Repeat("p", 513), Action: "pass"}},
		{name: "invalid type", rule: model.FilterRule{Name: "name", RuleType: "script", Pattern: "sale", Action: "pass"}},
		{name: "invalid action", rule: model.FilterRule{Name: "name", RuleType: "keyword", Pattern: "sale", Action: "delete"}},
		{name: "invalid regex", rule: model.FilterRule{Name: "name", RuleType: "regex", Pattern: "(", Action: "flag"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateLegacyFilterRule(&test.rule); err == nil {
				t.Fatal("invalid rule accepted")
			}
		})
	}
}

func TestCreateLegacyFilterRejectsInvalidRegexBeforeStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	(&FilterHandler{}).RegisterAdminRoutes(router.Group("/api/v1/admin"))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/filters", strings.NewReader(
		`{"name":"bad regex","rule_type":"regex","pattern":"(","action":"flag","enabled":true}`,
	))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", response.Code, response.Body.String())
	}
}
