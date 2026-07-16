package mailboxaddr

import (
	"fmt"
	"regexp"
	"strings"
)

var localPartPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+\-]{0,63}$`)

func ValidateLocalPart(value string) error {
	if strings.ContainsAny(value, "\r\n\x00") || !localPartPattern.MatchString(value) ||
		strings.HasSuffix(value, ".") || strings.Contains(value, "..") {
		return fmt.Errorf("local_part contains unsupported characters")
	}
	return nil
}

func NormalizeDomain(value string) (string, error) {
	if strings.ContainsAny(value, "\r\n\x00") {
		return "", fmt.Errorf("invalid domain name")
	}
	domain := strings.ToLower(strings.TrimSpace(value))
	domain = strings.TrimSuffix(domain, ".")
	if len(domain) == 0 || len(domain) > 253 {
		return "", fmt.Errorf("invalid domain name")
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("invalid domain name")
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("invalid domain name")
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return "", fmt.Errorf("invalid domain name")
			}
		}
	}
	return domain, nil
}

func ValidatePassword(value string) error {
	if strings.ContainsAny(value, ":\r\n\x00") {
		return fmt.Errorf("password contains unsupported control or separator characters")
	}
	return nil
}
