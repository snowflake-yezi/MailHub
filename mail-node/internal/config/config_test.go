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

func TestLoadDefaultsFilterSyncInterval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  port: 8081\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Management.FilterSyncInterval != 3600 || cfg.Management.HeartbeatInterval != 30 {
		t.Fatalf("management defaults = %+v", cfg.Management)
	}
}
