package config

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRemoteConfigGetStringFallbacks(t *testing.T) {
	rc := NewRemoteConfig("http://mgmt.example", "secret")

	if got := rc.GetString("forward.target_address", "union@asadad.bond"); got != "union@asadad.bond" {
		t.Fatalf("missing key fallback = %q, want %q", got, "union@asadad.bond")
	}

	rc.configs["forward.target_address"] = ""
	if got := rc.GetString("forward.target_address", "union@asadad.bond"); got != "union@asadad.bond" {
		t.Fatalf("empty value fallback = %q, want %q", got, "union@asadad.bond")
	}

	rc.configs["forward.target_address"] = "ops@example.com"
	if got := rc.GetString("forward.target_address", "union@asadad.bond"); got != "ops@example.com" {
		t.Fatalf("configured value = %q, want %q", got, "ops@example.com")
	}
}

func TestGetDurationHours(t *testing.T) {
	rc := NewRemoteConfig("", "")
	rc.configs["lifecycle.trash_retention_hours"] = "24"
	if got := rc.GetDurationHours("lifecycle.trash_retention_hours", time.Hour); got != 24*time.Hour {
		t.Fatalf("duration = %v, want 24h", got)
	}
}

func TestPullAllUsesServerIDAndTracksSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("server_id"); got != "42" {
			t.Errorf("server_id = %q", got)
		}
		if got := r.Header.Get("X-Internal-Token"); got != "secret" {
			t.Errorf("token = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"configs":          map[string]string{"lifecycle.trash_retention_hours": "48"},
			"sources":          map[string]string{"lifecycle.trash_retention_hours": "server_override"},
			"desired_revision": 7,
		}})
	}))
	defer server.Close()
	rc := NewRemoteConfig(server.URL, "secret", 42)
	if err := rc.PullAll(); err != nil {
		t.Fatal(err)
	}
	if got := rc.Source("lifecycle.trash_retention_hours"); got != "server_override" {
		t.Fatalf("source = %q", got)
	}
	if desired, applied := rc.Revisions(); desired != 7 || applied != 7 {
		t.Fatalf("revisions = (%d, %d), want (7, 7)", desired, applied)
	}
}

func TestPullAllPrefersNodeCredentialAuthentication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Node node-secret" || request.Header.Get("X-MailHub-Node-UUID") != "node-uuid" {
			t.Errorf("node auth headers = %q / %q", request.Header.Get("Authorization"), request.Header.Get("X-MailHub-Node-UUID"))
		}
		if request.Header.Get("X-Internal-Token") != "" {
			t.Error("shared secret was sent with node credential")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"configs": map[string]string{}, "sources": map[string]string{}, "desired_revision": 0,
		}})
	}))
	defer server.Close()
	rc := NewRemoteConfig(server.URL, "legacy-secret")
	rc.ConfigureNodeCredential("node-uuid", "node-secret")
	if err := rc.PullAll(); err != nil {
		t.Fatal(err)
	}
}

func TestSetNodeIDSwitchesPollingToNodeScope(t *testing.T) {
	queries := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries <- r.URL.Query().Get("server_id")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"configs": map[string]string{}, "sources": map[string]string{}, "desired_revision": 0,
		}})
	}))
	defer server.Close()
	rc := NewRemoteConfig(server.URL, "secret")
	if err := rc.PullAll(); err != nil {
		t.Fatal(err)
	}
	rc.SetNodeID(42)
	if err := rc.PullAll(); err != nil {
		t.Fatal(err)
	}
	if got := <-queries; got != "" {
		t.Fatalf("initial server_id = %q, want empty", got)
	}
	if got := <-queries; got != "42" {
		t.Fatalf("recovered server_id = %q, want 42", got)
	}
	if got := rc.NodeID(); got != 42 {
		t.Fatalf("NodeID() = %d, want 42", got)
	}
}

func TestPullAllKeepsAppliedConfigWhenHookFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"configs": map[string]string{"key": "new"}, "sources": map[string]string{"key": "global"}, "desired_revision": 2,
		}})
	}))
	defer server.Close()

	rc := NewRemoteConfig(server.URL, "secret", 1)
	rc.configs["key"] = "old"
	rc.desiredRevision = 1
	rc.appliedRevision = 1
	rc.RegisterApplyHook(func(_, _ map[string]string) error { return errors.New("rejected") })

	if err := rc.PullAll(); err == nil {
		t.Fatal("PullAll() error = nil, want apply failure")
	}
	if got := rc.GetString("key", ""); got != "old" {
		t.Fatalf("applied config = %q, want old", got)
	}
	if desired, applied := rc.Revisions(); desired != 2 || applied != 1 {
		t.Fatalf("revisions after failed apply = (%d, %d), want (2, 1)", desired, applied)
	}
	if rc.LastApplyError() == "" {
		t.Fatal("last apply error was not recorded")
	}
}

func TestStartPollingConvergesWithoutNotification(t *testing.T) {
	requestSeen := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case requestSeen <- struct{}{}:
		default:
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"configs": map[string]string{"key": "polled"}, "sources": map[string]string{"key": "global"}, "desired_revision": 3,
		}})
	}))
	defer server.Close()

	rc := NewRemoteConfig(server.URL, "secret", 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go rc.StartPolling(ctx, 10*time.Millisecond, nil)
	select {
	case <-requestSeen:
	case <-time.After(time.Second):
		t.Fatal("periodic pull was not attempted")
	}
	deadline := time.Now().Add(time.Second)
	for rc.GetString("key", "") != "polled" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := rc.GetString("key", ""); got != "polled" {
		t.Fatalf("polled config = %q, want polled", got)
	}
}

func TestPullAllSkipsHooksForUnchangedAppliedRevision(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"configs": map[string]string{"key": "same"}, "sources": map[string]string{"key": "global"}, "desired_revision": 4,
		}})
	}))
	defer server.Close()

	rc := NewRemoteConfig(server.URL, "secret", 1)
	calls := 0
	afterCalls := 0
	rc.RegisterApplyHook(func(_, _ map[string]string) error { calls++; return nil })
	rc.RegisterAfterApplyHook(func(_, _ uint64) { afterCalls++ })
	if err := rc.PullAll(); err != nil {
		t.Fatal(err)
	}
	if err := rc.PullAll(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("apply hook calls = %d, want 1", calls)
	}
	if afterCalls != 1 {
		t.Fatalf("after apply hook calls = %d, want 1", afterCalls)
	}
}

func TestReportSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/internal/servers/7/config-snapshot" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("X-Internal-Token") != "secret" {
			t.Error("missing internal token")
		}
		var body struct {
			Items []struct {
				EffectiveValue string `json:"effective_value"`
				Source         string `json:"source"`
			} `json:"items"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Items) != 1 || body.Items[0].EffectiveValue != "24" || body.Items[0].Source != "global" {
			t.Errorf("body = %+v", body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	rc := NewRemoteConfig(server.URL, "secret", 7)
	rc.sources["lifecycle.trash_retention_hours"] = "global"
	if err := rc.ReportSnapshot("lifecycle.trash_retention_hours", "24", time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestReportSnapshotsSendsAllAppliedValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			BootID    string    `json:"boot_id"`
			StartedAt time.Time `json:"started_at"`
			Items     []struct {
				ConfigKey string `json:"config_key"`
			} `json:"items"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Items) != 2 {
			t.Fatalf("snapshot items = %d, want 2", len(body.Items))
		}
		if body.BootID != "boot-7" {
			t.Fatalf("boot_id = %q, want boot-7", body.BootID)
		}
		if body.StartedAt.IsZero() {
			t.Fatal("started_at was not reported")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	rc := NewRemoteConfig(server.URL, "secret", 7)
	rc.SetBootIdentity("boot-7", time.Now())
	if err := rc.ReportSnapshots(map[string]string{"forward.scan_interval": "5", "lifecycle.gc_interval_minutes": "60"}, time.Now()); err != nil {
		t.Fatal(err)
	}
}
