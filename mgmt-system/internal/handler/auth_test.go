package handler

import "testing"

func TestSafeAdminRedirect(t *testing.T) {
	tests := []struct{ input, want string }{
		{"/admin/", "/admin/"},
		{"/admin/emails?mailbox=a%40example.com", "/admin/emails?mailbox=a%40example.com"},
		{"https://evil.example/admin", ""},
		{"//evil.example/admin", ""},
		{"/api/v1/admin/configs", ""},
		{"/administrator", ""},
		{" /admin/config ", "/admin/config"},
	}
	for _, tt := range tests {
		if got := safeAdminRedirect(tt.input); got != tt.want {
			t.Errorf("safeAdminRedirect(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
