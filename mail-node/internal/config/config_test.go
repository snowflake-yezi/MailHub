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
