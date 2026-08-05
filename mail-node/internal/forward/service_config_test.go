package forward

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	mailconfig "github.com/ticket/email-mail-node/internal/config"
	"github.com/ticket/email-mail-node/internal/filter"
	"github.com/ticket/email-mail-node/internal/mailbox"
)

func TestScannerSkipsDeliveredQuarantine(t *testing.T) {
	base := t.TempDir()
	newDir := filepath.Join(base, "example.com", "user", "new")
	if err := os.MkdirAll(newDir, 0700); err != nil {
		t.Fatal(err)
	}
	quarantined := filepath.Join(newDir, "message.forwarded-error")
	if err := os.WriteFile(quarantined, []byte("invalid mail"), 0600); err != nil {
		t.Fatal(err)
	}

	mgr := mailbox.NewManagerWithFiles(base, 0, 0, filepath.Join(base, "users"), filepath.Join(base, "vmailbox"))
	service := New(ForwardConfig{}, filter.New(filter.ActionPass, ""), mgr, nil)
	processed, errors := service.ScanOnce()
	if processed != 0 || errors != 0 {
		t.Fatalf("quarantine scan = processed %d errors %d, want 0/0", processed, errors)
	}
	if _, err := os.Stat(quarantined); err != nil {
		t.Fatalf("quarantined file changed during scan: %v", err)
	}
}

func TestScannerSkipsSelfTargetWithoutSMTP(t *testing.T) {
	tests := []struct {
		name   string
		target string
	}{
		{name: "exact address", target: "se@example.com"},
		{name: "case insensitive address", target: "SE@EXAMPLE.COM"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := t.TempDir()
			newDir := filepath.Join(base, "example.com", "se", "new")
			if err := os.MkdirAll(newDir, 0700); err != nil {
				t.Fatal(err)
			}
			messagePath := filepath.Join(newDir, "message")
			message := "From: sender@example.net\r\nTo: se@example.com\r\nSubject: direct mail\r\nMessage-ID: <self-target@example.net>\r\n\r\nbody"
			if err := os.WriteFile(messagePath, []byte(message), 0600); err != nil {
				t.Fatal(err)
			}

			mgr := mailbox.NewManagerWithFiles(base, 0, 0, filepath.Join(base, "users"), filepath.Join(base, "vmailbox"))
			service := New(ForwardConfig{TargetAddress: tt.target}, filter.New(filter.ActionPass, ""), mgr, nil)
			processed, errors := service.ScanOnce()
			if processed != 1 || errors != 0 {
				t.Fatalf("self-target scan = processed %d errors %d, want 1/0", processed, errors)
			}
			if _, err := os.Stat(messagePath); !os.IsNotExist(err) {
				t.Fatalf("self-target message remains in new: %v", err)
			}
			curEntries, err := os.ReadDir(filepath.Join(base, "example.com", "se", "cur"))
			if err != nil {
				t.Fatal(err)
			}
			if len(curEntries) != 1 {
				t.Fatalf("cur entries = %d, want 1", len(curEntries))
			}
		})
	}
}

func TestSameMailboxAddress(t *testing.T) {
	tests := []struct {
		name   string
		source string
		target string
		want   bool
	}{
		{name: "exact", source: "se@example.com", target: "se@example.com", want: true},
		{name: "case and spaces", source: " se@example.com ", target: "SE@EXAMPLE.COM", want: true},
		{name: "different mailbox", source: "orders@example.com", target: "se@example.com", want: false},
		{name: "empty source", source: "", target: "se@example.com", want: false},
		{name: "empty target", source: "se@example.com", target: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sameMailboxAddress(tt.source, tt.target); got != tt.want {
				t.Fatalf("sameMailboxAddress(%q, %q) = %v, want %v", tt.source, tt.target, got, tt.want)
			}
		})
	}
}

func TestDeliveredCommitFailureIsQuarantined(t *testing.T) {
	maildir := t.TempDir()
	newDir := filepath.Join(maildir, "new")
	if err := os.MkdirAll(newDir, 0700); err != nil {
		t.Fatal(err)
	}
	messagePath := filepath.Join(newDir, "message")
	if err := os.WriteFile(messagePath, []byte("mail"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(maildir, "cur"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := commitDeliveredFile(messagePath); err == nil {
		t.Fatal("commitDeliveredFile() error = nil, want quarantined commit failure")
	}
	quarantined := messagePath + ".forwarded-error"
	if shouldProcessMailFile(filepath.Base(quarantined)) {
		t.Fatalf("quarantined file %q would be processed again", quarantined)
	}
	if _, err := os.Stat(quarantined); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(messagePath); !os.IsNotExist(err) {
		t.Fatalf("original delivered file still exists: %v", err)
	}
}

func TestCopyAndRemoveClosesSourceBeforeDelete(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	destination := filepath.Join(dir, "destination")
	if err := os.WriteFile(source, []byte("mail"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyAndRemove(source, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source still exists after copy: %v", err)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "mail" {
		t.Fatalf("destination content = %q, want mail", data)
	}
}

func TestForwardConfigReadsReloadedRuntimeValues(t *testing.T) {
	values := map[string]string{
		"forward.scan_interval":     "7",
		"forward.max_email_size":    "2048",
		"forward.body_preview_size": "1024",
		"forward.target_address":    "ops@example.com",
		"forward.smtp_dial_timeout": "30",
		"forward.tls_insecure_skip": "false",
		"forward.tls_min_version":   "13",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"configs": values, "sources": map[string]string{}, "desired_revision": 1,
		}})
	}))
	defer server.Close()

	remoteCfg := mailconfig.NewRemoteConfig(server.URL, "secret")
	if err := remoteCfg.PullAll(); err != nil {
		t.Fatal(err)
	}
	service := New(ForwardConfig{ScanInterval: 5, MaxEmailSize: 4096, BodyPreviewSize: 2048}, nil, nil, remoteCfg)
	if got := service.currentScanInterval(); got != 7*time.Second {
		t.Fatalf("scan interval = %v, want 7s", got)
	}
	if got := service.currentMaxEmailSize(); got != 2048 {
		t.Fatalf("max email size = %d, want 2048", got)
	}
	if got := service.currentBodyPreviewSize(); got != 1024 {
		t.Fatalf("body preview size = %d, want 1024", got)
	}
	if got := service.currentTarget(); got != "ops@example.com" {
		t.Fatalf("target = %q, want ops@example.com", got)
	}
	smtpCfg := service.currentSMTPConfig()
	if smtpCfg.SMTPDialTimeout != 30*time.Second || smtpCfg.TLSInsecureSkip || smtpCfg.TLSMinVersion != 13 {
		t.Fatalf("smtp config = %#v", smtpCfg)
	}
}

func TestForwardApplyRejectsInvalidRuntimeValue(t *testing.T) {
	service := New(ForwardConfig{}, nil, nil, nil)
	if err := service.ApplyConfig(nil, map[string]string{"forward.scan_interval": "0"}); err == nil {
		t.Fatal("ApplyConfig() error = nil, want invalid interval rejection")
	}
	if err := service.ApplyConfig(nil, map[string]string{"forward.target_address": "invalid"}); err == nil {
		t.Fatal("ApplyConfig() error = nil, want invalid target rejection")
	}
	if err := service.ApplyConfig(nil, map[string]string{"forward.tls_min_version": "11"}); err == nil {
		t.Fatal("invalid TLS version accepted")
	}
	service.AfterApplyConfig(1, 1)
	select {
	case <-service.scanReset:
	default:
		t.Fatal("after apply did not request scan timer reset")
	}
}
