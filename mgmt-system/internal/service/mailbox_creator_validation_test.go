package service

import "testing"

func TestValidateMailboxLocalPart(t *testing.T) {
	for _, value := range []string{"order-001", "user.name", "user+tag"} {
		if err := ValidateMailboxLocalPart(value); err != nil {
			t.Fatalf("valid local part %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{"", "../escape", `a\b`, "a/b", ".leading", "trailing.", "two..dots", "line\nbreak", "valid\n"} {
		if err := ValidateMailboxLocalPart(value); err == nil {
			t.Fatalf("invalid local part %q accepted", value)
		}
	}
}

func TestValidateMailboxPasswordRejectsConfigInjection(t *testing.T) {
	for _, value := range []string{"line\nbreak", "line\rbreak", "field:break", "nul\x00break", "valid\n"} {
		if err := ValidateMailboxPassword(value); err == nil {
			t.Fatalf("invalid password %q accepted", value)
		}
	}
	if err := ValidateMailboxPassword("Valid password + symbols!"); err != nil {
		t.Fatalf("valid password rejected: %v", err)
	}
}
