package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/apiregistry"
)

func TestRegisterExternalMailboxRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registry := apiregistry.New("/api/v1")
	(&MailboxHandler{}).RegisterExternalRoutes(registry, router.Group("/api/v1"))

	want := map[string]bool{
		http.MethodPost + " /api/v1/mailboxes":                      false,
		http.MethodGet + " /api/v1/mailboxes/:mailbox_ref":          false,
		http.MethodPost + " /api/v1/mailboxes/:mailbox_ref/disable": false,
	}
	for _, route := range router.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, registered := range want {
		if !registered {
			t.Errorf("external mailbox route %s was not registered", route)
		}
	}
}

func TestMailboxListFilterFromQuerySeparatesTrashView(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name                string
		target              string
		wantStatus          string
		wantStatuses        []string
		wantExcludeStatuses []string
		wantSearch          string
		wantDomainID        uint64
		wantServerID        uint64
	}{
		{
			name:                "normal default excludes recycle-bin statuses",
			target:              "/mailboxes",
			wantExcludeStatuses: []string{"soft_deleted", "purged"},
		},
		{
			name:         "trash includes only recycle-bin statuses",
			target:       "/mailboxes?view=trash",
			wantStatuses: []string{"soft_deleted", "purged"},
		},
		{
			name:                "normal status filter still excludes recycle-bin statuses",
			target:              "/mailboxes?status=active&search=foo&domain_id=7&server_id=9",
			wantStatus:          "active",
			wantExcludeStatuses: []string{"soft_deleted", "purged"},
			wantSearch:          "foo",
			wantDomainID:        7,
			wantServerID:        9,
		},
		{
			name:         "trash ignores conflicting single status filter",
			target:       "/mailboxes?view=trash&status=active&order_id=legacy",
			wantStatuses: []string{"soft_deleted", "purged"},
			wantSearch:   "legacy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, tt.target, nil)

			got := mailboxListFilterFromQuery(c)
			if got.Status != tt.wantStatus {
				t.Fatalf("Status = %q, want %q", got.Status, tt.wantStatus)
			}
			if !reflect.DeepEqual(got.Statuses, tt.wantStatuses) {
				t.Fatalf("Statuses = %#v, want %#v", got.Statuses, tt.wantStatuses)
			}
			if !reflect.DeepEqual(got.ExcludeStatuses, tt.wantExcludeStatuses) {
				t.Fatalf("ExcludeStatuses = %#v, want %#v", got.ExcludeStatuses, tt.wantExcludeStatuses)
			}
			if got.Search != tt.wantSearch {
				t.Fatalf("Search = %q, want %q", got.Search, tt.wantSearch)
			}
			if got.DomainID != tt.wantDomainID {
				t.Fatalf("DomainID = %d, want %d", got.DomainID, tt.wantDomainID)
			}
			if got.ServerID != tt.wantServerID {
				t.Fatalf("ServerID = %d, want %d", got.ServerID, tt.wantServerID)
			}
		})
	}
}

func TestParseOptionalFormUint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name    string
		value   string
		want    uint64
		wantErr bool
	}{
		{name: "missing uses automatic allocation", value: "", want: 0},
		{name: "zero uses automatic allocation", value: "0", want: 0},
		{name: "valid id", value: "42", want: 42},
		{name: "invalid id", value: "server-1", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{"server_id": {tt.value}}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/mailboxes/upload", strings.NewReader(form.Encode()))
			c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			got, err := parseOptionalFormUint(c, "server_id")
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("value = %d, want %d", got, tt.want)
			}
		})
	}
}
