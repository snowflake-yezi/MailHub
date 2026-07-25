package nodetransport

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	nodecontract "github.com/ticket/email-node-contract"
)

func TestOperationTypesMatchNodeContract(t *testing.T) {
	commands := []Command{
		MailboxCreate("a@example.com", "secret"),
		MailboxDelete("a@example.com", time.Second),
		MailboxRestore("a@example.com", "secret"),
		MailboxPassword("a@example.com", "secret"),
		DomainApply("example.com"),
		DomainRemove("example.com"),
		MessageDelete("m-1", "a@example.com"),
		MessageRetentionPurge(nil, time.Second),
		QuarantineRelease("q-1", "op-1"),
		QuarantineGC(30, time.Second),
	}
	wantCommands := []nodecontract.CommandType{
		nodecontract.CommandMailboxCreate,
		nodecontract.CommandMailboxDelete,
		nodecontract.CommandMailboxRestore,
		nodecontract.CommandMailboxPassword,
		nodecontract.CommandDomainApply,
		nodecontract.CommandDomainRemove,
		nodecontract.CommandMessageDelete,
		nodecontract.CommandMessageRetentionPurge,
		nodecontract.CommandQuarantineRelease,
		nodecontract.CommandQuarantineGC,
	}
	for i := range commands {
		if commands[i].Type != wantCommands[i] || !json.Valid(commands[i].PayloadJSON) {
			t.Fatalf("command[%d] type=%q payload=%q", i, commands[i].Type, commands[i].PayloadJSON)
		}
	}
	if ConfigRevisionChanged(time.Second).Type != nodecontract.NotificationConfigRevisionChanged {
		t.Fatal("config notification type drifted from node-contract")
	}
	if FilterRevisionChanged().Type != nodecontract.NotificationFilterRevisionChanged {
		t.Fatal("filter notification type drifted from node-contract")
	}
	if MessageList("a@example.com", "1", "20").Type != nodecontract.DataRequestMessageList ||
		MessageBody("m-1", "a@example.com").Type != nodecontract.DataRequestMessageBody ||
		MessageRaw("m-1", "a@example.com").Type != nodecontract.DataRequestMessageRaw ||
		MessageAttachment("m-1", "a@example.com", "0").Type != nodecontract.DataRequestMessageAttachment ||
		MessageAttachmentPreview("m-1", "a@example.com", "0").Type != nodecontract.DataRequestMessageAttachmentPreview ||
		QuarantineMessage("q-1").Type != nodecontract.DataRequestQuarantineMessage ||
		QuarantineAttachment("q-1", "0").Type != nodecontract.DataRequestQuarantineAttachment {
		t.Fatal("data request type drifted from node-contract")
	}
}

func TestLegacyHTTPTransportBufferedOperations(t *testing.T) {
	type invocation func(NodeTransport, Target) (*Response, error)
	tests := []struct {
		name        string
		method      string
		path        string
		query       map[string]string
		body        string
		jsonContent bool
		invoke      invocation
	}{
		{"mailbox create", http.MethodPost, "/internal/mailboxes", nil, `{"email_address":"box@example.com","password":"pw"}`, true, func(tr NodeTransport, target Target) (*Response, error) {
			return tr.Execute(context.Background(), target, MailboxCreate("box@example.com", "pw"))
		}},
		{"mailbox delete", http.MethodDelete, "/internal/mailboxes/box+tag@example.com", nil, "", false, func(tr NodeTransport, target Target) (*Response, error) {
			return tr.Execute(context.Background(), target, MailboxDelete("box+tag@example.com", time.Second))
		}},
		{"mailbox restore", http.MethodPost, "/internal/mailboxes/box@example.com/restore", nil, `{"password":"pw"}`, true, func(tr NodeTransport, target Target) (*Response, error) {
			return tr.Execute(context.Background(), target, MailboxRestore("box@example.com", "pw"))
		}},
		{"mailbox password", http.MethodPut, "/internal/mailboxes/box@example.com/password", nil, `{"password":"pw"}`, true, func(tr NodeTransport, target Target) (*Response, error) {
			return tr.Execute(context.Background(), target, MailboxPassword("box@example.com", "pw"))
		}},
		{"domain apply", http.MethodPost, "/internal/domains", nil, `{"domain":"example.com"}`, true, func(tr NodeTransport, target Target) (*Response, error) {
			return tr.Execute(context.Background(), target, DomainApply("example.com"))
		}},
		{"domain remove", http.MethodDelete, "/internal/domains/example.com", nil, "", false, func(tr NodeTransport, target Target) (*Response, error) {
			return tr.Execute(context.Background(), target, DomainRemove("example.com"))
		}},
		{"message delete", http.MethodDelete, "/internal/messages/m-1", map[string]string{"mailbox": "box@example.com"}, "", true, func(tr NodeTransport, target Target) (*Response, error) {
			return tr.Execute(context.Background(), target, MessageDelete("m-1", "box@example.com"))
		}},
		{"message retention", http.MethodPost, "/internal/messages/retention/purge", nil, `{"items":[{"email_address":"box@example.com","retention_days":30}]}`, true, func(tr NodeTransport, target Target) (*Response, error) {
			return tr.Execute(context.Background(), target, MessageRetentionPurge([]RetentionItem{{EmailAddress: "box@example.com", RetentionDays: 30}}, time.Second))
		}},
		{"quarantine release", http.MethodPost, "/internal/filter-quarantines/q-1/release", nil, `{"operation_id":"op-1"}`, true, func(tr NodeTransport, target Target) (*Response, error) {
			return tr.Execute(context.Background(), target, QuarantineRelease("q-1", "op-1"))
		}},
		{"quarantine gc", http.MethodPost, "/internal/filter-quarantines/gc", nil, `{"retention_days":30}`, true, func(tr NodeTransport, target Target) (*Response, error) {
			return tr.Execute(context.Background(), target, QuarantineGC(30, time.Second))
		}},
		{"config notify", http.MethodPost, "/internal/configs/reload", nil, "", false, func(tr NodeTransport, target Target) (*Response, error) {
			return tr.Notify(context.Background(), target, ConfigRevisionChanged(time.Second))
		}},
		{"filter notify", http.MethodPost, "/internal/filters/reload", nil, "", false, func(tr NodeTransport, target Target) (*Response, error) {
			return tr.Notify(context.Background(), target, FilterRevisionChanged())
		}},
		{"message list", http.MethodGet, "/internal/mailboxes/box@example.com/messages", map[string]string{"page": "2", "size": "7"}, "", true, func(tr NodeTransport, target Target) (*Response, error) {
			return tr.Query(context.Background(), target, MessageList("box@example.com", "2", "7"))
		}},
		{"message body", http.MethodGet, "/internal/messages/m-1", map[string]string{"mailbox": "box@example.com"}, "", true, func(tr NodeTransport, target Target) (*Response, error) {
			return tr.Query(context.Background(), target, MessageBody("m-1", "box@example.com"))
		}},
		{"quarantine message", http.MethodGet, "/internal/filter-quarantines/q-1/message", nil, "", true, func(tr NodeTransport, target Target) (*Response, error) {
			return tr.Query(context.Background(), target, QuarantineMessage("q-1"))
		}},
		{"release status", http.MethodGet, "/internal/filter-quarantines/q-1/release-status", nil, "", true, func(tr NodeTransport, target Target) (*Response, error) {
			return tr.Query(context.Background(), target, QuarantineReleaseStatus("q-1"))
		}},
		{"health probe", http.MethodGet, "/internal/health", nil, "", false, func(tr NodeTransport, target Target) (*Response, error) {
			return tr.Probe(context.Background(), target)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tt.method || r.URL.Path != tt.path || r.Header.Get("X-Internal-Token") != "node-secret" {
					t.Errorf("request = %s %s token=%q", r.Method, r.URL.Path, r.Header.Get("X-Internal-Token"))
				}
				for key, want := range tt.query {
					if got := r.URL.Query().Get(key); got != want {
						t.Errorf("query %s = %q, want %q", key, got, want)
					}
				}
				if got := strings.HasPrefix(r.Header.Get("Content-Type"), "application/json"); got != tt.jsonContent {
					t.Errorf("JSON Content-Type = %v, want %v", got, tt.jsonContent)
				}
				body, _ := io.ReadAll(r.Body)
				if tt.body == "" {
					if len(body) != 0 {
						t.Errorf("body = %q, want empty", body)
					}
				} else {
					assertJSONEqual(t, body, []byte(tt.body))
				}
				w.Header().Set("X-Upstream", "preserved")
				w.WriteHeader(http.StatusMultiStatus)
				_, _ = w.Write([]byte(`{"code":0}`))
			}))
			defer server.Close()

			transport := NewLegacyHTTPTransportWithOptions("node-secret", LegacyHTTPOptions{Client: server.Client(), RawClient: server.Client()})
			response, err := tt.invoke(transport, Target{NodeID: 42, APIHost: strings.TrimPrefix(server.URL, "http://")})
			if err != nil {
				t.Fatalf("invoke: %v", err)
			}
			wantResponseBody := `{"code":0}`
			if tt.name == "config notify" || tt.name == "filter notify" {
				wantResponseBody = ""
			}
			if response.StatusCode != http.StatusMultiStatus || response.Header.Get("X-Upstream") != "preserved" || string(response.Body) != wantResponseBody {
				t.Fatalf("response = %#v", response)
			}
		})
	}
}

func TestLegacyOpenDataKeepsRequestAliveUntilBodyClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/messages/m-1/attachments/0" || r.URL.Query().Get("mailbox") != "box@example.com" {
			t.Errorf("request URI = %s", r.URL.RequestURI())
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="x.bin"`)
		w.WriteHeader(http.StatusPartialContent)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write([]byte("stream-body"))
	}))
	defer server.Close()

	transport := NewLegacyHTTPTransportWithOptions("secret", LegacyHTTPOptions{Client: server.Client(), RawClient: server.Client()})
	response, err := transport.OpenData(context.Background(), Target{APIHost: strings.TrimPrefix(server.URL, "http://")}, MessageAttachment("m-1", "box@example.com", "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read delayed stream: %v", err)
	}
	if response.StatusCode != http.StatusPartialContent || response.Header.Get("Content-Disposition") != `attachment; filename="x.bin"` || string(body) != "stream-body" {
		t.Fatalf("stream response status=%d headers=%v body=%q", response.StatusCode, response.Header, body)
	}
}

func TestLegacyTransportRequiresAPIHost(t *testing.T) {
	transport := NewLegacyHTTPTransport("secret")
	_, err := transport.Probe(context.Background(), Target{NodeID: 42})
	if err == nil || !strings.Contains(err.Error(), "node 42 has no legacy API host") {
		t.Fatalf("error = %v", err)
	}
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode got JSON %q: %v", got, err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("decode want JSON %q: %v", want, err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
}
