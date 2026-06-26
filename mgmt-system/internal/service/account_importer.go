package service

import (
	"bufio"
	"fmt"
	"strings"
	"time"

	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/store"
)

type AccountImportResult struct {
	Imported int      `json:"imported"`
	Skipped  int      `json:"skipped"`
	Errors   []string `json:"errors,omitempty"`
}

type AccountImporter struct {
	store *store.Store
}

func NewAccountImporter(s *store.Store) *AccountImporter {
	return &AccountImporter{store: s}
}

func (i *AccountImporter) ImportDovecotUsers(serverID uint64, content string, defaultRetentionDays int) (*AccountImportResult, error) {
	if serverID == 0 {
		return nil, fmt.Errorf("server_id is required")
	}
	if defaultRetentionDays <= 0 {
		defaultRetentionDays = 30
	}

	result := &AccountImportResult{}
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		email, password, ok := parseDovecotUserLine(line)
		if !ok {
			result.Skipped++
			continue
		}

		localPart, domainName, ok := strings.Cut(email, "@")
		if !ok || localPart == "" || domainName == "" {
			result.Skipped++
			continue
		}

		domain, err := i.store.GetDomainByName(domainName)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: domain not found", email))
			continue
		}

		now := time.Now()
		account := &model.MailboxAccount{
			EmailAddress:  email,
			LocalPart:     localPart,
			Password:      password,
			DomainID:      domain.ID,
			ServerID:      serverID,
			Status:        "active",
			SyncStatus:    "synced",
			RetentionDays: defaultRetentionDays,
			SyncedAt:      &now,
		}
		if err := i.store.UpsertMailboxAccount(account); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", email, err))
			continue
		}
		result.Imported++
	}
	if err := scanner.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func parseDovecotUserLine(line string) (email, password string, ok bool) {
	parts := strings.Split(line, ":")
	if len(parts) < 2 {
		return "", "", false
	}

	email = strings.TrimSpace(parts[0])
	secret := strings.TrimSpace(parts[1])
	if email == "" || secret == "" {
		return "", "", false
	}

	const plainPrefix = "{PLAIN}"
	if strings.HasPrefix(secret, plainPrefix) {
		return email, strings.TrimPrefix(secret, plainPrefix), true
	}
	return email, secret, true
}
