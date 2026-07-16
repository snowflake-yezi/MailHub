package mailbox

import (
	"strings"
	"testing"
)

func TestValidateMailboxAddress(t *testing.T) {
	for _, value := range []string{"order-001@example.com", "user.name+tag@mail.example.com", strings.Repeat("a", 64) + "@example.com"} {
		if _, _, err := validateMailboxAddress(value); err != nil {
			t.Fatalf("valid address %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{
		"../../escape@example.com", `a\b@example.com`, "a/b@example.com", "a..b@example.com",
		"a@..", "a@example..com", "a@example.com\n", "a@example.com\nother", "a@bad_domain.com",
		strings.Repeat("a", 65) + "@example.com", "a@" + strings.Repeat("a", 64) + ".com",
	} {
		if _, _, err := validateMailboxAddress(value); err == nil {
			t.Fatalf("invalid address %q accepted", value)
		}
	}
}

func TestValidateMailboxPassword(t *testing.T) {
	for _, value := range []string{"line\nbreak", "line\rbreak", "field:break", "nul\x00break"} {
		if err := validateMailboxPassword(value); err == nil {
			t.Fatalf("invalid password %q accepted", value)
		}
	}
}
