package handler

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ticket/email-mail-node/internal/domain"
	"github.com/ticket/email-mail-node/internal/mailbox"
	nodecontract "github.com/ticket/email-node-contract"
	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
)

func TestExecuteControlCommandCreatesMailboxIdempotently(t *testing.T) {
	root := t.TempDir()
	usersFile := filepath.Join(root, "dovecot", "users.conf")
	vmailboxFile := filepath.Join(root, "postfix", "vmailbox")
	if err := os.MkdirAll(filepath.Dir(usersFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(vmailboxFile), 0o755); err != nil {
		t.Fatal(err)
	}
	manager := mailbox.NewManagerWithFiles(filepath.Join(root, "maildir"), 1000, 1000, usersFile, vmailboxFile)
	handler := NewNodeHandler(manager, nil, nil, nil, 7, "node-7", "", "", nil)
	command := &nodev1.Command{
		CommandId: "command-1", Sequence: 1, Type: string(nodecontract.CommandMailboxCreate), SchemaVersion: 1,
		IdempotencyKey: "mailbox:create:a@example.com",
		PayloadJson:    []byte(`{"email_address":"a@example.com","password":"secret123"}`),
	}
	for attempt := 0; attempt < 2; attempt++ {
		result := handler.ExecuteControlCommand(context.Background(), command)
		if result.State != nodev1.CommandState_COMMAND_STATE_SUCCEEDED || result.ResultCode != "http.201" {
			t.Fatalf("attempt %d result = %#v", attempt, result)
		}
		var envelope nodecontract.CommandResponse
		if err := json.Unmarshal(result.ResultJSON, &envelope); err != nil || envelope.StatusCode != 201 {
			t.Fatalf("attempt %d envelope = %#v, %v", attempt, envelope, err)
		}
	}
	users, _ := os.ReadFile(usersFile)
	vmailboxes, _ := os.ReadFile(vmailboxFile)
	if strings.Count(string(users), "a@example.com:") != 1 || strings.Count(string(vmailboxes), "a@example.com ") != 1 {
		t.Fatalf("duplicate config entries: users=%q vmailboxes=%q", users, vmailboxes)
	}
}

func TestExecuteControlCommandMapsDomainPartialToWarning(t *testing.T) {
	root := t.TempDir()
	virtualDomains := filepath.Join(root, "postfix", "virtual_domains")
	vmailboxFile := filepath.Join(root, "postfix", "vmailbox")
	if err := os.MkdirAll(filepath.Dir(virtualDomains), 0o755); err != nil {
		t.Fatal(err)
	}
	domainManager := domain.NewManager(domain.Config{
		PublicHost: "mail.example.com", VirtualDomainsFile: virtualDomains, VmailboxFile: vmailboxFile,
		EnableDKIMProvision: false,
	})
	handler := NewNodeHandler(nil, domainManager, nil, nil, 7, "node-7", "", "", nil)
	result := handler.ExecuteControlCommand(context.Background(), &nodev1.Command{
		CommandId: "command-2", Sequence: 2, Type: string(nodecontract.CommandDomainApply), SchemaVersion: 1,
		IdempotencyKey: "domain:apply:example.com", PayloadJson: []byte(`{"domain":"example.com"}`),
	})
	if result.State != nodev1.CommandState_COMMAND_STATE_SUCCEEDED_WITH_WARNING || result.ResultCode != "http.200" {
		t.Fatalf("domain result = %#v", result)
	}
	var envelope nodecontract.CommandResponse
	if err := json.Unmarshal(result.ResultJSON, &envelope); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(envelope.Body), `"dkim_status":"sync_failed"`) {
		t.Fatalf("domain body = %s", envelope.Body)
	}
}
