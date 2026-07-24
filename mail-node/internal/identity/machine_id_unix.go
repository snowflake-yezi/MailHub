//go:build unix

package identity

import (
	"errors"
	"io/fs"
	"os"
	"strings"
)

func readMachineID() ([]byte, error) {
	for _, path := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		value, err := os.ReadFile(path)
		if err == nil && strings.TrimSpace(string(value)) != "" {
			return value, nil
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	return nil, ErrMachineIDUnavailable
}
