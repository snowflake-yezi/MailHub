package service

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ticket/email-mgmt-system/internal/config"
	"github.com/ticket/email-mgmt-system/internal/store"
)

// Allocator keeps the existing external API surface while delegating creation
// to the unified mailbox creator.
type Allocator struct {
	creator *MailboxCreator
}

func NewAllocator(s *store.Store, cfg *config.Config, sharedSecret string) *Allocator {
	return &Allocator{creator: NewMailboxCreator(s, cfg, sharedSecret)}
}

func (a *Allocator) Creator() *MailboxCreator {
	return a.creator
}

type AllocateResult struct {
	OrderID      string    `json:"order_id"`
	EmailAddress string    `json:"email_address"`
	LocalPart    string    `json:"local_part"`
	Domain       string    `json:"domain"`
	Password     string    `json:"password,omitempty"`
	ServerID     uint64    `json:"server_id"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	SyncStatus   string    `json:"sync_status"`
	IsExisting   bool      `json:"is_existing"`
}

func (a *Allocator) Allocate(orderID string, domainID uint64, retentionDays int) (*AllocateResult, error) {
	result, err := a.creator.Create(MailboxCreateInput{
		OrderID:       orderID,
		DomainID:      domainID,
		RetentionDays: retentionDays,
	})
	if err != nil {
		return nil, err
	}

	return &AllocateResult{
		OrderID:      result.OrderID,
		EmailAddress: result.EmailAddress,
		LocalPart:    result.LocalPart,
		Domain:       result.Domain,
		Password:     result.Password,
		ServerID:     result.ServerID,
		CreatedAt:    result.CreatedAt,
		ExpiresAt:    result.ExpiresAt,
		SyncStatus:   result.SyncStatus,
		IsExisting:   result.IsExisting,
	}, nil
}

func sanitizeLocalPart(orderID string) string {
	s := strings.ToLower(orderID)
	reg := regexp.MustCompile(`[^a-z0-9]`)
	s = reg.ReplaceAllString(s, "-")
	multiDash := regexp.MustCompile(`-+`)
	s = multiDash.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 64 {
		s = s[:56] + "-" + hashSuffix(orderID)
	}
	return s
}

func hashSuffix(s string) string {
	h := 0
	for _, c := range s {
		h = h*31 + int(c)
	}
	return fmt.Sprintf("%08x", uint32(h)&0xFFFFFFFF)
}
