package config

import "testing"

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
