package lifecycle

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/nodetransport"
)

func TestCallNodePurgeExpiredMessagesBatch(t *testing.T) {
	var itemCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/internal/messages/retention/purge" || r.Header.Get("X-Internal-Token") != "secret" {
			t.Fatalf("unexpected request: %s %s token=%q", r.Method, r.URL.Path, r.Header.Get("X-Internal-Token"))
		}
		var body struct {
			Items []map[string]interface{} `json:"items"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		itemCount = len(body.Items)
		_, _ = w.Write([]byte(`{"code":0,"data":{"deleted":2}}`))
	}))
	defer server.Close()
	transport := nodetransport.NewLegacyHTTPTransportWithOptions("secret", nodetransport.LegacyHTTPOptions{Client: server.Client()})
	scheduler := &Scheduler{transport: transport, operationTimeout: time.Second}
	items := []nodetransport.RetentionItem{{EmailAddress: "a@example.com", RetentionDays: 30}, {EmailAddress: "b@example.com", RetentionDays: 7}}
	node := &model.MailServer{APIHost: strings.TrimPrefix(server.URL, "http://")}
	deleted, err := scheduler.callNodePurgeExpiredMessagesBatch(node, items)
	if err != nil || deleted != 2 || itemCount != 2 {
		t.Fatalf("deleted=%d items=%d err=%v", deleted, itemCount, err)
	}
}
