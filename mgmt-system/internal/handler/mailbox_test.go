package handler

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
)

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
