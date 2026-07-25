package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ticket/email-mgmt-system/internal/config"
	"github.com/ticket/email-mgmt-system/internal/mailboxaddr"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/nodetransport"
	"github.com/ticket/email-mgmt-system/internal/store"
)

func ValidateMailboxLocalPart(value string) error {
	return mailboxaddr.ValidateLocalPart(value)
}

func ValidateMailboxPassword(value string) error {
	return mailboxaddr.ValidatePassword(value)
}

type MailboxCreateInput struct {
	OrderID       string
	LocalPart     string
	Password      string
	DomainID      uint64
	ServerID      uint64
	RetentionDays int
	AllowExisting bool
}

type MailboxCreateResult struct {
	MailboxAccountID uint64     `json:"mailbox_account_id"`
	OrderID          string     `json:"order_id"`
	EmailAddress     string     `json:"email_address"`
	LocalPart        string     `json:"local_part"`
	Domain           string     `json:"domain"`
	Password         string     `json:"password,omitempty"`
	ServerID         uint64     `json:"server_id"`
	CreatedAt        time.Time  `json:"created_at"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	SyncStatus       string     `json:"sync_status"`
	IsExisting       bool       `json:"is_existing"`
}

type MailboxCreator struct {
	store     *store.Store
	config    *config.Config
	transport nodetransport.NodeTransport
}

func NewMailboxCreator(s *store.Store, cfg *config.Config, transport nodetransport.NodeTransport) *MailboxCreator {
	return &MailboxCreator{
		store:     s,
		config:    cfg,
		transport: transport,
	}
}

func (m *MailboxCreator) Create(input MailboxCreateInput) (*MailboxCreateResult, error) {
	input.OrderID = strings.TrimSpace(input.OrderID)
	if input.OrderID == "" {
		return nil, fmt.Errorf("order_id is required")
	}

	if existing, err := m.store.GetMailboxByOrderID(input.OrderID); err == nil {
		if input.AllowExisting {
			return mailboxResult(existing, input.OrderID, true), nil
		}
		return nil, fmt.Errorf("mailbox already exists for prefix: %s", input.OrderID)
	}

	localPart := input.LocalPart
	if localPart == "" {
		localPart = sanitizeLocalPart(input.OrderID)
	}
	if localPart == "" {
		return nil, fmt.Errorf("local_part is required")
	}
	if err := ValidateMailboxLocalPart(localPart); err != nil {
		return nil, err
	}

	password := input.Password
	if password == "" {
		password = generatePassword()
	}
	if err := ValidateMailboxPassword(password); err != nil {
		return nil, err
	}

	retentionDays := input.RetentionDays
	if retentionDays <= 0 {
		retentionDays = m.config.DefaultRetentionDays
	}

	domain, err := m.selectDomain(input.DomainID)
	if err != nil {
		return nil, err
	}
	domainName, err := mailboxaddr.NormalizeDomain(domain.Name)
	if err != nil {
		return nil, fmt.Errorf("invalid selected domain: %w", err)
	}
	emailAddress := localPart + "@" + domainName

	if existing, err := m.store.GetMailboxByEmail(emailAddress); err == nil {
		return nil, fmt.Errorf("email already exists: %s", existing.EmailAddress)
	}

	srv, err := m.selectServer(input.ServerID, domain.ID)
	if err != nil {
		return nil, err
	}

	if err := m.createRemote(srv, emailAddress, password); err != nil {
		return nil, fmt.Errorf("create remote mailbox: %w", err)
	}

	now := time.Now()
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
		ExpiresAt:     nil,
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

	domain, err := m.store.GetAllocatableDomain()
	if err != nil {
		return nil, fmt.Errorf("no active domain available with healthy synced server")
	}
	return domain, nil
}

func (m *MailboxCreator) selectServer(serverID, domainID uint64) (*model.MailServer, error) {
	// 指定服务器：校验健康；指定域名时还须校验该服务器已绑定该域且 Postfix 已同步，
	// 否则 mgmt 落库但远端 Postfix 不收该域 → 投递失败。
	if serverID > 0 {
		srv, err := m.store.GetServer(serverID)
		if err != nil {
			return nil, fmt.Errorf("server not found")
		}
		if !srv.IsAllocatableState(time.Now()) {
			return nil, fmt.Errorf("server is not allocatable")
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

func (m *MailboxCreator) createRemote(server *model.MailServer, email, password string) error {
	resp, err := m.transport.Execute(context.Background(), nodetransport.Target{
		NodeID: server.ID, APIHost: server.APIHost, TransportMode: server.TransportMode,
	}, nodetransport.MailboxCreate(email, password))
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("upstream error: %d - %s", resp.StatusCode, string(resp.Body))
	}
	return nil
}

func mailboxResult(mb *model.MailboxAccount, orderID string, existing bool) *MailboxCreateResult {
	return &MailboxCreateResult{
		MailboxAccountID: mb.ID,
		OrderID:          orderID,
		EmailAddress:     mb.EmailAddress,
		LocalPart:        mb.LocalPart,
		Domain:           mb.Domain.Name,
		Password:         mb.Password,
		ServerID:         mb.ServerID,
		CreatedAt:        mb.CreatedAt,
		ExpiresAt:        mb.ExpiresAt,
		SyncStatus:       mb.SyncStatus,
		IsExisting:       existing,
	}
}

func generatePassword() string {
	return fmt.Sprintf("%x-%s", time.Now().UnixNano(), uuid.New().String()[:4])[:16]
}
