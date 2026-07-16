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
)

func TestDeliveredFileQuarantineIsSkippedByScanner(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "message")
	if err := os.WriteFile(path, []byte("mail"), 0600); err != nil {
		t.Fatal(err)
	}
	quarantined, err := quarantineDeliveredFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if shouldProcessMailFile(filepath.Base(quarantined)) {
		t.Fatalf("quarantined file %q would be processed again", quarantined)
	}
	if _, err := os.Stat(quarantined); err != nil {
		t.Fatal(err)
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
