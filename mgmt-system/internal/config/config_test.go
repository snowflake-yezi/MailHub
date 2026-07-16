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
