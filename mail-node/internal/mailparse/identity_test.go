package mailparse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanMessageIDBoundsHeaderAndIgnoresBody(t *testing.T) {
	raw := "Message-ID: <bounded@example.test>\r\nSubject: test\r\n\r\n" + strings.Repeat("x", MaxMessageHeaderBytes+1)
	messageID, err := ScanMessageID(strings.NewReader(raw))
	if err != nil || messageID != "<bounded@example.test>" {
		t.Fatalf("ScanMessageID() = %q, %v", messageID, err)
	}

	oversized := "X-Padding: " + strings.Repeat("x", MaxMessageHeaderBytes) + "\r\n\r\n"
	if _, err := ScanMessageID(strings.NewReader(oversized)); err == nil {
		t.Fatal("oversized header accepted")
	}
	if _, err := ScanMessageID(strings.NewReader("Message-ID: <unterminated@example.test>")); err == nil {
		t.Fatal("unterminated header accepted")
	}
}

func TestFallbackMessageIDIgnoresMaildirStateAndFlags(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "message")
	if err := os.WriteFile(file, []byte("same bytes"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	maildirBase := filepath.Join(directory, "mail")
	newPath := filepath.Join(maildirBase, "example.test", "inbox", "new", "unique-name")
	curPath := filepath.Join(maildirBase, "example.test", "inbox", "cur", "unique-name:2,RS")
	if left, right := FallbackMessageID(newPath, maildirBase, info), FallbackMessageID(curPath, maildirBase, info); left != right {
		t.Fatalf("fallback IDs differ across Maildir move: %q != %q", left, right)
	}
}

func TestMIMEFailureAndTooLargeRetainScannedIdentity(t *testing.T) {
	fatalPath := filepath.Join("testdata", "body_projection", "fatal-missing-boundary.eml")
	fatal, err := ParseFile(fatalPath, Options{
		Mailbox:       "inbox@example.test",
		MaildirBase:   filepath.Dir(fatalPath),
		ProjectorMode: ProjectorEnforce,
	})
	if err != nil {
		t.Fatalf("fatal ParseFile() error = %v", err)
	}
	if fatal.Status != ParseFailed || fatal.ErrorCode != "mime_parse_failed" || fatal.Message.MessageID != "<fatal-boundary@example.test>" {
		t.Fatalf("fatal result = status %s/%s, message ID %q", fatal.Status, fatal.ErrorCode, fatal.Message.MessageID)
	}

	tooLargePath := filepath.Join("testdata", "body_projection", "alternative-last.eml")
	tooLarge, err := ParseFile(tooLargePath, Options{
		Mailbox:       "inbox@example.test",
		MaildirBase:   filepath.Dir(tooLargePath),
		ProjectorMode: ProjectorEnforce,
		Limits:        Limits{MaxMessageBytes: 1},
	})
	if err != nil {
		t.Fatalf("too-large ParseFile() error = %v", err)
	}
	if tooLarge.Status != ParseTooLarge || tooLarge.Message.MessageID != "<alternative-last@example.test>" {
		t.Fatalf("too-large result = status %s, message ID %q", tooLarge.Status, tooLarge.Message.MessageID)
	}
}
