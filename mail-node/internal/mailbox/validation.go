package mailbox

import (
	"fmt"
	"regexp"
	"strings"
)

var localPartPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+\-]{0,63}$`)

func ParseAddress(email string) (localPart, domain string, err error) {
	if strings.Count(email, "@") != 1 || strings.ContainsAny(email, "\r\n\x00") {
		return "", "", fmt.Errorf("invalid email address")
	}
	parts := strings.SplitN(email, "@", 2)
	localPart, domain = parts[0], strings.ToLower(parts[1])
	if !localPartPattern.MatchString(localPart) || strings.HasSuffix(localPart, ".") || strings.Contains(localPart, "..") {
		return "", "", fmt.Errorf("invalid email local part")
	}
	if !validDNSName(domain) {
		return "", "", fmt.Errorf("invalid email domain")
	}
	return localPart, domain, nil
}

func validateMailboxAddress(email string) (localPart, domain string, err error) {
	return ParseAddress(email)
}

func validateMailboxPassword(password string) error {
	if strings.ContainsAny(password, ":\r\n\x00") {
		return fmt.Errorf("password contains unsupported control or separator characters")
	}
	return nil
}

func validDNSName(value string) bool {
	if len(value) == 0 || len(value) > 253 || strings.HasSuffix(value, ".") {
		return false
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}
