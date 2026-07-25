package identity

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLoadOrCreateIsIdempotentAndPersistsUUIDv4(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "identity")
	store := New(directory)
	store.MachineID = func() ([]byte, error) { return []byte("machine-a"), nil }

	first, err := store.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	if first.NodeUUID != second.NodeUUID || !validUUIDv4(first.NodeUUID) {
		t.Fatalf("node UUIDs = %q / %q", first.NodeUUID, second.NodeUUID)
	}
	if first.MachineFingerprint != fingerprint([]byte("machine-a")) {
		t.Fatalf("fingerprint = %q", first.MachineFingerprint)
	}
}

func TestEnrollmentStateAndCredentialAreProtectedAndRecoverable(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "identity")
	store := New(directory)
	store.MachineID = func() ([]byte, error) { return []byte("machine-a"), nil }
	if _, err := store.LoadOrCreate(); err != nil {
		t.Fatal(err)
	}
	pending := PendingEnrollment{
		RequestID: "request-1", RequestSecret: "request-secret", ManagementURL: "https://mgmt.example",
		NodeName: "node-a", CreatedAt: time.Now().UTC(),
	}
	if err := store.SavePendingEnrollment(pending); err != nil {
		t.Fatal(err)
	}
	if err := store.SavePendingEnrollment(PendingEnrollment{RequestID: "request-2", RequestSecret: "other", ManagementURL: "https://mgmt.example", NodeName: "node-b"}); err == nil {
		t.Fatal("a second pending enrollment replaced the resumable request")
	}
	loadedPending, err := store.LoadPendingEnrollment()
	if err != nil || loadedPending.RequestSecret != pending.RequestSecret {
		t.Fatalf("pending enrollment = %+v, error = %v", loadedPending, err)
	}
	if err := store.SaveCredential("first-credential"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCredential("rotated-credential"); err != nil {
		t.Fatal(err)
	}
	credential, err := store.LoadCredential()
	if err != nil || credential != "rotated-credential" {
		t.Fatalf("credential = %q, error = %v", credential, err)
	}
	if err := store.ClearPendingEnrollment(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadPendingEnrollment(); !errors.Is(err, ErrIdentityNotFound) {
		t.Fatalf("cleared pending enrollment error = %v", err)
	}
	if err := store.SaveCredentialFile(filepath.Join(filepath.Dir(directory), "outside"), "credential"); err == nil {
		t.Fatal("credential outside identity directory was accepted")
	}
}

func TestConcurrentInitializationKeepsOneUUID(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "identity")
	const workers = 16
	results := make(chan Record, workers)
	errorsChannel := make(chan error, workers)
	var wait sync.WaitGroup
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			store := New(directory)
			store.MachineID = func() ([]byte, error) { return []byte("machine-a"), nil }
			record, err := store.LoadOrCreate()
			if err != nil {
				errorsChannel <- err
				return
			}
			results <- record
		}()
	}
	wait.Wait()
	close(results)
	close(errorsChannel)
	for err := range errorsChannel {
		t.Errorf("concurrent init: %v", err)
	}
	var nodeUUID string
	for result := range results {
		if nodeUUID == "" {
			nodeUUID = result.NodeUUID
		}
		if result.NodeUUID != nodeUUID {
			t.Fatalf("multiple UUIDs persisted: %q and %q", nodeUUID, result.NodeUUID)
		}
	}
	if nodeUUID == "" {
		t.Fatal("no initializer returned an identity")
	}
}

func TestLoadRejectsCorruptUUIDWithoutReplacingIt(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "identity")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, nodeIDFilename)
	if err := os.WriteFile(path, []byte("not-a-uuid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := New(directory)
	store.MachineID = func() ([]byte, error) { return []byte("machine-a"), nil }
	if _, err := store.LoadOrCreate(); !errors.Is(err, ErrCorruptIdentity) {
		t.Fatalf("error = %v, want ErrCorruptIdentity", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "not-a-uuid\n" {
		t.Fatalf("corrupt identity was replaced with %q", data)
	}
}

func TestLoadDetectsClonedIdentityDirectory(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "identity")
	original := New(directory)
	original.MachineID = func() ([]byte, error) { return []byte("machine-a"), nil }
	if _, err := original.LoadOrCreate(); err != nil {
		t.Fatal(err)
	}

	clone := New(directory)
	clone.MachineID = func() ([]byte, error) { return []byte("machine-b"), nil }
	if _, err := clone.Load(); !errors.Is(err, ErrCloneDetected) {
		t.Fatalf("error = %v, want ErrCloneDetected", err)
	}
}

func TestLoadOrCreateRejectsCredentialWithoutNodeID(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "identity")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, credentialFilename), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(directory).LoadOrCreate(); !errors.Is(err, ErrCorruptIdentity) {
		t.Fatalf("error = %v, want ErrCorruptIdentity", err)
	}
}
