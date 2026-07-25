package command

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
)

const (
	journalFilename      = "command-journal.json"
	defaultMaxEntries    = 2048
	defaultRetention     = 30 * 24 * time.Hour
	journalSchemaVersion = 1
)

var (
	ErrCommandConflict = errors.New("command identity or payload conflicts with the durable journal")
	ErrCommandNotFound = errors.New("command is not present in the durable journal")
)

type StoredResult struct {
	State        nodev1.CommandState `json:"state"`
	ResultCode   string              `json:"result_code,omitempty"`
	ResultJSON   []byte              `json:"result_json,omitempty"`
	ErrorMessage string              `json:"error_message,omitempty"`
	CompletedAt  time.Time           `json:"completed_at"`
}

type journalEntry struct {
	CommandID      string        `json:"command_id"`
	Sequence       uint64        `json:"sequence"`
	CommandType    string        `json:"command_type"`
	SchemaVersion  uint32        `json:"schema_version"`
	IdempotencyKey string        `json:"idempotency_key"`
	Fingerprint    string        `json:"fingerprint"`
	State          string        `json:"state"`
	Result         *StoredResult `json:"result,omitempty"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

type journalFile struct {
	SchemaVersion int                     `json:"schema_version"`
	Entries       map[string]journalEntry `json:"entries"`
}

type JournalConfig struct {
	MaxEntries int
	Retention  time.Duration
	Now        func() time.Time
}

type Journal struct {
	mu         sync.Mutex
	path       string
	entries    map[string]journalEntry
	byKey      map[string]string
	maxEntries int
	retention  time.Duration
	now        func() time.Time
}

// OpenJournal stores the journal beside the protected node identity files.
func OpenJournal(identityDirectory string, config JournalConfig) (*Journal, error) {
	identityDirectory = strings.TrimSpace(identityDirectory)
	if identityDirectory == "" {
		return nil, errors.New("identity directory is required for command journal")
	}
	if config.MaxEntries <= 0 {
		config.MaxEntries = defaultMaxEntries
	}
	if config.Retention <= 0 {
		config.Retention = defaultRetention
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if err := os.MkdirAll(identityDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create command journal directory: %w", err)
	}
	journal := &Journal{
		path: filepath.Join(identityDirectory, journalFilename), entries: make(map[string]journalEntry),
		byKey: make(map[string]string), maxEntries: config.MaxEntries, retention: config.Retention, now: config.Now,
	}
	if err := journal.load(); err != nil {
		return nil, err
	}
	return journal, nil
}

// Begin persists receipt before the caller emits CommandReceived. A cached
// terminal result is returned for duplicate command IDs or idempotency keys.
func (journal *Journal) Begin(command *nodev1.Command) (*StoredResult, error) {
	if err := validateCommand(command); err != nil {
		return nil, err
	}
	fingerprint := commandFingerprint(command)
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if entry, ok := journal.entries[command.CommandId]; ok {
		if entry.Fingerprint != fingerprint || entry.Sequence != command.Sequence || entry.IdempotencyKey != command.IdempotencyKey {
			return nil, ErrCommandConflict
		}
		return cloneStoredResult(entry.Result), nil
	}
	if existingID, ok := journal.byKey[command.IdempotencyKey]; ok {
		existing := journal.entries[existingID]
		if existing.Fingerprint != fingerprint {
			return nil, ErrCommandConflict
		}
		if existing.Result == nil {
			return nil, ErrCommandConflict
		}
		return cloneStoredResult(existing.Result), nil
	}
	now := journal.now().UTC()
	journal.entries[command.CommandId] = journalEntry{
		CommandID: command.CommandId, Sequence: command.Sequence, CommandType: command.Type,
		SchemaVersion: command.SchemaVersion, IdempotencyKey: command.IdempotencyKey,
		Fingerprint: fingerprint, State: "received", CreatedAt: now, UpdatedAt: now,
	}
	journal.byKey[command.IdempotencyKey] = command.CommandId
	if err := journal.persistLocked(); err != nil {
		delete(journal.entries, command.CommandId)
		delete(journal.byKey, command.IdempotencyKey)
		return nil, err
	}
	return nil, nil
}

func (journal *Journal) MarkRunning(commandID string) error {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	entry, ok := journal.entries[commandID]
	if !ok {
		return ErrCommandNotFound
	}
	if entry.Result != nil {
		return nil
	}
	entry.State = "running"
	entry.UpdatedAt = journal.now().UTC()
	journal.entries[commandID] = entry
	return journal.persistLocked()
}

func (journal *Journal) Complete(commandID string, result StoredResult) error {
	if !terminalState(result.State) {
		return errors.New("journal result must be terminal")
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	entry, ok := journal.entries[commandID]
	if !ok {
		return ErrCommandNotFound
	}
	if entry.Result != nil {
		if sameResult(*entry.Result, result) {
			return nil
		}
		return ErrCommandConflict
	}
	if result.CompletedAt.IsZero() {
		result.CompletedAt = journal.now().UTC()
	} else {
		result.CompletedAt = result.CompletedAt.UTC()
	}
	entry.State = "terminal"
	entry.Result = cloneStoredResult(&result)
	entry.UpdatedAt = journal.now().UTC()
	journal.entries[commandID] = entry
	journal.pruneLocked()
	return journal.persistLocked()
}

func (journal *Journal) LastCompletedSequence() uint64 {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	var sequence uint64
	for _, entry := range journal.entries {
		if entry.Result != nil && entry.Sequence > sequence {
			sequence = entry.Sequence
		}
	}
	return sequence
}

func (journal *Journal) load() error {
	data, err := os.ReadFile(journal.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read command journal: %w", err)
	}
	var file journalFile
	if err := json.Unmarshal(data, &file); err != nil || file.SchemaVersion != journalSchemaVersion || file.Entries == nil {
		return fmt.Errorf("decode command journal: invalid or unsupported journal")
	}
	for commandID, entry := range file.Entries {
		if commandID == "" || entry.CommandID != commandID || entry.IdempotencyKey == "" || entry.Fingerprint == "" {
			return fmt.Errorf("decode command journal: corrupt entry")
		}
		if previous, exists := journal.byKey[entry.IdempotencyKey]; exists && previous != commandID {
			return fmt.Errorf("decode command journal: duplicate idempotency key")
		}
		journal.entries[commandID] = entry
		journal.byKey[entry.IdempotencyKey] = commandID
	}
	return nil
}

func (journal *Journal) persistLocked() error {
	data, err := json.Marshal(journalFile{SchemaVersion: journalSchemaVersion, Entries: journal.entries})
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(journal.path), ".command-journal-*")
	if err != nil {
		return fmt.Errorf("create command journal temp file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, journal.path); err != nil {
		return fmt.Errorf("replace command journal: %w", err)
	}
	return os.Chmod(journal.path, 0o600)
}

func (journal *Journal) pruneLocked() {
	cutoff := journal.now().UTC().Add(-journal.retention)
	terminal := make([]journalEntry, 0, len(journal.entries))
	for commandID, entry := range journal.entries {
		if entry.Result != nil && entry.UpdatedAt.Before(cutoff) {
			delete(journal.entries, commandID)
			delete(journal.byKey, entry.IdempotencyKey)
			continue
		}
		if entry.Result != nil {
			terminal = append(terminal, entry)
		}
	}
	if len(journal.entries) <= journal.maxEntries {
		return
	}
	sort.Slice(terminal, func(i, j int) bool { return terminal[i].UpdatedAt.Before(terminal[j].UpdatedAt) })
	for _, entry := range terminal {
		if len(journal.entries) <= journal.maxEntries {
			break
		}
		delete(journal.entries, entry.CommandID)
		delete(journal.byKey, entry.IdempotencyKey)
	}
}

func validateCommand(command *nodev1.Command) error {
	if command == nil || strings.TrimSpace(command.CommandId) == "" || command.Sequence == 0 ||
		strings.TrimSpace(command.Type) == "" || command.SchemaVersion == 0 || strings.TrimSpace(command.IdempotencyKey) == "" {
		return errors.New("command ID, sequence, type, schema version, and idempotency key are required")
	}
	return nil
}

func commandFingerprint(command *nodev1.Command) string {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%s\x00%d\x00", command.Type, command.SchemaVersion)
	_, _ = hash.Write(command.PayloadJson)
	return hex.EncodeToString(hash.Sum(nil))
}

func cloneStoredResult(result *StoredResult) *StoredResult {
	if result == nil {
		return nil
	}
	clone := *result
	clone.ResultJSON = append([]byte(nil), result.ResultJSON...)
	return &clone
}

func terminalState(state nodev1.CommandState) bool {
	switch state {
	case nodev1.CommandState_COMMAND_STATE_SUCCEEDED,
		nodev1.CommandState_COMMAND_STATE_SUCCEEDED_WITH_WARNING,
		nodev1.CommandState_COMMAND_STATE_FAILED,
		nodev1.CommandState_COMMAND_STATE_REJECTED,
		nodev1.CommandState_COMMAND_STATE_EXPIRED:
		return true
	default:
		return false
	}
}

func sameResult(left, right StoredResult) bool {
	return left.State == right.State && left.ResultCode == right.ResultCode &&
		string(left.ResultJSON) == string(right.ResultJSON) && left.ErrorMessage == right.ErrorMessage
}
