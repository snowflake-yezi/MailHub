package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/apiregistry"
	"github.com/ticket/email-mgmt-system/internal/model"
)

type emailStoreStub struct {
	mailboxByEmail map[string]*model.MailboxAccount
	mailboxByOrder map[string]*model.MailboxAccount
	servers        map[uint64]*model.MailServer
	serverByDomain map[string]*model.MailServer
	emailLookups   []string
	orderLookups   []string
	domainLookups  []string
}

func (s *emailStoreStub) GetMailboxByEmail(email string) (*model.MailboxAccount, error) {
	s.emailLookups = append(s.emailLookups, email)
	mailbox, ok := s.mailboxByEmail[email]
	if !ok {
		return nil, errors.New("mailbox not found")
	}
	return mailbox, nil
}

func (s *emailStoreStub) GetMailboxByOrderID(orderID string) (*model.MailboxAccount, error) {
	s.orderLookups = append(s.orderLookups, orderID)
	mailbox, ok := s.mailboxByOrder[orderID]
	if !ok {
		return nil, errors.New("mailbox not found")
	}
	return mailbox, nil
}

func (s *emailStoreStub) GetServer(id uint64) (*model.MailServer, error) {
	server, ok := s.servers[id]
	if !ok {
		return nil, errors.New("server not found")
	}
	return server, nil
}

func (s *emailStoreStub) FindServerByEmailDomain(domain string) (*model.MailServer, error) {
	s.domainLookups = append(s.domainLookups, domain)
	server, ok := s.serverByDomain[domain]
	if !ok {
		return nil, errors.New("server not found")
	}
	return server, nil
}

func newEmailHandlerForUpstream(apiHost string) *EmailHandler {
	mailbox := &model.MailboxAccount{EmailAddress: "box@example.com", ServerID: 9}
	store := &emailStoreStub{
		mailboxByEmail: map[string]*model.MailboxAccount{mailbox.EmailAddress: mailbox},
		mailboxByOrder: map[string]*model.MailboxAccount{},
		servers: map[uint64]*model.MailServer{
			9: {ID: 9, APIHost: apiHost},
		},
	}
	return NewEmailHandler(store, newTestNodeTransport("shared-secret", http.DefaultClient))
}

func TestEmailListRoutesProxyByOrderOrMailbox(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Internal-Token"); got != "shared-secret" {
			t.Errorf("X-Internal-Token = %q, want shared-secret", got)
		}
		if got := r.URL.Path; got != "/internal/mailboxes/box+tag@example.com/messages" {
			t.Errorf("upstream path = %q", got)
		}
		if got := r.URL.Query().Get("page"); got != "3" {
			t.Errorf("page = %q, want 3", got)
		}
		if got := r.URL.Query().Get("size"); got != "7" {
			t.Errorf("size = %q, want 7", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"page":3,"size":7,"total":1,"messages":[{"message_id":"m-1"}]}}`))
	}))
	defer upstream.Close()

	mailbox := &model.MailboxAccount{EmailAddress: "box+tag@example.com", ServerID: 9}
	store := &emailStoreStub{
		mailboxByEmail: map[string]*model.MailboxAccount{mailbox.EmailAddress: mailbox},
		mailboxByOrder: map[string]*model.MailboxAccount{"ORDER-42": mailbox},
		servers: map[uint64]*model.MailServer{
			9: {ID: 9, APIHost: strings.TrimPrefix(upstream.URL, "http://")},
		},
	}
	handler := NewEmailHandler(store, newTestNodeTransport("shared-secret", upstream.Client()))
	router := gin.New()
	router.GET("/orders/:order_id/emails", handler.GetOrderEmails)
	router.GET("/mailboxes/:mailbox_ref/messages", handler.GetMailboxMessages)

	tests := []struct {
		name        string
		target      string
		wantOrderID string
	}{
		{name: "order", target: "/orders/ORDER-42/emails?page=3&size=7", wantOrderID: "ORDER-42"},
		{name: "mailbox", target: "/mailboxes/box%2Btag%40example.com/messages?page=3&size=7"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, tt.target, nil)
			router.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			var body struct {
				Code      int    `json:"code"`
				Message   string `json:"message"`
				RequestID string `json:"request_id"`
				Data      struct {
					OrderID      string            `json:"order_id"`
					EmailAddress string            `json:"email_address"`
					Page         int               `json:"page"`
					Size         int               `json:"size"`
					Total        int               `json:"total"`
					Messages     []json.RawMessage `json:"messages"`
				} `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Code != 0 || body.Message != "success" || body.RequestID == "" {
				t.Fatalf("response envelope = %#v", body)
			}
			if body.Data.OrderID != tt.wantOrderID {
				t.Errorf("order_id = %q, want %q", body.Data.OrderID, tt.wantOrderID)
			}
			if body.Data.EmailAddress != mailbox.EmailAddress || body.Data.Page != 3 || body.Data.Size != 7 || body.Data.Total != 1 || len(body.Data.Messages) != 1 {
				t.Errorf("response data = %#v", body.Data)
			}
		})
	}

	if len(store.orderLookups) != 1 || store.orderLookups[0] != "ORDER-42" {
		t.Fatalf("order lookups = %#v", store.orderLookups)
	}
	if len(store.emailLookups) != 1 || store.emailLookups[0] != mailbox.EmailAddress {
		t.Fatalf("email lookups = %#v", store.emailLookups)
	}
}

func TestGetOrderEmailsReturnsNotFoundForUnknownOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &emailStoreStub{
		mailboxByEmail: map[string]*model.MailboxAccount{},
		mailboxByOrder: map[string]*model.MailboxAccount{},
		servers:        map[uint64]*model.MailServer{},
	}
	handler := NewEmailHandler(store, newTestNodeTransport("shared-secret", http.DefaultClient))
	router := gin.New()
	router.GET("/orders/:order_id/emails", handler.GetOrderEmails)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/orders/UNKNOWN/emails", nil))

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body Response
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != ErrCodeNotFound || !strings.Contains(body.Message, "UNKNOWN") {
		t.Fatalf("body = %#v", body)
	}
}

func TestGetEmailBodyPassesUpstreamNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Internal-Token"); got != "shared-secret" {
			t.Errorf("X-Internal-Token = %q, want shared-secret", got)
		}
		if got := r.URL.Path; got != "/internal/messages/missing-message" {
			t.Errorf("upstream path = %q", got)
		}
		if got := r.URL.Query().Get("mailbox"); got != "box@example.com" {
			t.Errorf("mailbox = %q, want box@example.com", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":2003,"message":"message not found"}`))
	}))
	defer upstream.Close()

	handler := newEmailHandlerForUpstream(strings.TrimPrefix(upstream.URL, "http://"))
	router := gin.New()
	router.GET("/emails/:message_id/body", handler.GetEmailBody)

	response := httptest.NewRecorder()
	target := "/emails/missing-message/body?mailbox=box%40example.com"
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body Response
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != ErrCodeNotFound || body.Message != "message not found" || body.RequestID == "" {
		t.Fatalf("body = %#v", body)
	}
}

func TestGetEmailBodyProxiesSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"message_id":"m-1","text_body":"hello"}}`))
	}))
	defer upstream.Close()

	handler := newEmailHandlerForUpstream(strings.TrimPrefix(upstream.URL, "http://"))
	router := gin.New()
	router.GET("/emails/:message_id/body", handler.GetEmailBody)

	response := httptest.NewRecorder()
	target := "/emails/m-1/body?mailbox=box%40example.com"
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Code      int    `json:"code"`
		RequestID string `json:"request_id"`
		Data      struct {
			MessageID string `json:"message_id"`
			TextBody  string `json:"text_body"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != 0 || body.RequestID == "" || body.Data.MessageID != "m-1" || body.Data.TextBody != "hello" {
		t.Fatalf("body = %#v", body)
	}
}

func TestGetRawEmailProxiesExactBytes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	raw := append([]byte("Message-ID: <raw-1@example.com>\r\nContent-Type: application/octet-stream\r\n\r\n"), 0x00, 0xff, '\r', '\n')
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Internal-Token"); got != "shared-secret" {
			t.Errorf("X-Internal-Token = %q, want shared-secret", got)
		}
		if got := r.URL.Path; got != "/internal/messages/raw-1/raw" {
			t.Errorf("upstream path = %q", got)
		}
		if got := r.URL.Query().Get("mailbox"); got != "box@example.com" {
			t.Errorf("mailbox = %q, want box@example.com", got)
		}
		w.Header().Set("Content-Type", "message/rfc822")
		w.Header().Set("Content-Disposition", `attachment; filename="message.eml"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(raw)
	}))
	defer upstream.Close()

	handler := newEmailHandlerForUpstream(strings.TrimPrefix(upstream.URL, "http://"))
	router := gin.New()
	router.GET("/emails/:message_id/raw", handler.GetRawEmail)

	response := httptest.NewRecorder()
	target := "/emails/raw-1/raw?mailbox=box%40example.com"
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.Bytes())
	}
	if !bytes.Equal(response.Body.Bytes(), raw) {
		t.Fatalf("raw response differs from upstream: got %x, want %x", response.Body.Bytes(), raw)
	}
	for header, want := range map[string]string{
		"Content-Type":           "message/rfc822",
		"Content-Disposition":    `attachment; filename="message.eml"`,
		"Content-Length":         strconv.Itoa(len(raw)),
		"Cache-Control":          "private, no-store",
		"X-Content-Type-Options": "nosniff",
	} {
		if got := response.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestAdminGetRawEmailFallsBackByDomain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	raw := []byte("Message-ID: <raw-admin@example.com>\r\n\r\nexact admin raw")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/internal/messages/raw-admin/raw" {
			t.Errorf("upstream path = %q", got)
		}
		if got := r.URL.Query().Get("mailbox"); got != "unregistered@example.com" {
			t.Errorf("mailbox = %q, want unregistered@example.com", got)
		}
		w.Header().Set("Content-Type", "message/rfc822")
		w.Header().Set("Content-Disposition", `attachment; filename="message.eml"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
		_, _ = w.Write(raw)
	}))
	defer upstream.Close()

	server := &model.MailServer{ID: 9, APIHost: strings.TrimPrefix(upstream.URL, "http://")}
	store := &emailStoreStub{
		mailboxByEmail: map[string]*model.MailboxAccount{},
		mailboxByOrder: map[string]*model.MailboxAccount{},
		servers:        map[uint64]*model.MailServer{},
		serverByDomain: map[string]*model.MailServer{"example.com": server},
	}
	handler := NewEmailHandler(store, newTestNodeTransport("shared-secret", upstream.Client()))
	router := gin.New()
	handler.RegisterAdminRoutes(router.Group("/api/v1/admin"))

	response := httptest.NewRecorder()
	target := "/api/v1/admin/emails/raw-admin/raw?mailbox=unregistered%40example.com"
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.Bytes())
	}
	if !bytes.Equal(response.Body.Bytes(), raw) {
		t.Fatalf("raw response differs from upstream: got %x, want %x", response.Body.Bytes(), raw)
	}
	if got := response.Header().Get("Content-Type"); got != "message/rfc822" {
		t.Fatalf("content type = %q, want message/rfc822", got)
	}
	if got := response.Header().Get("Content-Disposition"); got != `attachment; filename="message.eml"` {
		t.Fatalf("content disposition = %q", got)
	}
	if len(store.domainLookups) != 1 || store.domainLookups[0] != "example.com" {
		t.Fatalf("domain lookups = %#v", store.domainLookups)
	}
}

func TestGetRawEmailPassesUpstreamJSONError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":2003,"message":"message not found"}`))
	}))
	defer upstream.Close()

	handler := newEmailHandlerForUpstream(strings.TrimPrefix(upstream.URL, "http://"))
	router := gin.New()
	router.GET("/emails/:message_id/raw", handler.GetRawEmail)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/emails/missing/raw?mailbox=box%40example.com", nil))

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Code != 2003 {
		t.Fatalf("JSON error = %#v, decode error = %v", body, err)
	}
}

func TestGetEmailBodyRejectsMalformedUpstreamError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("missing"))
	}))
	defer upstream.Close()

	handler := newEmailHandlerForUpstream(strings.TrimPrefix(upstream.URL, "http://"))
	router := gin.New()
	router.GET("/emails/:message_id/body", handler.GetEmailBody)

	response := httptest.NewRecorder()
	target := "/emails/missing-message/body?mailbox=box%40example.com"
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body Response
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != ErrCodeExternalFail || !strings.Contains(body.Message, "upstream error: 404") || body.RequestID == "" {
		t.Fatalf("body = %#v", body)
	}
}

func TestWriteMailboxMessagesResponseRejectsMissingData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(http.MethodGet, "/messages", nil)

	writeMailboxMessagesResponse(context, []byte(`{"code":0}`), "box@example.com", "ORDER-42")

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body Response
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != ErrCodeExternalFail || !strings.Contains(body.Message, "data object is required") {
		t.Fatalf("body = %#v", body)
	}
}

func TestRegisterExternalEmailListRoutesRequiresEmailListPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/v1")
	registry := apiregistry.New("/api/v1")
	(&EmailHandler{}).RegisterExternalRoutes(registry, group)

	for _, target := range []string{
		"/api/v1/orders/ORDER-42/emails",
		"/api/v1/mailboxes/box%40example.com/messages",
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))

		if response.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, body = %s", target, response.Code, response.Body.String())
		}
		var body Response
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s response: %v", target, err)
		}
		if body.Code != ErrCodeInsufficientScope || !strings.Contains(body.Message, "required: email:list") {
			t.Fatalf("%s body = %#v", target, body)
		}
	}
}

func TestRegisterExternalRawEmailRouteRequiresRawPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/v1")
	registry := apiregistry.New("/api/v1")
	(&EmailHandler{}).RegisterExternalRoutes(registry, group)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/emails/raw-1/raw?mailbox=box%40example.com", nil))

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body Response
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != ErrCodeInsufficientScope || !strings.Contains(body.Message, "required: email:raw") {
		t.Fatalf("body = %#v", body)
	}
}
