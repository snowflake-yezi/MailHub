package mailboxaddr

import (
	"strings"
	"testing"
)

func TestValidateLocalPartInjectionMatrix(t *testing.T) {
	for _, value := range []string{"order-001", "user.name", "user+tag", strings.Repeat("a", 64)} {
		if err := ValidateLocalPart(value); err != nil {
			t.Fatalf("valid local part %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{
		"", "../escape", `a\b`, "a/b", ".leading", "trailing.", "two..dots",
		"line\nbreak", "line\rbreak", "nul\x00break", "field:break", strings.Repeat("a", 65),
	} {
		if err := ValidateLocalPart(value); err == nil {
			t.Fatalf("invalid local part %q accepted", value)
		}
	}
}

func TestNormalizeDomainInjectionMatrix(t *testing.T) {
	for input, want := range map[string]string{
		"example.com": "example.com", "Mail.Example.COM.": "mail.example.com", " example.com ": "example.com",
	} {
		got, err := NormalizeDomain(input)
		if err != nil || got != want {
			t.Fatalf("NormalizeDomain(%q)=%q err=%v want=%q", input, got, err, want)
		}
	}
	for _, value := range []string{
		"..", ".example.com", "example..com", "example.com\n", "example.com\rother",
		"bad_domain.com", "-bad.example", "bad-.example", `example\com`, strings.Repeat("a", 64) + ".com",
	} {
		if _, err := NormalizeDomain(value); err == nil {
			t.Fatalf("invalid domain %q accepted", value)
		}
	}
}

func TestValidatePasswordInjectionMatrix(t *testing.T) {
	for _, value := range []string{"Valid password + symbols!", " leading and trailing "} {
		if err := ValidatePassword(value); err != nil {
			t.Fatalf("valid password %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{"line\nbreak", "line\rbreak", "field:break", "nul\x00break"} {
		if err := ValidatePassword(value); err == nil {
			t.Fatalf("invalid password %q accepted", value)
		}
	}
}
