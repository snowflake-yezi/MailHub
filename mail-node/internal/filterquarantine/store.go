package filterquarantine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	filtercontract "github.com/ticket/email-filter-contract"
	"github.com/ticket/email-mail-node/internal/mailbox"
)

const metadataSchemaVersion = 1

var (
	ErrInvalidPath       = errors.New("invalid quarantine path")
	ErrInvalidKey        = errors.New("invalid quarantine key")
	ErrNotFound          = errors.New("quarantine not found")
	ErrOperationConflict = errors.New("release operation conflicts with existing receipt")
	keyPattern           = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type Metadata struct {
	SchemaVersion      int       `json:"schema_version"`
	QuarantineKey      string    `json:"quarantine_key"`
	DecisionKey        string    `json:"decision_key"`
	Mailbox            string    `json:"mailbox"`
	MessageID          string    `json:"message_id"`
	OriginalMaildirKey string    `json:"original_maildir_key"`
	OriginalPath       string    `json:"original_path"`
	ReceivedAt         time.Time `json:"received_at"`
	QuarantinedAt      time.Time `json:"quarantined_at"`
	ExpiredAt          time.Time `json:"expired_at,omitempty"`
}

type ReleaseFunc func(messagePath, mailbox string) (forwardTarget string, err error)

type Store struct {
	root        string
	maildirBase string
	mu          sync.Mutex
	keyLocks    map[string]*sync.Mutex
	invalidate  func(mailbox, messageID string)
	prewarm     func(mailbox, messageID, path string)
}

func New(root, maildirBase string) (*Store, error) {
	root, maildirBase, err := validateRoots(root, maildirBase)
	if err != nil {
		return nil, err
	}
	for _, directory := range []string{root, filepath.Join(root, "items"), filepath.Join(root, "staging")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, fmt.Errorf("create quarantine directory %s: %w", directory, err)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return nil, fmt.Errorf("secure quarantine directory %s: %w", directory, err)
		}
	}
	store := &Store{root: root, maildirBase: maildirBase, keyLocks: make(map[string]*sync.Mutex)}
	if err := store.Recover(); err != nil {
		return nil, err
	}
	return store, nil
}

func validateRoots(root, maildirBase string) (string, string, error) {
	root = strings.TrimSpace(root)
	maildirBase = strings.TrimSpace(maildirBase)
	if root == "" || maildirBase == "" {
		return "", "", fmt.Errorf("%w: quarantine_base and maildir_base are required", ErrInvalidPath)
	}
	rootAbs, err := resolvePhysicalPath(root)
	if err != nil {
		return "", "", fmt.Errorf("%w: resolve quarantine_base: %v", ErrInvalidPath, err)
	}
	maildirAbs, err := resolvePhysicalPath(maildirBase)
	if err != nil {
		return "", "", fmt.Errorf("%w: resolve maildir_base: %v", ErrInvalidPath, err)
	}
	if pathWithin(rootAbs, maildirAbs) || pathWithin(maildirAbs, rootAbs) {
		return "", "", fmt.Errorf("%w: quarantine_base must be outside maildir_base", ErrInvalidPath)
	}
	return rootAbs, maildirAbs, nil
}

func pathWithin(candidate, parent string) bool {
	rel, err := filepath.Rel(parent, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func resolvePhysicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		return abs, nil
	}
	volume := filepath.VolumeName(abs)
	current := volume + string(filepath.Separator)
	remainder := strings.TrimPrefix(abs, current)
	parts := strings.Split(remainder, string(filepath.Separator))
	for index, part := range parts {
		if part == "" {
			continue
		}
		entries, readErr := os.ReadDir(current)
		if readErr != nil {
			return "", readErr
		}
		var matched os.DirEntry
		for _, entry := range entries {
			if entry.Name() == part || (runtime.GOOS == "windows" && strings.EqualFold(entry.Name(), part)) {
				matched = entry
				break
			}
		}
		if matched == nil {
			for _, missing := range parts[index:] {
				if missing != "" {
					current = filepath.Join(current, missing)
				}
			}
			return filepath.Clean(current), nil
		}
		candidate := filepath.Join(current, matched.Name())
		info, infoErr := matched.Info()
		if infoErr != nil {
			return "", infoErr
		}
		if info.Mode()&os.ModeSymlink == 0 {
			current = candidate
			continue
		}
		if index == len(parts)-1 {
			return "", fmt.Errorf("final path component must not be a symlink")
		}
		current, err = filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", err
		}
	}
	return filepath.Clean(current), nil
}

func KeyForDecision(decisionKey string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(decisionKey)))
	return hex.EncodeToString(sum[:])
}

func (s *Store) SetIndexHooks(invalidate func(mailbox, messageID string), prewarm func(mailbox, messageID, path string)) {
	s.invalidate = invalidate
	s.prewarm = prewarm
}

func (s *Store) Quarantine(sourcePath, mailboxAddress, messageID, decisionKey string, receivedAt time.Time) (*Metadata, error) {
	if strings.TrimSpace(decisionKey) == "" {
		return nil, errors.New("decision key is required")
	}
	localPart, domain, err := mailbox.ParseAddress(mailboxAddress)
	if err != nil {
		return nil, fmt.Errorf("invalid mailbox: %w", err)
	}
	sourceAbs, err := filepath.Abs(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve source", ErrInvalidPath)
	}
	info, err := os.Lstat(sourceAbs)
	if err != nil {
		return nil, fmt.Errorf("stat quarantine source: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: source must be a regular file", ErrInvalidPath)
	}
	if runtime.GOOS != "windows" {
		sourceAbs, err = filepath.EvalSymlinks(sourceAbs)
	}
	if err != nil || !pathWithin(sourceAbs, s.maildirBase) {
		return nil, fmt.Errorf("%w: source must be inside maildir_base", ErrInvalidPath)
	}
	sourceAbs = filepath.Clean(sourceAbs)
	key := KeyForDecision(decisionKey)
	lock := s.lockFor(key)
	lock.Lock()
	defer lock.Unlock()
	if existing, existingErr := s.Metadata(key); existingErr == nil {
		return existing, nil
	}

	now := time.Now().UTC()
	if receivedAt.IsZero() {
		receivedAt = info.ModTime().UTC()
	}
	metadata := Metadata{
		SchemaVersion: metadataSchemaVersion, QuarantineKey: key, DecisionKey: decisionKey,
		Mailbox: localPart + "@" + domain, MessageID: messageID, OriginalMaildirKey: filepath.Base(sourceAbs),
		OriginalPath: sourceAbs, ReceivedAt: receivedAt.UTC(), QuarantinedAt: now,
	}
	staging := filepath.Join(s.root, "staging", key)
	if err := os.Mkdir(staging, 0o700); err != nil && !os.IsExist(err) {
		return nil, fmt.Errorf("create quarantine staging: %w", err)
	}
	if err := writeJSONAtomic(filepath.Join(staging, "metadata.json"), metadata); err != nil {
		return nil, err
	}
	stagedMessage := filepath.Join(staging, "message.eml")
	if err := moveFileDurable(sourceAbs, stagedMessage); err != nil {
		return nil, fmt.Errorf("move message into quarantine staging: %w", err)
	}
	finalDirectory := s.itemDirectory(key)
	if err := os.Rename(staging, finalDirectory); err != nil {
		return nil, fmt.Errorf("commit quarantine item: %w", err)
	}
	if err := syncDirectory(filepath.Dir(finalDirectory)); err != nil {
		return nil, err
	}
	if s.invalidate != nil {
		s.invalidate(metadata.Mailbox, metadata.MessageID)
	}
	return &metadata, nil
}

func (s *Store) Metadata(key string) (*Metadata, error) {
	if !keyPattern.MatchString(key) {
		return nil, ErrInvalidKey
	}
	var metadata Metadata
	if err := readJSON(filepath.Join(s.itemDirectory(key), "metadata.json"), &metadata); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if metadata.SchemaVersion != metadataSchemaVersion || metadata.QuarantineKey != key {
		return nil, errors.New("invalid quarantine metadata")
	}
	return &metadata, nil
}

func (s *Store) MessagePath(key string) (*Metadata, string, error) {
	metadata, err := s.Metadata(key)
	if err != nil {
		return nil, "", err
	}
	path := filepath.Join(s.itemDirectory(key), "message.eml")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, "", ErrNotFound
	}
	return metadata, path, nil
}

func (s *Store) Release(key, operationID string, deliver ReleaseFunc) (*filtercontract.ReleaseReceipt, error) {
	if !keyPattern.MatchString(key) || strings.TrimSpace(operationID) == "" || deliver == nil {
		return nil, ErrInvalidKey
	}
	lock := s.lockFor(key)
	lock.Lock()
	defer lock.Unlock()

	metadata, err := s.Metadata(key)
	if err != nil {
		return nil, err
	}
	messagePath := filepath.Join(s.itemDirectory(key), "message.eml")
	_, messageErr := os.Stat(messagePath)
	receipt, err := s.Receipt(key)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if receipt != nil && receipt.OperationID != operationID {
		return nil, ErrOperationConflict
	}
	if messageErr != nil {
		if receipt == nil || !receipt.SMTPDelivered || !os.IsNotExist(messageErr) {
			return nil, ErrNotFound
		}
		destination, destinationErr := s.restoreDestination(metadata)
		if destinationErr != nil {
			return nil, destinationErr
		}
		if info, statErr := os.Stat(destination); statErr != nil || !info.Mode().IsRegular() {
			return nil, ErrNotFound
		}
		receipt.RestoredToCur = true
		receipt.Status = filtercontract.ReleaseStatusReleased
		receipt.CompletedAt = time.Now().UTC()
		receipt.ErrorCode, receipt.ErrorSummary = "", ""
		if err := s.writeReceipt(key, receipt); err != nil {
			return nil, err
		}
		if s.prewarm != nil {
			s.prewarm(metadata.Mailbox, metadata.MessageID, destination)
		}
		return receipt, nil
	}
	if receipt == nil {
		receipt = &filtercontract.ReleaseReceipt{
			SchemaVersion: filtercontract.SchemaVersionV1, OperationID: operationID, QuarantineKey: key,
			DecisionKey: metadata.DecisionKey, Status: filtercontract.ReleaseStatusReleasing,
		}
	}
	if !receipt.SMTPDelivered {
		target, deliverErr := deliver(messagePath, metadata.Mailbox)
		if deliverErr != nil {
			return s.failReceipt(key, receipt, "smtp_failed", deliverErr)
		}
		receipt.SMTPDelivered = true
		receipt.ForwardTarget = target
		receipt.ErrorCode, receipt.ErrorSummary = "", ""
		if err := s.writeReceipt(key, receipt); err != nil {
			return nil, err
		}
	}
	if !receipt.RestoredToCur {
		destination, restoreErr := s.restoreDestination(metadata)
		if restoreErr == nil {
			if info, statErr := os.Stat(destination); statErr == nil && info.Mode().IsRegular() {
				restoreErr = os.Remove(messagePath)
			} else {
				restoreErr = moveFileDurable(messagePath, destination)
			}
		}
		if restoreErr != nil {
			return s.failReceipt(key, receipt, "restore_failed", restoreErr)
		}
		receipt.RestoredToCur = true
		if s.prewarm != nil {
			s.prewarm(metadata.Mailbox, metadata.MessageID, destination)
		}
	}
	receipt.Status = filtercontract.ReleaseStatusReleased
	receipt.CompletedAt = time.Now().UTC()
	receipt.ErrorCode, receipt.ErrorSummary = "", ""
	if err := s.writeReceipt(key, receipt); err != nil {
		return nil, err
	}
	return receipt, nil
}

func (s *Store) Receipt(key string) (*filtercontract.ReleaseReceipt, error) {
	if !keyPattern.MatchString(key) {
		return nil, ErrInvalidKey
	}
	var receipt filtercontract.ReleaseReceipt
	if err := readJSON(filepath.Join(s.itemDirectory(key), "release-receipt.json"), &receipt); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := receipt.Validate(); err != nil {
		return nil, err
	}
	return &receipt, nil
}

func (s *Store) failReceipt(key string, receipt *filtercontract.ReleaseReceipt, code string, cause error) (*filtercontract.ReleaseReceipt, error) {
	receipt.Status = filtercontract.ReleaseStatusFailed
	receipt.ErrorCode = code
	receipt.ErrorSummary = cause.Error()
	receipt.CompletedAt = time.Now().UTC()
	if err := s.writeReceipt(key, receipt); err != nil {
		return nil, err
	}
	return receipt, cause
}

func (s *Store) writeReceipt(key string, receipt *filtercontract.ReleaseReceipt) error {
	return writeJSONAtomic(filepath.Join(s.itemDirectory(key), "release-receipt.json"), receipt)
}

func (s *Store) PurgeExpired(cutoff time.Time) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, "items"))
	if err != nil {
		return nil, err
	}
	expired := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() || !keyPattern.MatchString(entry.Name()) {
			continue
		}
		lock := s.lockFor(entry.Name())
		lock.Lock()
		metadata, metadataErr := s.Metadata(entry.Name())
		if metadataErr != nil || metadata.ReceivedAt.After(cutoff) {
			lock.Unlock()
			continue
		}
		if !metadata.ExpiredAt.IsZero() {
			expired = append(expired, entry.Name())
			lock.Unlock()
			continue
		}
		receipt, receiptErr := s.Receipt(entry.Name())
		if receiptErr != nil && !errors.Is(receiptErr, ErrNotFound) {
			lock.Unlock()
			return expired, receiptErr
		}
		if receipt != nil && receipt.SMTPDelivered {
			lock.Unlock()
			continue
		}
		messagePath := filepath.Join(s.itemDirectory(entry.Name()), "message.eml")
		if removeErr := os.Remove(messagePath); os.IsNotExist(removeErr) {
			lock.Unlock()
			continue
		} else if removeErr != nil {
			lock.Unlock()
			return expired, removeErr
		}
		metadata.ExpiredAt = time.Now().UTC()
		if writeErr := writeJSONAtomic(filepath.Join(s.itemDirectory(entry.Name()), "metadata.json"), metadata); writeErr != nil {
			lock.Unlock()
			return expired, writeErr
		}
		expired = append(expired, entry.Name())
		lock.Unlock()
	}
	return expired, nil
}

func (s *Store) Recover() error {
	entries, err := os.ReadDir(filepath.Join(s.root, "staging"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !keyPattern.MatchString(entry.Name()) {
			continue
		}
		staging := filepath.Join(s.root, "staging", entry.Name())
		var metadata Metadata
		if err := readJSON(filepath.Join(staging, "metadata.json"), &metadata); err != nil || metadata.QuarantineKey != entry.Name() {
			continue
		}
		if _, err := os.Stat(filepath.Join(staging, "message.eml")); err != nil {
			continue
		}
		finalDirectory := s.itemDirectory(entry.Name())
		if _, err := os.Stat(finalDirectory); err == nil {
			continue
		}
		if err := os.Rename(staging, finalDirectory); err != nil {
			return fmt.Errorf("recover quarantine %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func (s *Store) RecoveryResult(decisionKey string) (filtercontract.ProcessingResult, bool) {
	key := KeyForDecision(decisionKey)
	metadata, path, err := s.MessagePath(key)
	if err != nil || metadata.DecisionKey != decisionKey || path == "" {
		return filtercontract.ProcessingResult{}, false
	}
	return filtercontract.ProcessingResult{
		Status: "succeeded", AttemptedAction: filtercontract.ActionQuarantine,
		ActualAction: filtercontract.ActionQuarantine, QuarantineKey: key, OriginalMaildirKey: metadata.OriginalMaildirKey,
	}, true
}

func (s *Store) restoreDestination(metadata *Metadata) (string, error) {
	localPart, domain, err := mailbox.ParseAddress(metadata.Mailbox)
	if err != nil {
		return "", err
	}
	filename := filepath.Base(metadata.OriginalMaildirKey)
	if filename == "." || filename == "" || strings.Contains(filename, string(filepath.Separator)) {
		return "", errors.New("invalid original Maildir key")
	}
	if runtime.GOOS != "windows" && !strings.Contains(filename, ":2,") {
		filename += ":2,S"
	}
	directory := filepath.Join(s.maildirBase, domain, localPart, "cur")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	destination := filepath.Join(directory, filename)
	if !pathWithin(destination, s.maildirBase) {
		return "", ErrInvalidPath
	}
	if _, err := os.Stat(destination); err == nil {
		return destination, nil
	}
	return destination, nil
}

func (s *Store) itemDirectory(key string) string { return filepath.Join(s.root, "items", key) }

func (s *Store) lockFor(key string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock := s.keyLocks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		s.keyLocks[key] = lock
	}
	return lock
}

func moveFileDurable(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	if err := os.Rename(source, destination); err == nil {
		return syncDirectory(filepath.Dir(destination))
	}
	temporary := destination + ".copying"
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	dst, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		src.Close()
		return err
	}
	_, copyErr := io.Copy(dst, src)
	if copyErr == nil {
		copyErr = dst.Sync()
	}
	closeErr := dst.Close()
	srcCloseErr := src.Close()
	if copyErr != nil || closeErr != nil || srcCloseErr != nil {
		_ = os.Remove(temporary)
		return errors.Join(copyErr, closeErr, srcCloseErr)
	}
	if err := os.Rename(temporary, destination); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := syncDirectory(filepath.Dir(destination)); err != nil {
		return err
	}
	return os.Remove(source)
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func syncDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
