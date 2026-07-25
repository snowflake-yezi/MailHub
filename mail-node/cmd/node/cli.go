package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/ticket/email-mail-node/internal/enrollment"
	"github.com/ticket/email-mail-node/internal/identity"
)

const defaultCLIIdentityDirectory = "/var/lib/mail-node/identity"

func runNodeCommand(args []string, output io.Writer) (bool, error) {
	if len(args) == 0 || args[0] == "serve" {
		return false, nil
	}
	switch args[0] {
	case "identity":
		return true, runIdentityCommand(args[1:], output)
	case "enroll":
		return true, runEnrollCommand(args[1:], output)
	default:
		return true, fmt.Errorf("unknown command %q (expected serve, identity, or enroll)", args[0])
	}
}

func runIdentityCommand(args []string, output io.Writer) error {
	if len(args) == 0 || (args[0] != "init" && args[0] != "show") {
		return fmt.Errorf("identity command requires init or show")
	}
	command := args[0]
	flags := flag.NewFlagSet("identity "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	directory := flags.String("directory", defaultCLIIdentityDirectory, "identity directory")
	credentialFile := flags.String("credential-file", "", "node credential file")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected identity arguments")
	}
	identityStore := identity.New(*directory)
	var record identity.Record
	var err error
	if command == "init" {
		record, err = identityStore.LoadOrCreate()
	} else {
		record, err = identityStore.Load()
	}
	if err != nil {
		return err
	}
	path := resolveCredentialFile(*directory, *credentialFile)
	_, credentialErr := identityStore.LoadCredentialFile(path)
	pending, pendingErr := identityStore.LoadPendingEnrollment()
	if credentialErr != nil && !errors.Is(credentialErr, identity.ErrIdentityNotFound) {
		return credentialErr
	}
	if pendingErr != nil && !errors.Is(pendingErr, identity.ErrIdentityNotFound) {
		return pendingErr
	}
	result := map[string]any{
		"node_uuid": record.NodeUUID, "machine_fingerprint": record.MachineFingerprint,
		"identity_directory": record.Directory, "credential_file": path,
		"credential_present": credentialErr == nil,
	}
	if pendingErr == nil {
		result["pending_request_id"] = pending.RequestID
		result["pending_since"] = pending.CreatedAt
	}
	return json.NewEncoder(output).Encode(result)
}

func runEnrollCommand(args []string, output io.Writer) error {
	resume := len(args) > 0 && args[0] == "resume"
	if resume {
		args = args[1:]
	}
	flags := flag.NewFlagSet("enroll", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	managementURL := flags.String("management-url", "", "management HTTPS URL")
	tokenFile := flags.String("token-file", "", "enrollment token file")
	caFile := flags.String("ca-file", "", "management CA PEM file")
	name := flags.String("name", "", "node display name")
	identityDirectory := flags.String("identity-directory", defaultCLIIdentityDirectory, "identity directory")
	credentialFile := flags.String("credential-file", "", "node credential file")
	waitTimeout := flags.Duration("timeout", 30*time.Minute, "approval wait timeout")
	pollInterval := flags.Duration("poll-interval", 2*time.Second, "approval poll interval")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *waitTimeout <= 0 || *pollInterval <= 0 {
		return fmt.Errorf("invalid enrollment arguments")
	}

	identityStore := identity.New(*identityDirectory)
	ctx, cancel := context.WithTimeout(context.Background(), *waitTimeout)
	defer cancel()
	if resume {
		if _, err := identityStore.Load(); err != nil {
			return fmt.Errorf("load node identity: %w", err)
		}
		pending, err := identityStore.LoadPendingEnrollment()
		if err != nil {
			return fmt.Errorf("load pending enrollment: %w", err)
		}
		if strings.TrimSpace(*managementURL) != "" {
			pending.ManagementURL = strings.TrimSpace(*managementURL)
		}
		if strings.TrimSpace(*caFile) != "" {
			pending.CAFile = strings.TrimSpace(*caFile)
		}
		client, err := enrollment.NewClient(pending.ManagementURL, pending.CAFile)
		if err != nil {
			return err
		}
		credentialPath := pending.CredentialFile
		if strings.TrimSpace(*credentialFile) != "" || strings.TrimSpace(credentialPath) == "" {
			credentialPath = resolveCredentialFile(*identityDirectory, *credentialFile)
		}
		if err := identityStore.ValidateCredentialFile(credentialPath); err != nil {
			return err
		}
		return waitForEnrollment(ctx, client, identityStore, pending, credentialPath, *pollInterval, output)
	}

	if strings.TrimSpace(*managementURL) == "" || strings.TrimSpace(*tokenFile) == "" || strings.TrimSpace(*name) == "" {
		return fmt.Errorf("management-url, token-file, and name are required")
	}
	record, err := identityStore.LoadOrCreate()
	if err != nil {
		return err
	}
	credentialPath := resolveCredentialFile(*identityDirectory, *credentialFile)
	if err := identityStore.ValidateCredentialFile(credentialPath); err != nil {
		return err
	}
	token, err := readSecretFile(*tokenFile)
	if err != nil {
		return err
	}
	client, err := enrollment.NewClient(*managementURL, *caFile)
	if err != nil {
		return err
	}
	hostname, _ := os.Hostname()
	claim, err := client.Claim(ctx, enrollment.ClaimInput{
		Token: token, NodeUUID: record.NodeUUID, Name: strings.TrimSpace(*name), Hostname: hostname,
		OS: runtime.GOOS, Arch: runtime.GOARCH, AgentVersion: nodeAgentVersion(), MachineFingerprint: record.MachineFingerprint,
	})
	if err != nil {
		return err
	}
	pending := identity.PendingEnrollment{
		RequestID: claim.Request.ID, RequestSecret: claim.RequestSecret, ManagementURL: strings.TrimRight(*managementURL, "/"),
		CAFile: strings.TrimSpace(*caFile), NodeName: strings.TrimSpace(*name), CredentialFile: credentialPath, CreatedAt: time.Now().UTC(),
	}
	if err := identityStore.SavePendingEnrollment(pending); err != nil {
		return err
	}
	if err := json.NewEncoder(output).Encode(map[string]any{"state": claim.Request.State, "request_id": claim.Request.ID}); err != nil {
		return err
	}
	return waitForEnrollment(ctx, client, identityStore, pending, credentialPath, *pollInterval, output)
}

type enrollmentClient interface {
	Status(context.Context, string, string) (*enrollment.Request, error)
	Complete(context.Context, string, string) (*enrollment.CompleteResult, error)
}

func waitForEnrollment(ctx context.Context, client enrollmentClient, identityStore *identity.Store, pending identity.PendingEnrollment, credentialFile string, pollInterval time.Duration, output io.Writer) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		request, err := client.Status(ctx, pending.RequestID, pending.RequestSecret)
		if err != nil {
			return err
		}
		switch request.State {
		case "pending":
			select {
			case <-ctx.Done():
				return fmt.Errorf("wait for enrollment approval: %w", ctx.Err())
			case <-ticker.C:
			}
		case "approved":
			completed, err := client.Complete(ctx, pending.RequestID, pending.RequestSecret)
			if err != nil {
				return err
			}
			if err := identityStore.SaveCredentialFile(credentialFile, completed.Credential); err != nil {
				return err
			}
			if err := identityStore.ClearPendingEnrollment(); err != nil {
				return err
			}
			return json.NewEncoder(output).Encode(map[string]any{
				"state": "completed", "request_id": pending.RequestID,
				"credential_prefix": completed.Metadata.CredentialPrefix, "credential_version": completed.Metadata.Version,
			})
		case "rejected", "expired":
			if err := identityStore.ClearPendingEnrollment(); err != nil {
				return err
			}
			return fmt.Errorf("enrollment request %s: %s", request.State, strings.TrimSpace(request.ReviewNote))
		case "completed":
			return fmt.Errorf("enrollment request is already completed but no local credential was confirmed")
		default:
			return fmt.Errorf("management returned unknown enrollment state %q", request.State)
		}
	}
}

func readSecretFile(path string) (string, error) {
	info, err := os.Lstat(strings.TrimSpace(path))
	if err != nil {
		return "", fmt.Errorf("inspect enrollment token file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > 4096 {
		return "", fmt.Errorf("enrollment token file must be a regular file no larger than 4 KiB")
	}
	payload, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return "", fmt.Errorf("read enrollment token file: %w", err)
	}
	token := strings.TrimSpace(string(payload))
	if token == "" || strings.ContainsAny(token, "\r\n\x00") {
		return "", fmt.Errorf("enrollment token file is empty or invalid")
	}
	return token, nil
}

func resolveCredentialFile(identityDirectory, credentialFile string) string {
	if strings.TrimSpace(credentialFile) != "" {
		return filepath.Clean(strings.TrimSpace(credentialFile))
	}
	return filepath.Join(identityDirectory, "credential")
}

func nodeAgentVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && len(setting.Value) >= 12 {
			return setting.Value[:12]
		}
	}
	return "devel"
}
