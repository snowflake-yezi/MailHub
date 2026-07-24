//go:build unix

package identity

import (
	"fmt"
	"io/fs"
	"os"
)

func checkRootOnlyDirectory(mode fs.FileMode) error {
	if mode.Perm()&0o077 != 0 {
		return fmt.Errorf("%w: identity directory mode is %04o, want 0700 or stricter", ErrInsecurePermissions, mode.Perm())
	}
	return nil
}

func checkRootOnlyFile(mode fs.FileMode) error {
	if mode.Perm()&0o177 != 0 {
		return fmt.Errorf("%w: identity file mode is %04o, want 0600 or stricter", ErrInsecurePermissions, mode.Perm())
	}
	return nil
}

func syncIdentityDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
