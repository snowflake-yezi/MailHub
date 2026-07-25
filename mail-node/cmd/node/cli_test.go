package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ticket/email-mail-node/internal/enrollment"
	"github.com/ticket/email-mail-node/internal/identity"
)

type approvedEnrollmentClient struct{}

func (approvedEnrollmentClient) Status(context.Context, string, string) (*enrollment.Request, error) {
	return &enrollment.Request{ID: "request-1", State: "approved"}, nil
}

func (approvedEnrollmentClient) Complete(context.Context, string, string) (*enrollment.CompleteResult, error) {
	result := &enrollment.CompleteResult{Credential: "mhn_runtime-secret"}
	result.Metadata.CredentialPrefix = "mhn_runtime-"
	result.Metadata.Version = 1
	return result, nil
}

func TestWaitForEnrollmentPersistsCredentialBeforeClearingResumeState(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "identity")
	store := identity.New(directory)
	store.MachineID = func() ([]byte, error) { return []byte("machine-a"), nil }
	if _, err := store.LoadOrCreate(); err != nil {
		t.Fatal(err)
	}
	pending := identity.PendingEnrollment{
		RequestID: "request-1", RequestSecret: "request-secret", ManagementURL: "https://management.example",
		NodeName: "node-a", CreatedAt: time.Now().UTC(),
	}
	if err := store.SavePendingEnrollment(pending); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	credentialPath := filepath.Join(directory, "credential")
	if err := waitForEnrollment(context.Background(), approvedEnrollmentClient{}, store, pending, credentialPath, time.Millisecond, &output); err != nil {
		t.Fatal(err)
	}
	credential, err := store.LoadCredentialFile(credentialPath)
	if err != nil || credential != "mhn_runtime-secret" {
		t.Fatalf("credential = %q, error = %v", credential, err)
	}
	if _, err := store.LoadPendingEnrollment(); !errors.Is(err, identity.ErrIdentityNotFound) {
		t.Fatalf("pending enrollment error = %v", err)
	}
	if strings.Contains(output.String(), "mhn_runtime-secret") || !strings.Contains(output.String(), `"state":"completed"`) {
		t.Fatalf("CLI output = %s", output.String())
	}
}
