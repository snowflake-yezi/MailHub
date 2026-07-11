package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseAdminOptionsReadsPasswordFileAndTrimsNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "password.txt")
	if err := os.WriteFile(path, []byte("StrongPassword!2026\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	options, err := parseAdminOptions("test", []string{"--username", "admin", "--password", "ignored", "--password-file", path, "--must-change-password"})
	if err != nil {
		t.Fatal(err)
	}
	if options.password != "StrongPassword!2026" || !options.mustChange {
		t.Fatalf("options = %#v", options)
	}
}

func TestParseAdminOptionsRequiresExplicitCredentials(t *testing.T) {
	t.Setenv("MAILHUB_BOOTSTRAP_ADMIN_USERNAME", "")
	t.Setenv("MAILHUB_BOOTSTRAP_ADMIN_PASSWORD", "")
	t.Setenv("MAILHUB_BOOTSTRAP_ADMIN_PASSWORD_FILE", "")
	if _, err := parseAdminOptions("test", nil); err == nil {
		t.Fatal("expected missing username error")
	}
	if _, err := parseAdminOptions("test", []string{"--username", "admin"}); err == nil {
		t.Fatal("expected missing password error")
	}
}
