package forward

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mailconfig "github.com/ticket/email-mail-node/internal/config"
)

func TestForwardConfigReadsReloadedRuntimeValues(t *testing.T) {
	values := map[string]string{
		"forward.scan_interval":     "7",
		"forward.max_email_size":    "2048",
		"forward.body_preview_size": "1024",
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
}

func TestForwardApplyRejectsInvalidRuntimeValue(t *testing.T) {
	service := New(ForwardConfig{}, nil, nil, nil)
	if err := service.ApplyConfig(nil, map[string]string{"forward.scan_interval": "0"}); err == nil {
		t.Fatal("ApplyConfig() error = nil, want invalid interval rejection")
	}
	service.AfterApplyConfig(1, 1)
	select {
	case <-service.scanReset:
	default:
		t.Fatal("after apply did not request scan timer reset")
	}
}
