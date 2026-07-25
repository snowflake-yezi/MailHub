package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppliesDockerSecretOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("server:\n  mode: release\ndatabase:\n  dsn: yaml-dsn\nauth:\n  shared_secret: yaml-secret\ndomains:\n  - name: example.com\ndefault_retention_days: 30\n")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAILHUB_DATABASE_DSN", "env-dsn")
	t.Setenv("MAILHUB_SHARED_SECRET", "env-secret")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.DSN != "env-dsn" || cfg.Auth.SharedSecret != "env-secret" {
		t.Fatalf("overrides not applied: dsn=%q secret=%q", cfg.Database.DSN, cfg.Auth.SharedSecret)
	}
}

func TestValidateNormalizesAndRejectsInjectedDomains(t *testing.T) {
	base := Config{Database: DatabaseConfig{DSN: "dsn"}, DefaultRetentionDays: 30}
	base.Domains = []DomainConfig{{Name: "Mail.Example.COM."}}
	if err := base.Validate(); err != nil || base.Domains[0].Name != "mail.example.com" {
		t.Fatalf("normalized domain=%q err=%v", base.Domains[0].Name, err)
	}

	for _, domain := range []string{"example.com\n", "example.com\rother", "../example.com", "bad_domain.com", "example..com"} {
		cfg := Config{Database: DatabaseConfig{DSN: "dsn"}, DefaultRetentionDays: 30, Domains: []DomainConfig{{Name: domain}}}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("invalid config domain %q accepted", domain)
		}
	}
}

func TestLoadDefaultsNodeControlToDisabledLegacyMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("database:\n  dsn: test-dsn\ndomains:\n  - name: example.com\ndefault_retention_days: 30\n")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NodeControl.Enabled {
		t.Fatal("node control must be disabled by default")
	}
	if !cfg.NodeControl.LegacyHTTPEnabled {
		t.Fatal("legacy HTTP must remain enabled by default")
	}
	if cfg.NodeControl.Listen != ":8443" || cfg.NodeControl.DataChunkSize != 256*1024 {
		t.Fatalf("unexpected node control defaults: %+v", cfg.NodeControl)
	}
}

func TestLoadAcceptsExplicitNodeControlFlags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("database:\n  dsn: test-dsn\ndomains:\n  - name: example.com\ndefault_retention_days: 30\nnode_control:\n  enabled: true\n  listen: ':9443'\n  legacy_http_enabled: false\n")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.NodeControl.Enabled || cfg.NodeControl.LegacyHTTPEnabled || cfg.NodeControl.Listen != ":9443" {
		t.Fatalf("node control flags not loaded: %+v", cfg.NodeControl)
	}
}

func TestValidateRequiresTLSForEnabledNodeControl(t *testing.T) {
	base := Config{
		Database: DatabaseConfig{DSN: "dsn"}, Domains: []DomainConfig{{Name: "example.com"}},
		DefaultRetentionDays: 30,
		NodeControl: NodeControlConfig{Enabled: true, Listen: ":8443", HeartbeatIntervalSeconds: 30,
			LeaseTimeoutSeconds: 90, CommandTimeoutSeconds: 15,
			DataMaxConcurrencyPerNode: 4, DataChunkSize: 256 * 1024},
	}
	if err := base.Validate(); err == nil {
		t.Fatal("enabled node control without TLS files was accepted")
	}
	base.NodeControl.TLSCertFile = "control.crt"
	base.NodeControl.TLSKeyFile = "control.key"
	if err := base.Validate(); err != nil {
		t.Fatalf("valid node control config rejected: %v", err)
	}
	base.NodeControl.CommandTimeoutSeconds = 0
	if err := base.Validate(); err == nil {
		t.Fatal("non-positive command timeout was accepted")
	}
	base.NodeControl.CommandTimeoutSeconds = 15
	base.NodeControl.DataChunkSize = 256*1024 + 1
	if err := base.Validate(); err == nil {
		t.Fatal("oversized data chunk was accepted")
	}
	base.NodeControl.DataChunkSize = 256 * 1024
	base.NodeControl.LeaseTimeoutSeconds = 30
	if err := base.Validate(); err == nil {
		t.Fatal("lease timeout equal to heartbeat interval was accepted")
	}
}
