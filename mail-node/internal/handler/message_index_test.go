package handler

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ticket/email-mail-node/internal/mailbox"
)

type countingReader struct {
	reader io.Reader
	read   int
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.read += n
	return n, err
}

func TestReadMessageHeaderIDDoesNotReadBody(t *testing.T) {
	raw := []byte("Message-ID: <header-only@example.com>\r\nSubject: test\r\n\r\n" + strings.Repeat("x", 1024*1024))
	reader := &countingReader{reader: bytes.NewReader(raw)}

	messageID, err := readMessageHeaderID(reader)
	if err != nil {
		t.Fatalf("readMessageHeaderID() error = %v", err)
	}
	if messageID != "<header-only@example.com>" {
		t.Fatalf("message ID = %q", messageID)
	}
	if reader.read >= len(raw) {
		t.Fatalf("header lookup read the full message: read=%d total=%d", reader.read, len(raw))
	}
}

func TestMessagePathIndexEvictsLeastRecentlyUsed(t *testing.T) {
	tmp := t.TempDir()
	paths := []string{
		filepath.Join(tmp, "a.eml"),
		filepath.Join(tmp, "b.eml"),
		filepath.Join(tmp, "c.eml"),
	}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte(path), 0600); err != nil {
			t.Fatal(err)
		}
	}

	index := newMessagePathIndex(2)
	if !index.putFile("user@example.com", "<a@example.com>", paths[0]) || !index.putFile("user@example.com", "<b@example.com>", paths[1]) {
		t.Fatal("failed to populate index")
	}
	if _, ok := index.get("user@example.com", "a@example.com"); !ok {
		t.Fatal("expected a to be cached")
	}
	if !index.putFile("user@example.com", "<c@example.com>", paths[2]) {
		t.Fatal("failed to cache c")
	}

	if _, ok := index.get("user@example.com", "b@example.com"); ok {
		t.Fatal("least recently used entry b was not evicted")
	}
	for _, messageID := range []string{"a@example.com", "c@example.com"} {
		if _, ok := index.get("user@example.com", messageID); !ok {
			t.Fatalf("expected %s to remain cached", messageID)
		}
	}
}

func TestFindMessagePathInvalidatesChangedFile(t *testing.T) {
	tmp := t.TempDir()
	h := newMessageIndexTestHandler(tmp)
	path := filepath.Join(tmp, "example.com", "user", "new", "message.eml")
	writeTestFile(t, path, "Message-ID: <old@example.com>\r\n\r\nold body")

	if got, ok := h.findMessagePath("user@example.com", "old@example.com"); !ok || got != path {
		t.Fatalf("initial lookup = %q/%v", got, ok)
	}
	writeTestFile(t, path, "Message-ID: <new@example.com>\r\n\r\nnew and longer body")

	if got, ok := h.findMessagePath("user@example.com", "old@example.com"); ok {
		t.Fatalf("stale lookup returned %q", got)
	}
	if got, ok := h.findMessagePath("user@example.com", "new@example.com"); !ok || got != path {
		t.Fatalf("updated lookup = %q/%v", got, ok)
	}
}

func TestFindMessagePathRebuildsAfterNewToCurMove(t *testing.T) {
	tmp := t.TempDir()
	h := newMessageIndexTestHandler(tmp)
	newPath := filepath.Join(tmp, "example.com", "user", "new", "message.eml")
	curPath := filepath.Join(tmp, "example.com", "user", "cur", "message.eml")
	writeTestFile(t, newPath, "Message-ID: <move@example.com>\r\n\r\nbody")

	if got, ok := h.findMessagePath("user@example.com", "<move@example.com>"); !ok || got != newPath {
		t.Fatalf("new lookup = %q/%v", got, ok)
	}
	if err := os.MkdirAll(filepath.Dir(curPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(newPath, curPath); err != nil {
		t.Fatal(err)
	}

	if got, ok := h.findMessagePath("user@example.com", "move@example.com"); !ok || got != curPath {
		t.Fatalf("cur lookup = %q/%v", got, ok)
	}
}

func TestFindMessagePathSupportsFallbackID(t *testing.T) {
	tmp := t.TempDir()
	h := newMessageIndexTestHandler(tmp)
	path := filepath.Join(tmp, "example.com", "user", "new", "message.eml")
	writeTestFile(t, path, "From: sender@example.com\r\nSubject: no id\r\n\r\nbody")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	wantID := fallbackMessageID(path, tmp, info)

	if got, ok := h.findMessagePath("user@example.com", wantID); !ok || got != path {
		t.Fatalf("fallback lookup = %q/%v", got, ok)
	}
	if got, ok := h.messagePaths().get("user@example.com", strings.ToUpper(strings.TrimPrefix(wantID, "fallback-"))); ok {
		t.Fatalf("fallback lookup without required prefix returned %q", got)
	}
	caseVariant := "fallback-" + strings.ToUpper(strings.TrimPrefix(wantID, "fallback-"))
	if got, ok := h.messagePaths().get("user@example.com", caseVariant); !ok || got != path {
		t.Fatalf("case-insensitive fallback cache lookup = %q/%v", got, ok)
	}
}

func TestFindMessagePathConcurrentAccess(t *testing.T) {
	tmp := t.TempDir()
	h := newMessageIndexTestHandler(tmp)
	path := filepath.Join(tmp, "example.com", "user", "new", "message.eml")
	writeTestFile(t, path, "Message-ID: <concurrent@example.com>\r\n\r\nbody")

	const workers = 32
	var wg sync.WaitGroup
	errors := make(chan string, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, ok := h.findMessagePath("user@example.com", "concurrent@example.com")
			if !ok || got != path {
				errors <- got
			}
		}()
	}
	wg.Wait()
	close(errors)
	for got := range errors {
		t.Errorf("concurrent lookup = %q", got)
	}
}

func newMessageIndexTestHandler(maildirBase string) *NodeHandler {
	mgr := mailbox.NewManagerWithFiles(
		maildirBase,
		5000,
		5000,
		filepath.Join(maildirBase, "users.conf"),
		filepath.Join(maildirBase, "vmailbox"),
	)
	return &NodeHandler{mailboxMgr: mgr}
}
