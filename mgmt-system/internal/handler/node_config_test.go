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
