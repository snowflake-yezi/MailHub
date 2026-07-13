package forward

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mailconfig "github.com/ticket/email-mail-node/internal/config"
	"github.com/ticket/email-mail-node/internal/mailbox"
)

func TestPurgeExpiredTrashUsesReloadedRetention(t *testing.T) {
	retention := "48"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"configs":{"lifecycle.trash_retention_hours":"` + retention + `"},"sources":{"lifecycle.trash_retention_hours":"global"}}}`))
	}))
	defer server.Close()

	remoteCfg := mailconfig.NewRemoteConfig(server.URL, "secret")
	if err := remoteCfg.PullAll(); err != nil {
		t.Fatalf("initial PullAll() error: %v", err)
	}

	base := t.TempDir()
	usersFile, vmailboxFile := setupConfigFiles(t)
	mgr := newTestManager(t, base, usersFile, vmailboxFile)
	lifecycle := NewLifecycle(mgr, &Service{}, 24*time.Hour, 0, 0, 0, remoteCfg)
	trashDir := filepath.Join(base, ".trash", trashDirName("example.com", "alice", time.Now().Add(-30*time.Hour).Unix()))
	mustMkdir(t, trashDir)

	lifecycle.purgeExpiredTrash()
	if _, err := os.Stat(trashDir); err != nil {
		t.Fatalf("30h trash removed with 48h retention: %v", err)
	}

	retention = "24"
	if err := remoteCfg.PullAll(); err != nil {
		t.Fatalf("reload PullAll() error: %v", err)
	}
	lifecycle.purgeExpiredTrash()
	if _, err := os.Stat(trashDir); !os.IsNotExist(err) {
		t.Fatalf("30h trash still exists with reloaded 24h retention: %v", err)
	}
}

func TestRestoreFromTrashRestoresNewestDomainScopedMailbox(t *testing.T) {
	base := t.TempDir()
	usersFile, vmailboxFile := setupConfigFiles(t)
	usrMgr := mailbox.NewManagerWithFiles(base, os.Getuid(), os.Getgid(), usersFile, vmailboxFile)
	lifecycle := NewLifecycle(usrMgr, &Service{}, 0, 0, 0, 0)

	trashBase := filepath.Join(base, ".trash")
	mustMkdir(t, filepath.Join(trashBase, trashDirName("example.com", "alice", 100), "new"))
	mustMkdir(t, filepath.Join(trashBase, trashDirName("other.com", "alice", 200), "new"))
	mustMkdir(t, filepath.Join(trashBase, trashDirName("example.com", "alice", 300), "new"))

	path, err := lifecycle.RestoreFromTrash("alice@example.com", "secret")
	if err != nil {
		t.Fatalf("RestoreFromTrash() error: %v", err)
	}
	wantPath := filepath.Join(base, "example.com", "alice")
	if path != wantPath {
		t.Fatalf("path = %q, want %q", path, wantPath)
	}
	if _, err := os.Stat(filepath.Join(wantPath, "new")); err != nil {
		t.Fatalf("restored maildir missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(trashBase, trashDirName("other.com", "alice", 200), "new")); err != nil {
		t.Fatalf("other domain trash should remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(trashBase, trashDirName("example.com", "alice", 100), "new")); err != nil {
		t.Fatalf("older same-domain trash should remain for GC: %v", err)
	}
	assertFileContains(t, usersFile, "alice@example.com:{PLAIN}secret::::::")
	assertFileContains(t, vmailboxFile, "alice@example.com example.com/alice/")
}

func TestRestoreFromTrashNotInTrash(t *testing.T) {
	base := t.TempDir()
	usersFile, vmailboxFile := setupConfigFiles(t)
	usrMgr := mailbox.NewManagerWithFiles(base, os.Getuid(), os.Getgid(), usersFile, vmailboxFile)
	lifecycle := NewLifecycle(usrMgr, &Service{}, 0, 0, 0, 0)

	_, err := lifecycle.RestoreFromTrash("alice@example.com", "secret")
	if !errors.Is(err, ErrNotInTrash) {
		t.Fatalf("error = %v, want ErrNotInTrash", err)
	}
}

func TestRestoreFromTrashRollsBackWhenConfigFails(t *testing.T) {
	base := t.TempDir()
	usersFile, vmailboxFile := setupConfigFiles(t)
	usrMgr := mailbox.NewManagerWithFiles(base, os.Getuid(), os.Getgid(), usersFile, vmailboxFile)
	lifecycle := NewLifecycle(usrMgr, &Service{}, 0, 0, 0, 0)

	trashName := trashDirName("example.com", "alice", time.Now().Unix())
	trashPath := filepath.Join(base, ".trash", trashName)
	mustMkdir(t, filepath.Join(trashPath, "new"))
	if err := os.Remove(usersFile); err != nil {
		t.Fatalf("remove users file: %v", err)
	}
	mustMkdir(t, usersFile) // make appendToFile fail with "is a directory"

	_, err := lifecycle.RestoreFromTrash("alice@example.com", "secret")
	if err == nil || !strings.Contains(err.Error(), "reinstall configs") {
		t.Fatalf("error = %v, want reinstall configs error", err)
	}
	if _, err := os.Stat(trashPath); err != nil {
		t.Fatalf("trash path should be restored after rollback: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "example.com", "alice")); !os.IsNotExist(err) {
		t.Fatalf("maildir should not remain after rollback, stat err=%v", err)
	}
}

func TestPullDeletingTasksSendsInternalToken(t *testing.T) {
	var gotToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Internal-Token")
		if r.URL.Path != "/api/v1/internal/sync/deleting" {
			t.Fatalf("path = %q, want /api/v1/internal/sync/deleting", r.URL.Path)
		}
		if r.URL.Query().Get("server_id") != "42" {
			t.Fatalf("server_id = %q, want 42", r.URL.Query().Get("server_id"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":[]}`))
	}))
	defer server.Close()

	lifecycle := &Lifecycle{}
	lifecycle.PullDeletingTasks(server.URL, 42, "secret")

	if gotToken != "secret" {
		t.Fatalf("X-Internal-Token = %q, want secret", gotToken)
	}
}

func setupConfigFiles(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.conf")
	vmailboxFile := filepath.Join(dir, "vmailbox")
	if err := os.WriteFile(usersFile, []byte(""), 0644); err != nil {
		t.Fatalf("write users file: %v", err)
	}
	if err := os.WriteFile(vmailboxFile, []byte(""), 0644); err != nil {
		t.Fatalf("write vmailbox file: %v", err)
	}
	return usersFile, vmailboxFile
}

func newTestManager(t *testing.T, base, usersFile, vmailboxFile string) *mailbox.Manager {
	t.Helper()
	return mailbox.NewManagerWithFiles(base, os.Getuid(), os.Getgid(), usersFile, vmailboxFile)
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("%s = %q, want contains %q", path, string(data), want)
	}
}
