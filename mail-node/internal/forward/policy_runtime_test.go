package forward

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	filtercontract "github.com/ticket/email-filter-contract"
	"github.com/ticket/email-mail-node/internal/config"
	"github.com/ticket/email-mail-node/internal/filter"
	"github.com/ticket/email-mail-node/internal/filterdecision"
	"github.com/ticket/email-mail-node/internal/filteroutbox"
	"github.com/ticket/email-mail-node/internal/filterquarantine"
	"github.com/ticket/email-mail-node/internal/mailbox"
)

func TestDualShadowRecordsDecisionWithoutChangingLegacyBlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"configs": map[string]string{filterdecision.EngineModeConfigKey: filterdecision.EngineModeDualShadow},
			"sources": map[string]string{}, "desired_revision": 1,
		}})
	}))
	defer server.Close()
	remote := config.NewRemoteConfig(server.URL, "secret", 7)
	if err := remote.PullAll(); err != nil {
		t.Fatal(err)
	}

	base := t.TempDir()
	newDir := filepath.Join(base, "example.com", "inbox", "new")
	if err := os.MkdirAll(newDir, 0700); err != nil {
		t.Fatal(err)
	}
	messagePath := filepath.Join(newDir, "fixture")
	message := "From: sender@example.com\r\nTo: inbox@example.com\r\nSubject: ordinary\r\nMessage-ID: <fixture@example.com>\r\n\r\nbody"
	if err := os.WriteFile(messagePath, []byte(message), 0600); err != nil {
		t.Fatal(err)
	}
	outboxRoot := filepath.Join(t.TempDir(), "outbox")
	queue, err := filteroutbox.New(outboxRoot, 10, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	service := New(ForwardConfig{}, filter.New(filter.ActionBlock, ""), mailbox.NewManager(base, 0, 0), remote)
	service.ConfigurePolicyRuntime(filterdecision.New(), queue)
	if err := service.processFile(messagePath, "inbox@example.com"); err != nil {
		t.Fatal(err)
	}
	curEntries, err := os.ReadDir(filepath.Join(base, "example.com", "inbox", "cur"))
	if err != nil || len(curEntries) != 1 {
		t.Fatalf("legacy block did not move message to cur: entries=%d err=%v", len(curEntries), err)
	}
	if _, err := os.Stat(messagePath); !os.IsNotExist(err) {
		t.Fatalf("message remains in new: %v", err)
	}
	staged, ready, _, err := queue.Pending()
	if err != nil || staged != 0 || ready != 1 {
		t.Fatalf("outbox pending = %d/%d err=%v", staged, ready, err)
	}
	files, err := filepath.Glob(filepath.Join(outboxRoot, "ready", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("ready files = %#v / %v", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var event filtercontract.OutboxEvent
	if err := filtercontract.DecodeStrict(data, &event); err != nil {
		t.Fatal(err)
	}
	if event.Decision.FinalAction != filtercontract.ActionAllow || event.Result == nil ||
		event.Result.AttemptedAction != filtercontract.ActionQuarantine || event.Result.ActualAction != filtercontract.ActionQuarantine {
		t.Fatalf("shadow event = %#v", event)
	}
}

func TestDualFilterQuarantineMovesOriginalOutsideMaildir(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"configs": map[string]string{filterdecision.EngineModeConfigKey: filterdecision.EngineModeDualFilter},
			"sources": map[string]string{}, "desired_revision": 1,
		}})
	}))
	defer server.Close()
	remote := config.NewRemoteConfig(server.URL, "secret", 7)
	if err := remote.PullAll(); err != nil {
		t.Fatal(err)
	}

	decisionEngine := filterdecision.New()
	bundle := filtercontract.ManualBundle{
		SchemaVersion: filtercontract.SchemaVersionV1, PolicyKind: filtercontract.PolicyManual, Revision: 1,
		Rules: []filtercontract.ManualRule{{
			LogicalID: "manual-quarantine", Name: "Manual quarantine", ScopeType: "global",
			Action: filtercontract.ActionQuarantine, Priority: 1, Mode: filtercontract.ModeEnforce, Source: "manual",
			Conditions: []filtercontract.Condition{{Field: "subject", Operator: "contains", Value: filtercontract.StringValue("isolated"), Position: 0}},
		}},
	}
	checksum, err := bundle.CalculatedChecksum()
	if err != nil {
		t.Fatal(err)
	}
	bundle.Checksum = checksum
	if err := decisionEngine.ApplyManual(bundle); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	maildir := filepath.Join(root, "maildir")
	newDir := filepath.Join(maildir, "example.com", "inbox", "new")
	if err := os.MkdirAll(newDir, 0o700); err != nil {
		t.Fatal(err)
	}
	messagePath := filepath.Join(newDir, "fixture")
	message := "From: sender@example.com\r\nTo: inbox@example.com\r\nSubject: isolated\r\nMessage-ID: <isolated@example.com>\r\n\r\nbody"
	if err := os.WriteFile(messagePath, []byte(message), 0o600); err != nil {
		t.Fatal(err)
	}
	queue, err := filteroutbox.New(filepath.Join(root, "outbox"), 10, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	quarantine, err := filterquarantine.New(filepath.Join(root, "quarantine"), maildir)
	if err != nil {
		t.Fatal(err)
	}
	service := New(ForwardConfig{}, filter.New(filter.ActionPass, ""), mailbox.NewManager(maildir, 0, 0), remote)
	service.ConfigurePolicyRuntime(decisionEngine, queue)
	service.ConfigureQuarantine(quarantine)
	if err := service.processFile(messagePath, "inbox@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(messagePath); !os.IsNotExist(err) {
		t.Fatalf("message remains in Maildir new: %v", err)
	}
	if entries, err := os.ReadDir(filepath.Join(maildir, "example.com", "inbox", "cur")); err == nil && len(entries) != 0 {
		t.Fatalf("quarantined message visible in cur: %v", entries)
	}
	files, err := filepath.Glob(filepath.Join(root, "outbox", "ready", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("ready files = %#v / %v", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var event filtercontract.OutboxEvent
	if err := filtercontract.DecodeStrict(data, &event); err != nil {
		t.Fatal(err)
	}
	if event.Result == nil || event.Result.QuarantineKey == "" || event.Result.ActualAction != filtercontract.ActionQuarantine {
		t.Fatalf("quarantine result = %#v", event.Result)
	}
	if _, _, err := quarantine.MessagePath(event.Result.QuarantineKey); err != nil {
		t.Fatalf("quarantined original missing: %v", err)
	}
}
