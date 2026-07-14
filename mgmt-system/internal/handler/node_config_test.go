package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ticket/email-mgmt-system/internal/configschema"
)

func TestNotifyNodeReload(t *testing.T) {
	var gotPath, gotToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotToken = r.Header.Get("X-Internal-Token")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	h := &ConfigHandler{
		sharedSecret: "secret",
		httpClient:   &http.Client{Timeout: time.Second},
	}
	apiHost := strings.TrimPrefix(server.URL, "http://")
	if err := h.notifyNodeReload(apiHost); err != nil {
		t.Fatalf("notifyNodeReload() error: %v", err)
	}
	if gotPath != "/internal/configs/reload" {
		t.Fatalf("path = %q, want /internal/configs/reload", gotPath)
	}
	if gotToken != "secret" {
		t.Fatalf("token = %q, want secret", gotToken)
	}
}

func TestValidateNodeConfigValueSupportsBoolContract(t *testing.T) {
	definition, ok := configschema.Get("forward.tls_insecure_skip")
	if !ok {
		t.Fatal("TLS boolean definition missing")
	}
	if err := validateNodeConfigValue(definition, "false"); err != nil {
		t.Fatalf("valid bool rejected: %v", err)
	}
	if err := validateNodeConfigValue(definition, "yes"); err == nil {
		t.Fatal("invalid bool accepted")
	}
}

func TestNotifyNodeReloadRejectsFailureStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	h := &ConfigHandler{httpClient: &http.Client{Timeout: time.Second}}
	err := h.notifyNodeReload(strings.TrimPrefix(server.URL, "http://"))
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("error = %v, want status 503", err)
	}
}

func TestNodeConfigReloadMetadata(t *testing.T) {
	definition, ok := configschema.Get(trashRetentionKey)
	if !ok || !definition.Reloadable() || definition.RequiresRestart() {
		t.Fatalf("trash retention metadata = %#v", definition)
	}

	result := reloadDispatchResult(definition, 42, http.ErrHandlerTimeout)
	if result["reload_dispatched"] != false || result["reload_error"] == "" {
		t.Fatalf("failed dispatch result = %#v", result)
	}
	if result["desired_revision"] != uint64(42) {
		t.Fatalf("desired revision result = %#v", result)
	}
}

func TestMessageRetentionIsMgmtReadThrough(t *testing.T) {
	definition, ok := configschema.Get("lifecycle.message_retention_days")
	if !ok || definition.Owner != "mgmt-system" || definition.ApplyStrategy != configschema.ReadThrough || !definition.Reloadable() || definition.NodeOverridable {
		t.Fatalf("message retention metadata = %#v", definition)
	}
	result := mgmtReadThroughResult(7)
	if result["reload_target"] != "mgmt_read_through" || result["reload_dispatched"] != true || result["requires_restart"] != false {
		t.Fatalf("read-through result = %#v", result)
	}
}

func TestDefaultRetentionEffectType(t *testing.T) {
	if got := configEffectType("general.default_retention_days", true); got != "hot_reload" {
		t.Fatalf("effect type = %q", got)
	}
	if got := configEffectType("lifecycle.message_retention_days", true); got != "hot_reload" {
		t.Fatalf("node retention effect type = %q", got)
	}
}

func TestValidateNodeConfigValueSupportsStringContract(t *testing.T) {
	definition, ok := configschema.Get("forward.target_address")
	if !ok {
		t.Fatal("target address definition missing")
	}
	if err := validateNodeConfigValue(definition, "ops@example.com"); err != nil {
		t.Fatalf("valid address rejected: %v", err)
	}
	if err := validateNodeConfigValue(definition, "invalid"); err == nil {
		t.Fatal("invalid address accepted")
	}
}
