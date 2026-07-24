package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	nodeIDFilename      = "node-id"
	fingerprintFilename = "machine-fingerprint"
	credentialFilename  = "credential"
)

var (
	ErrIdentityNotFound     = errors.New("node identity not found")
	ErrCorruptIdentity      = errors.New("node identity is corrupt")
	ErrInsecurePermissions  = errors.New("node identity permissions are too broad")
	ErrCloneDetected        = errors.New("node identity clone detected")
	ErrMachineIDUnavailable = errors.New("stable machine ID unavailable")
)

type Record struct {
	NodeUUID           string
	Directory          string
	MachineFingerprint string
}

// Store owns the root-only node identity directory. MachineID is injectable
// for tests and platform integrations; the raw value is hashed before storage.
type Store struct {
	Directory string
	MachineID func() ([]byte, error)
}

func New(directory string) *Store {
	return &Store{Directory: directory, MachineID: readMachineID}
}

// LoadOrCreate initializes a UUID exactly once and then returns the persisted
// identity. It never replaces a corrupt or cloned identity.
func (store *Store) LoadOrCreate() (Record, error) {
	if err := store.ensureDirectory(); err != nil {
		return Record{}, err
	}

	record, err := store.Load()
	if err == nil {
		return record, nil
	}
	if !errors.Is(err, ErrIdentityNotFound) {
		return Record{}, err
	}
	if err := store.rejectOrphanedIdentityFiles(); err != nil {
		return Record{}, err
	}

	nodeUUID, err := newUUIDv4()
	if err != nil {
		return Record{}, fmt.Errorf("generate node UUID: %w", err)
	}
	if _, err := writeProtectedFileIfAbsent(store.nodeIDPath(), []byte(nodeUUID+"\n")); err != nil {
		return Record{}, fmt.Errorf("persist node UUID: %w", err)
	}
	return store.Load()
}

// Load validates the stored UUID, file permissions, and machine fingerprint.
func (store *Store) Load() (Record, error) {
	if err := store.validateDirectory(); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Record{}, ErrIdentityNotFound
		}
		return Record{}, err
	}

	rawUUID, err := readProtectedFile(store.nodeIDPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Record{}, ErrIdentityNotFound
		}
		return Record{}, err
	}
	nodeUUID := strings.TrimSpace(string(rawUUID))
	if !validUUIDv4(nodeUUID) {
		return Record{}, fmt.Errorf("%w: invalid node UUID", ErrCorruptIdentity)
	}

	fingerprint, err := store.validateMachineFingerprint()
	if err != nil {
		return Record{}, err
	}
	return Record{NodeUUID: nodeUUID, Directory: store.Directory, MachineFingerprint: fingerprint}, nil
}

func (store *Store) ensureDirectory() error {
	if strings.TrimSpace(store.Directory) == "" {
		return fmt.Errorf("identity directory is required")
	}
	info, err := os.Lstat(store.Directory)
	if err == nil {
		return validateIdentityDirectory(info)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect identity directory: %w", err)
	}
	if err := os.MkdirAll(store.Directory, 0o700); err != nil {
		return fmt.Errorf("create identity directory: %w", err)
	}
	if err := os.Chmod(store.Directory, 0o700); err != nil {
		return fmt.Errorf("protect identity directory: %w", err)
	}
	return store.validateDirectory()
}

func (store *Store) validateDirectory() error {
	info, err := os.Lstat(store.Directory)
	if err != nil {
		return err
	}
	return validateIdentityDirectory(info)
}

func validateIdentityDirectory(info fs.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%w: identity path must be a real directory", ErrCorruptIdentity)
	}
	if err := checkRootOnlyDirectory(info.Mode()); err != nil {
		return err
	}
	return nil
}

func (store *Store) rejectOrphanedIdentityFiles() error {
	for _, name := range []string{credentialFilename, fingerprintFilename} {
		_, err := os.Lstat(filepath.Join(store.Directory, name))
		if err == nil {
			return fmt.Errorf("%w: %s exists without %s", ErrCorruptIdentity, name, nodeIDFilename)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("inspect identity directory: %w", err)
		}
	}
	return nil
}

func (store *Store) validateMachineFingerprint() (string, error) {
	machineIDReader := store.MachineID
	if machineIDReader == nil {
		machineIDReader = readMachineID
	}
	machineID, machineErr := machineIDReader()
	current := ""
	if machineErr == nil {
		if strings.TrimSpace(string(machineID)) == "" {
			machineErr = ErrMachineIDUnavailable
		} else {
			current = fingerprint(machineID)
		}
	} else if !errors.Is(machineErr, ErrMachineIDUnavailable) {
		return "", fmt.Errorf("read machine ID: %w", machineErr)
	}

	stored, err := readProtectedFile(store.fingerprintPath())
	if errors.Is(err, fs.ErrNotExist) {
		if current == "" {
			return "", nil
		}
		if _, err := writeProtectedFileIfAbsent(store.fingerprintPath(), []byte(current+"\n")); err != nil {
			return "", fmt.Errorf("persist machine fingerprint: %w", err)
		}
		stored, err = readProtectedFile(store.fingerprintPath())
	}
	if err != nil {
		return "", err
	}
	persisted := strings.TrimSpace(string(stored))
	if !validFingerprint(persisted) {
		return "", fmt.Errorf("%w: invalid machine fingerprint", ErrCorruptIdentity)
	}
	if current != "" && current != persisted {
		return "", fmt.Errorf("%w: persisted fingerprint does not match this machine", ErrCloneDetected)
	}
	return persisted, nil
}

func (store *Store) nodeIDPath() string { return filepath.Join(store.Directory, nodeIDFilename) }
func (store *Store) fingerprintPath() string {
	return filepath.Join(store.Directory, fingerprintFilename)
}

func readProtectedFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s must be a regular file", ErrCorruptIdentity, filepath.Base(path))
	}
	if err := checkRootOnlyFile(info.Mode()); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	return data, nil
}

// writeProtectedFileIfAbsent uses a fully flushed temporary file and an atomic
// hard-link claim so concurrent initializers cannot replace the winning value.
func writeProtectedFileIfAbsent(path string, data []byte) (bool, error) {
	directory := filepath.Dir(path)
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return false, err
	}
	tempPath := filepath.Join(directory, "."+filepath.Base(path)+".tmp-"+hex.EncodeToString(suffix))
	temp, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false, err
	}
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return false, err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return false, err
	}
	if err := temp.Close(); err != nil {
		return false, err
	}
	if err := os.Link(tempPath, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, err
	}
	if err := os.Remove(tempPath); err != nil {
		return false, err
	}
	removeTemp = false
	if err := syncIdentityDirectory(directory); err != nil {
		return false, err
	}
	return true, nil
}

func newUUIDv4() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

func validUUIDv4(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	raw, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	if err != nil || len(raw) != 16 {
		return false
	}
	return raw[6]>>4 == 4 && raw[8]&0xc0 == 0x80 && value == strings.ToLower(value)
}

func fingerprint(machineID []byte) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(string(machineID))))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validFingerprint(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}
