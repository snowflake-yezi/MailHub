//go:build unix

package identity

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRejectsBroadIdentityPermissions(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "identity")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := New(directory).LoadOrCreate(); !errors.Is(err, ErrInsecurePermissions) {
		t.Fatalf("directory error = %v, want ErrInsecurePermissions", err)
	}

	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, nodeIDFilename)
	if err := os.WriteFile(path, []byte("6ba7b810-9dad-41d1-80b4-00c04fd430c8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(directory).Load(); !errors.Is(err, ErrInsecurePermissions) {
		t.Fatalf("file error = %v, want ErrInsecurePermissions", err)
	}
}
