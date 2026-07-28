package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHostWithoutPort(t *testing.T) {
	tests := map[string]string{
		"mail.example.com:587": "mail.example.com",
		"mail.example.com":     "mail.example.com",
		"[::1]:587":            "::1",
		"":                     "",
	}
	for input, want := range tests {
		if got := hostWithoutPort(input); got != want {
			t.Fatalf("hostWithoutPort(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLoadNormalizesFilterSyncInterval(t *testing.T) {
	tests := []struct {
		name       string
		management string
		want       int
	}{
		{name: "missing", want: defaultFilterSyncIntervalSeconds},
		{name: "zero", management: "management:\n  filter_sync_interval: 0\n", want: defaultFilterSyncIntervalSeconds},
		{name: "negative", management: "management:\n  filter_sync_interval: -1\n", want: defaultFilterSyncIntervalSeconds},
		{name: "configured", management: "management:\n  filter_sync_interval: 30\n", want: 30},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte("server:\n  port: 8081\n"+tt.management), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Management.FilterSyncInterval != tt.want {
				t.Fatalf("filter sync interval = %d, want %d", cfg.Management.FilterSyncInterval, tt.want)
			}
		})
	}
}

func TestLoadDefaultsNodeTransportToLegacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  port: 8081\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Management.TransportMode != defaultTransportMode {
		t.Fatalf("transport mode = %q, want %q", cfg.Management.TransportMode, defaultTransportMode)
	}
	if cfg.Management.ControlURL != "" {
		t.Fatalf("control URL must be empty by default, got %q", cfg.Management.ControlURL)
	}
	if cfg.Management.CredentialFile != defaultCredentialFile || cfg.Management.CAFile != "" {
		t.Fatalf("unexpected management identity defaults: %+v", cfg.Management)
	}
	if cfg.Identity.Directory != defaultIdentityDir {
		t.Fatalf("identity directory = %q, want %q", cfg.Identity.Directory, defaultIdentityDir)
	}
}

func TestLoadAcceptsExplicitNodeTransportSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte("management:\n  control_url: node-control.example.com:443\n  transport_mode: control_stream\n  credential_file: /run/mail-node/credential\n  ca_file: /etc/ssl/mailhub.pem\nidentity:\n  directory: /srv/mail-node/identity\n")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Management.TransportMode != "control_stream" || cfg.Management.ControlURL != "node-control.example.com:443" {
		t.Fatalf("node transport settings not loaded: %+v", cfg.Management)
	}
	if cfg.Identity.Directory != "/srv/mail-node/identity" {
		t.Fatalf("identity directory = %q", cfg.Identity.Directory)
	}
}

func TestLoadRejectsControlTransportWithoutGatewayAddress(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("management:\n  transport_mode: control_stream\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("control_stream without management.control_url was accepted")
	}
}
