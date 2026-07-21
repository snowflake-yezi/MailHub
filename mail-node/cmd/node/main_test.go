package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mail-node/internal/config"
	"github.com/ticket/email-mail-node/internal/filter"
	"github.com/ticket/email-mail-node/internal/filterdecision"
	"github.com/ticket/email-mail-node/internal/forward"
	"github.com/ticket/email-mail-node/internal/handler"
)

func TestTLSVerificationIsEnabledByDefault(t *testing.T) {
	if tlsInsecureSkip(config.NewRemoteConfig("", "")) {
		t.Fatal("empty node config must verify SMTP TLS certificates")
	}
}

func TestNodeHTTPServerSetsConnectionTimeouts(t *testing.T) {
	handler := http.NewServeMux()
	server := newNodeHTTPServer(":18081", handler)
	if server.Addr != ":18081" {
		t.Fatalf("server address = %q, want :18081", server.Addr)
	}
	if server.Handler != handler {
		t.Fatal("server handler was not preserved")
	}

	tests := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{name: "read header", got: server.ReadHeaderTimeout, want: 5 * time.Second},
		{name: "read", got: server.ReadTimeout, want: 30 * time.Second},
		{name: "write", got: server.WriteTimeout, want: 2 * time.Minute},
		{name: "idle", got: server.IdleTimeout, want: 2 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("timeout = %s, want %s", tt.got, tt.want)
			}
		})
	}
}

func TestFilterConfigRevisionUpdatesRunningEngine(t *testing.T) {
	values := map[string]string{
		"filter.default_action":      "pass",
		"filter.flag_subject_prefix": "",
		filter.SyncIntervalConfigKey: "30",
	}
	revision := uint64(1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"configs": values, "sources": map[string]string{}, "desired_revision": revision,
		}})
	}))
	defer server.Close()

	remoteCfg := config.NewRemoteConfig(server.URL, "secret", 1)
	remoteCfg.RegisterApplyHook(filter.ValidateConfig)
	if err := remoteCfg.PullAll(); err != nil {
		t.Fatal(err)
	}
	engine := newFilterEngine(remoteCfg, "block", "[local]")
	registerFilterConfigAfterApply(remoteCfg, engine)
	if got := engine.Filter(&filter.EmailMessage{}).Action; got != filter.ActionPass {
		t.Fatalf("startup default action = %q, want pass", got)
	}
	if got := engine.GetFlagPrefix(); got != "" {
		t.Fatalf("startup flag prefix = %q, want empty", got)
	}
	if got := configuredFilterSyncInterval(remoteCfg, 3600); got != 30 {
		t.Fatalf("startup sync interval = %d, want 30", got)
	}
	if got := engine.SyncIntervalSeconds(); got != 30 {
		t.Fatalf("engine sync interval = %d, want 30", got)
	}

	values = map[string]string{
		"filter.default_action":      "block",
		"filter.flag_subject_prefix": "[new]",
		filter.SyncIntervalConfigKey: "60",
	}
	revision = 2
	if err := remoteCfg.PullAll(); err != nil {
		t.Fatal(err)
	}
	if got := engine.Filter(&filter.EmailMessage{}).Action; got != filter.ActionBlock {
		t.Fatalf("default action = %q, want block", got)
	}
	if got := engine.GetFlagPrefix(); got != "[new]" {
		t.Fatalf("flag prefix = %q, want [new]", got)
	}
	if got := engine.SyncIntervalSeconds(); got != 60 {
		t.Fatalf("updated sync interval = %d, want 60", got)
	}

	values["filter.default_action"] = "drop"
	revision = 3
	if err := remoteCfg.PullAll(); err == nil {
		t.Fatal("invalid filter revision was committed")
	}
	if got := engine.Filter(&filter.EmailMessage{}).Action; got != filter.ActionBlock {
		t.Fatalf("rejected revision changed default action to %q", got)
	}
	desired, applied := remoteCfg.Revisions()
	if desired != 3 || applied != 2 {
		t.Fatalf("revisions = %d/%d, want 3/2", desired, applied)
	}
}

func TestRequestBodyLimitRejectsOversizedBody(t *testing.T) {
	if maxNodeRequestBodyBytes != 16<<20 {
		t.Fatalf("node request limit = %d, want 16 MiB", maxNodeRequestBodyBytes)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(requestBodyLimit(4))
	r.POST("/body", func(c *gin.Context) {
		if _, err := io.ReadAll(c.Request.Body); err != nil {
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		c.Status(http.StatusNoContent)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/body", strings.NewReader("12345")))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized request status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestNodeRoutesDoNotExposeDeprecatedSMTPFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	nodeH := handler.NewNodeHandler(nil, nil, nil, nil, 0, "", "", "", nil)
	registerNodeRoutes(r, nodeH, "secret")

	foundInternalRoute := false
	for _, route := range r.Routes() {
		if route.Path == "/smtp/filter" {
			t.Fatalf("deprecated public route is still registered: %#v", route)
		}
		if route.Path == "/internal/health" {
			foundInternalRoute = true
		}
	}
	if !foundInternalRoute {
		t.Fatal("internal routes were not registered")
	}
}

func TestClampHeartbeat(t *testing.T) {
	tests := []struct {
		v, fallback, want int
	}{
		{30, 60, 30},   // 合法值原样返回
		{5, 60, 5},     // 下界
		{600, 60, 600}, // 上界
		{0, 60, 60},    // 非法 → fallback
		{-1, 60, 60},   // 负值 → fallback
		{4, 60, 60},    // 下界以下 → fallback
		{601, 60, 60},  // 上界以上 → fallback
	}
	for _, tc := range tests {
		if got := clampHeartbeat(tc.v, tc.fallback, nil); got != tc.want {
			t.Fatalf("clampHeartbeat(%d, %d) = %d, want %d", tc.v, tc.fallback, got, tc.want)
		}
	}
}

func TestRuntimeConfigSnapshotContract(t *testing.T) {
	engine := filter.New(filter.ActionPass, "")
	engine.UpdateConfig(map[string]string{filter.SyncIntervalConfigKey: "30"})
	values := runtimeConfigSnapshotValues(config.NewRemoteConfig("", ""), engine, forward.ForwardConfig{
		ScanInterval: 5, MaxEmailSize: 10485760, BodyPreviewSize: 65536, TargetAddress: "union@example.com",
	}, 24*time.Hour)
	want := []string{
		filter.SyncIntervalConfigKey,
		filterdecision.EngineModeConfigKey, filterdecision.AutoQuarantineConfigKey, "filter.quarantine_base",
		"forward.scan_interval", "forward.max_email_size", "forward.body_preview_size", "forward.target_address",
		"forward.smtp_dial_timeout", "forward.tls_insecure_skip", "forward.tls_min_version",
		"lifecycle.trash_retention_hours", "lifecycle.gc_interval_minutes", "lifecycle.drain_timeout_minutes", "lifecycle.drain_poll_interval_ms",
	}
	if len(values) != len(want) {
		t.Fatalf("snapshot keys = %d, want %d", len(values), len(want))
	}
	for _, key := range want {
		if _, ok := values[key]; !ok {
			t.Fatalf("snapshot provider missing %s", key)
		}
	}
}

func TestStartDiscoveryRetryRecoversAfterManagementStarts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var attempts atomic.Int32
	discovered := make(chan uint64, 1)
	go startDiscoveryRetry(ctx, time.Millisecond, func() (uint64, error) {
		if attempts.Add(1) < 3 {
			return 0, errors.New("management unavailable")
		}
		return 42, nil
	}, func(nodeID uint64) {
		discovered <- nodeID
	})

	select {
	case nodeID := <-discovered:
		if nodeID != 42 {
			t.Fatalf("node ID = %d, want 42", nodeID)
		}
	case <-time.After(time.Second):
		t.Fatal("discovery retry did not recover")
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestNewBootIdentityChangesPerProcessStart(t *testing.T) {
	first, firstStarted := newBootIdentity()
	second, secondStarted := newBootIdentity()
	if first == "" || second == "" || first == second {
		t.Fatalf("boot IDs = %q/%q, want distinct non-empty values", first, second)
	}
	if firstStarted.IsZero() || secondStarted.IsZero() {
		t.Fatal("started_at must be populated")
	}
}

func TestStartPeriodicSnapshotReportsUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := make(chan struct{}, 2)
	go startPeriodicSnapshot(ctx, time.Millisecond, func() error {
		calls <- struct{}{}
		return nil
	})
	select {
	case <-calls:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("periodic snapshot was not reported")
	}
}
