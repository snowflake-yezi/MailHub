package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestIssueAPICredential(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	first, err := IssueAPICredential(42, "生产凭证", &expiresAt)
	if err != nil {
		t.Fatalf("IssueAPICredential: %v", err)
	}
	second, err := IssueAPICredential(42, "轮换凭证", nil)
	if err != nil {
		t.Fatalf("IssueAPICredential second: %v", err)
	}
	if !strings.HasPrefix(first.Token, "mh_live_") {
		t.Fatalf("token prefix = %q", first.Token)
	}
	if first.Token == second.Token {
		t.Fatal("two issued credentials returned the same token")
	}
	if first.Credential.TokenHash != HashAPIToken(first.Token) {
		t.Fatal("persisted token hash does not match the issued token")
	}
	if first.Credential.TokenPrefix != first.Token[:16] {
		t.Fatalf("display prefix = %q, want %q", first.Credential.TokenPrefix, first.Token[:16])
	}
	if first.Credential.ApplicationID != 42 || !first.Credential.Enabled {
		t.Fatalf("unexpected credential: %+v", first.Credential)
	}
	if first.Credential.ExpiresAt == nil || !first.Credential.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("expires_at = %v, want %v", first.Credential.ExpiresAt, expiresAt)
	}

	encoded, err := json.Marshal(first.Credential)
	if err != nil {
		t.Fatalf("marshal credential: %v", err)
	}
	if strings.Contains(string(encoded), first.Credential.TokenHash) || strings.Contains(string(encoded), first.Token) {
		t.Fatalf("credential JSON leaked secret material: %s", encoded)
	}
}
