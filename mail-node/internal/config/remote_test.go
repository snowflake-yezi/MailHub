package config

import (
	"encoding/json"
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
			"configs": map[string]string{"lifecycle.trash_retention_hours": "48"},
			"sources": map[string]string{"lifecycle.trash_retention_hours": "server_override"},
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
