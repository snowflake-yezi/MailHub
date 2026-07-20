package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
	emailLookups   []string
	orderLookups   []string
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

func (s *emailStoreStub) FindServerByEmailDomain(string) (*model.MailServer, error) {
	return nil, errors.New("server not found")
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
	handler := NewEmailHandler(store, "shared-secret")
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
	handler := NewEmailHandler(store, "shared-secret")
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
