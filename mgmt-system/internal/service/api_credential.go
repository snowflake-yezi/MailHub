package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/ticket/email-mgmt-system/internal/model"
)

const apiTokenPrefix = "mh_live_"

// IssuedAPICredential contains the one-time secret and its persistable record.
type IssuedAPICredential struct {
	Credential model.APICredential
	Token      string
}

func IssueAPICredential(applicationID uint64, name string, expiresAt *time.Time) (*IssuedAPICredential, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return nil, fmt.Errorf("generate API credential: %w", err)
	}
	token := apiTokenPrefix + base64.RawURLEncoding.EncodeToString(random)
	return &IssuedAPICredential{
		Credential: model.APICredential{
			ApplicationID: applicationID,
			Name:          strings.TrimSpace(name),
			TokenPrefix:   token[:16],
			TokenHash:     HashAPIToken(token),
			Enabled:       true,
			ExpiresAt:     expiresAt,
		},
		Token: token,
	}, nil
}

func HashAPIToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
