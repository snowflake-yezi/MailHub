//go:build !unix

package identity

import "io/fs"

func checkRootOnlyDirectory(fs.FileMode) error { return nil }
func checkRootOnlyFile(fs.FileMode) error      { return nil }
func syncIdentityDirectory(string) error       { return nil }
