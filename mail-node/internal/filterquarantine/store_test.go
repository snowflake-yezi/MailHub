package filterquarantine

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	filtercontract "github.com/ticket/email-filter-contract"
)

func TestRejectsQuarantineInsideMaildir(t *testing.T) {
	root := t.TempDir()
	maildir := filepath.Join(root, "maildir")
	if err := os.MkdirAll(maildir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := New(filepath.Join(maildir, "quarantine"), maildir); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("New() error = %v, want ErrInvalidPath", err)
	}
}

func TestConfiguredSymlinkParentUsesPhysicalPaths(t *testing.T) {
	root := t.TempDir()
	physicalParent := filepath.Join(root, "spool", "mail")
	physicalMaildir := filepath.Join(physicalParent, "vhosts")
	sourceDir := filepath.Join(physicalMaildir, "example.com", "user", "new")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configuredParent := filepath.Join(root, "var-mail")
	if err := os.Symlink(physicalParent, configuredParent); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	store, err := New(filepath.Join(configuredParent, "mailhub-quarantine"), filepath.Join(configuredParent, "vhosts"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if store.root != filepath.Join(physicalParent, "mailhub-quarantine") || store.maildirBase != physicalMaildir {
		t.Fatalf("physical roots = %q, %q", store.root, store.maildirBase)
	}
	source := filepath.Join(configuredParent, "vhosts", "example.com", "user", "new", "message")
	if err := os.WriteFile(source, []byte("message"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Quarantine(source, "user@example.com", "message-id", "decision-symlink-parent", time.Now()); err != nil {
		t.Fatalf("Quarantine() error = %v", err)
	}
	if _, err := New(filepath.Join(configuredParent, "vhosts", "quarantine"), filepath.Join(configuredParent, "vhosts")); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("New() inside physical Maildir error = %v, want ErrInvalidPath", err)
	}
}

func TestQuarantineRejectsSymlinkSource(t *testing.T) {
	root := t.TempDir()
	maildir := filepath.Join(root, "maildir")
	sourceDir := filepath.Join(maildir, "example.com", "user", "new")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.eml")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(sourceDir, "linked-message")
	if err := os.Symlink(outside, source); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	store, err := New(filepath.Join(root, "quarantine"), maildir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Quarantine(source, "user@example.com", "linked-id", "decision-linked", time.Now()); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Quarantine() error = %v, want ErrInvalidPath", err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "outside" {
		t.Fatalf("outside source changed: data=%q err=%v", data, err)
	}
}

func TestQuarantineReleaseIsIdempotentAndHiddenFromMaildir(t *testing.T) {
	root := t.TempDir()
	maildir := filepath.Join(root, "maildir")
	quarantine := filepath.Join(root, "quarantine")
	sourceDir := filepath.Join(maildir, "example.com", "user", "new")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(sourceDir, "message-key")
	if err := os.WriteFile(source, []byte("Message-ID: <one@example.com>\r\n\r\nbody"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := New(quarantine, maildir)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := store.Quarantine(source, "user@example.com", "<one@example.com>", "decision-1", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source still visible in Maildir: %v", err)
	}
	deliveries := 0
	deliver := func(path, mailbox string) (string, error) {
		deliveries++
		if mailbox != "user@example.com" {
			t.Fatalf("mailbox = %q", mailbox)
		}
		return "union@example.net", nil
	}
	receipt, err := store.Release(metadata.QuarantineKey, "operation-1", deliver)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != filtercontract.ReleaseStatusReleased || !receipt.SMTPDelivered || !receipt.RestoredToCur {
		t.Fatalf("receipt = %#v", receipt)
	}
	if _, err := store.Release(metadata.QuarantineKey, "operation-1", deliver); err != nil {
		t.Fatal(err)
	}
	if deliveries != 1 {
		t.Fatalf("deliveries = %d, want 1", deliveries)
	}
	restoredName := "message-key"
	if runtime.GOOS != "windows" {
		restoredName += ":2,S"
	}
	if _, err := os.Stat(filepath.Join(maildir, "example.com", "user", "cur", restoredName)); err != nil {
		t.Fatalf("restored message missing: %v", err)
	}
}

func TestPurgeExpiredKeepsMetadataForReconciliation(t *testing.T) {
	root := t.TempDir()
	maildir := filepath.Join(root, "maildir")
	sourceDir := filepath.Join(maildir, "example.com", "user", "new")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(sourceDir, "old")
	if err := os.WriteFile(source, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := New(filepath.Join(root, "quarantine"), maildir)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := store.Quarantine(source, "user@example.com", "old-id", "decision-old", time.Now().Add(-48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := store.PurgeExpired(time.Now().Add(-24 * time.Hour))
	if err != nil || len(keys) != 1 || keys[0] != metadata.QuarantineKey {
		t.Fatalf("PurgeExpired() = %v, %v", keys, err)
	}
	loaded, err := store.Metadata(metadata.QuarantineKey)
	if err != nil || loaded.ExpiredAt.IsZero() {
		t.Fatalf("metadata = %#v, err=%v", loaded, err)
	}
	if _, _, err := store.MessagePath(metadata.QuarantineKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("MessagePath() error = %v, want ErrNotFound", err)
	}
}

func TestReleaseRecoversAfterRestoreBeforeReceiptCommit(t *testing.T) {
	root := t.TempDir()
	maildir := filepath.Join(root, "maildir")
	sourceDir := filepath.Join(maildir, "example.com", "user", "new")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(sourceDir, "crash-window")
	if err := os.WriteFile(source, []byte("message"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := New(filepath.Join(root, "quarantine"), maildir)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := store.Quarantine(source, "user@example.com", "crash-id", "decision-crash", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	receipt := &filtercontract.ReleaseReceipt{
		SchemaVersion: filtercontract.SchemaVersionV1, OperationID: "operation-crash",
		QuarantineKey: metadata.QuarantineKey, DecisionKey: metadata.DecisionKey,
		Status: filtercontract.ReleaseStatusReleasing, SMTPDelivered: true, ForwardTarget: "union@example.net",
	}
	if err := store.writeReceipt(metadata.QuarantineKey, receipt); err != nil {
		t.Fatal(err)
	}
	destination, err := store.restoreDestination(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := moveFileDurable(filepath.Join(store.itemDirectory(metadata.QuarantineKey), "message.eml"), destination); err != nil {
		t.Fatal(err)
	}
	deliveries := 0
	recovered, err := store.Release(metadata.QuarantineKey, "operation-crash", func(_, _ string) (string, error) {
		deliveries++
		return "", nil
	})
	if err != nil || recovered.Status != filtercontract.ReleaseStatusReleased || !recovered.RestoredToCur {
		t.Fatalf("Release() = %#v, %v", recovered, err)
	}
	if deliveries != 0 {
		t.Fatalf("deliveries = %d, want 0", deliveries)
	}
	keys, err := store.PurgeExpired(time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Fatalf("PurgeExpired() expired released keys: %v", keys)
	}
	loaded, err := store.Metadata(metadata.QuarantineKey)
	if err != nil || !loaded.ExpiredAt.IsZero() {
		t.Fatalf("released metadata marked expired: %#v, err=%v", loaded, err)
	}
}
