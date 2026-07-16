package mailbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	return NewManagerWithFiles(
		filepath.Join(dir, "mail"), 5000, 5000,
		filepath.Join(dir, "users.conf"), filepath.Join(dir, "vmailbox"),
	)
}

func TestCreatePropagatesSystemCommandFailure(t *testing.T) {
	mgr := newTestManager(t)
	mgr.SetCommandRunner(func(name string, _ ...string) error {
		if name == "postmap" {
			return errors.New("postmap failed")
		}
		return nil
	})
	if _, err := mgr.Create("user@example.com", "password"); err == nil || !strings.Contains(err.Error(), "postmap failed") {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestManagerRejectsPathAndConfigInjection(t *testing.T) {
	mgr := newTestManager(t)
	for _, email := range []string{"../escape@example.com", `a\b@example.com`, "a@example.com\n"} {
		if _, err := mgr.Create(email, "password"); err == nil {
			t.Fatalf("invalid email %q accepted", email)
		}
	}
	for _, password := range []string{"field:break", "line\nbreak", "line\rbreak", "nul\x00break"} {
		if _, err := mgr.Create("user@example.com", password); err == nil {
			t.Fatalf("invalid password %q accepted", password)
		}
	}
}

func TestConcurrentCreatesPreserveAllConfigLines(t *testing.T) {
	mgr := newTestManager(t)
	const count = 20
	errorsCh := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, err := mgr.Create(fmt.Sprintf("user-%02d@example.com", index), "password")
			errorsCh <- err
		}(i)
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{mgr.usersFile, mgr.vmailboxFile} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		got := 0
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if strings.TrimSpace(line) != "" {
				got++
			}
		}
		if got != count {
			t.Fatalf("%s contains %d records, want %d\n%s", path, got, count, data)
		}
	}
}

func TestRemoveLineMatchesExactMailbox(t *testing.T) {
	mgr := newTestManager(t)
	content := "a@example.com:{PLAIN}one::::::\nba@example.com:{PLAIN}two::::::\n"
	if err := os.WriteFile(mgr.usersFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if err := mgr.removeLineFromFile(mgr.usersFile, "a@example.com"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(mgr.usersFile)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "ba@example.com:") {
		t.Fatalf("unexpected users file: %s", data)
	}
}
