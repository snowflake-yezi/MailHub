package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ticket/email-mgmt-system/internal/config"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/store"
)

type MailboxCreateInput struct {
	OrderID       string
	LocalPart     string
	Password      string
	DomainID      uint64
	ServerID      uint64
	RetentionDays int
}

type MailboxCreateResult struct {
	MailboxAccountID uint64    `json:"mailbox_account_id"`
	OrderID          string    `json:"order_id"`
	EmailAddress     string    `json:"email_address"`
	LocalPart        string    `json:"local_part"`
	Domain           string    `json:"domain"`
	Password         string    `json:"password,omitempty"`
	ServerID         uint64    `json:"server_id"`
	CreatedAt        time.Time `json:"created_at"`
	ExpiresAt        time.Time `json:"expires_at"`
	SyncStatus       string    `json:"sync_status"`
	IsExisting       bool      `json:"is_existing"`
}

type MailboxCreator struct {
	store        *store.Store
	config       *config.Config
	client       *http.Client
	sharedSecret string
}

func NewMailboxCreator(s *store.Store, cfg *config.Config, sharedSecret string) *MailboxCreator {
	return &MailboxCreator{
		store:        s,
		config:       cfg,
		client:       &http.Client{Timeout: 10 * time.Second},
		sharedSecret: sharedSecret,
	}
}

func (m *MailboxCreator) Create(input MailboxCreateInput) (*MailboxCreateResult, error) {
	input.OrderID = strings.TrimSpace(input.OrderID)
	input.LocalPart = strings.TrimSpace(input.LocalPart)
	input.Password = strings.TrimSpace(input.Password)
	if input.OrderID == "" {
		return nil, fmt.Errorf("order_id is required")
	}

	if existing, err := m.store.GetMailboxByOrderID(input.OrderID); err == nil {
		return mailboxResult(existing, input.OrderID, true), nil
	}

	localPart := input.LocalPart
	if localPart == "" {
		localPart = sanitizeLocalPart(input.OrderID)
	}
	if localPart == "" {
		return nil, fmt.Errorf("local_part is required")
	}

	password := input.Password
	if password == "" {
		password = generatePassword()
	}

	retentionDays := input.RetentionDays
	if retentionDays <= 0 {
		retentionDays = m.config.DefaultRetentionDays
	}

	domain, err := m.selectDomain(input.DomainID)
	if err != nil {
		return nil, err
	}
	emailAddress := localPart + "@" + domain.Name

	if existing, err := m.store.GetMailboxByEmail(emailAddress); err == nil {
		return nil, fmt.Errorf("email already exists: %s", existing.EmailAddress)
	}

	srv, err := m.selectServer(input.ServerID, domain.ID)
	if err != nil {
		return nil, err
	}

	if err := m.createRemote(srv.APIHost, emailAddress, password); err != nil {
		return nil, fmt.Errorf("create remote mailbox: %w", err)
	}

	now := time.Now()
	expiresAt := now.Add(time.Duration(retentionDays) * 24 * time.Hour)
	account := &model.MailboxAccount{
		EmailAddress:  emailAddress,
		LocalPart:     localPart,
		Password:      password,
		DomainID:      domain.ID,
		ServerID:      srv.ID,
		Status:        "active",
		SyncStatus:    "synced",
		RetentionDays: retentionDays,
		SyncedAt:      &now,
		ExpiresAt:     &expiresAt,
	}
	if err := m.store.CreateMailboxAccountWithOrder(account, input.OrderID); err != nil {
		return nil, fmt.Errorf("create mailbox record: %w", err)
	}

	if err := m.store.IncrementServerLoad(srv.ID); err != nil {
		return nil, fmt.Errorf("increment server load: %w", err)
	}

	account.Domain = *domain
	account.Server = *srv
	return mailboxResult(account, input.OrderID, false), nil
}

func (m *MailboxCreator) selectDomain(domainID uint64) (*model.Domain, error) {
	if domainID > 0 {
		domain, err := m.store.GetDomainByID(domainID)
		if err != nil {
			return nil, fmt.Errorf("domain not found")
		}
		if domain.Status != "active" {
			return nil, fmt.Errorf("domain is not active")
		}
		return domain, nil
	}

	domains, err := m.store.ListDomains()
	if err != nil || len(domains) == 0 {
		return nil, fmt.Errorf("no active domain available")
	}
	return &domains[0], nil
}

func (m *MailboxCreator) selectServer(serverID, domainID uint64) (*model.MailServer, error) {
	// 指定服务器：校验健康；指定域名时还须校验该服务器已绑定该域且 Postfix 已同步，
	// 否则 mgmt 落库但远端 Postfix 不收该域 → 投递失败。
	if serverID > 0 {
		srv, err := m.store.GetServer(serverID)
		if err != nil {
			return nil, fmt.Errorf("server not found")
		}
		if srv.Status != "healthy" {
			return nil, fmt.Errorf("server is not healthy")
		}
		if domainID > 0 {
			sd, err := m.store.GetServerDomain(serverID, domainID)
			if err != nil || sd.Status != "active" {
				return nil, fmt.Errorf("server does not serve this domain")
			}
			if sd.PostfixStatus != "synced" {
				return nil, fmt.Errorf("domain not ready on server (postfix_status=%s)", sd.PostfixStatus)
			}
		}
		return srv, nil
	}

	// 未指定服务器：域名感知分配——在该域已同步 Postfix 的健康服务器中选最闲一台。
	if domainID > 0 {
		srv, err := m.store.GetHealthyServerForDomain(domainID)
		if err != nil {
			return nil, fmt.Errorf("no healthy server synced for domain: %w", err)
		}
		return srv, nil
	}

	srv, err := m.store.GetHealthyServerWithMinLoad()
	if err != nil {
		return nil, fmt.Errorf("no available mail server: %w", err)
	}
	return srv, nil
}

func (m *MailboxCreator) createRemote(apiHost, email, password string) error {
	body, err := json.Marshal(map[string]string{
		"email_address": email,
		"password":      password,
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("http://%s/internal/mailboxes", apiHost)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", m.sharedSecret)

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	data, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return fmt.Errorf("read response: %w", readErr)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("upstream error: %d - %s", resp.StatusCode, string(data))
	}
	return nil
}

func mailboxResult(mb *model.MailboxAccount, orderID string, existing bool) *MailboxCreateResult {
	var expiresAt time.Time
	if mb.ExpiresAt != nil {
		expiresAt = *mb.ExpiresAt
	}
	return &MailboxCreateResult{
		MailboxAccountID: mb.ID,
		OrderID:          orderID,
		EmailAddress:     mb.EmailAddress,
		LocalPart:        mb.LocalPart,
		Domain:           mb.Domain.Name,
		Password:         mb.Password,
		ServerID:         mb.ServerID,
		CreatedAt:        mb.CreatedAt,
		ExpiresAt:        expiresAt,
		SyncStatus:       mb.SyncStatus,
		IsExisting:       existing,
	}
}

func generatePassword() string {
	return fmt.Sprintf("%x-%s", time.Now().UnixNano(), uuid.New().String()[:4])[:16]
}
