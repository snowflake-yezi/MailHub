package config

import "testing"

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
