package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mail-node/internal/filterquarantine"
	"github.com/ticket/email-mail-node/internal/mailbox"
)

func TestQuarantineIsHiddenUntilIdempotentRelease(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	maildir := filepath.Join(root, "maildir")
	mgr := mailbox.NewManagerWithFiles(maildir, 5000, 5000, filepath.Join(root, "users.conf"), filepath.Join(root, "vmailbox"))
	store, err := filterquarantine.New(filepath.Join(root, "quarantine"), maildir)
	if err != nil {
		t.Fatal(err)
	}
	handler := &NodeHandler{mailboxMgr: mgr, messageIndex: newMessagePathIndex(10)}
	deliveries := 0
	handler.ConfigureQuarantine(store, func(_, _ string) (string, error) {
		deliveries++
		return "union@example.net", nil
	})

	source := filepath.Join(maildir, "example.com", "user", "new", "message")
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("From: sender@example.net\r\nTo: user@example.com\r\nSubject: isolated\r\nMessage-ID: <isolated@example.net>\r\n\r\nbody"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler.messagePaths().putFile("user@example.com", "<isolated@example.net>", source)
	metadata, err := store.Quarantine(source, "user@example.com", "<isolated@example.net>", "decision-isolated", time.Now())
	if err != nil {
		t.Fatal(err)
	}

	ordinary := httptest.NewRecorder()
	ordinaryContext, _ := gin.CreateTestContext(ordinary)
	ordinaryContext.Request = httptest.NewRequest(http.MethodGet, "/internal/messages/id?mailbox=user@example.com", nil)
	ordinaryContext.Params = gin.Params{{Key: "message_id", Value: "<isolated@example.net>"}}
	handler.GetMessageBody(ordinaryContext)
	if ordinary.Code != http.StatusNotFound {
		t.Fatalf("ordinary query status = %d, body=%s", ordinary.Code, ordinary.Body.String())
	}

	isolation := httptest.NewRecorder()
	isolationContext, _ := gin.CreateTestContext(isolation)
	isolationContext.Request = httptest.NewRequest(http.MethodGet, "/internal/filter-quarantines/key/message", nil)
	isolationContext.Params = gin.Params{{Key: "quarantine_key", Value: metadata.QuarantineKey}}
	handler.GetQuarantineMessage(isolationContext)
	if isolation.Code != http.StatusOK || !strings.Contains(isolation.Body.String(), "isolated") {
		t.Fatalf("quarantine query status = %d, body=%s", isolation.Code, isolation.Body.String())
	}

	for attempt := 0; attempt < 2; attempt++ {
		release := httptest.NewRecorder()
		releaseContext, _ := gin.CreateTestContext(release)
		releaseContext.Request = httptest.NewRequest(http.MethodPost, "/internal/filter-quarantines/key/release", strings.NewReader(`{"operation_id":"operation-1"}`))
		releaseContext.Request.Header.Set("Content-Type", "application/json")
		releaseContext.Params = gin.Params{{Key: "quarantine_key", Value: metadata.QuarantineKey}}
		handler.ReleaseQuarantine(releaseContext)
		if release.Code != http.StatusOK {
			t.Fatalf("release %d status = %d, body=%s", attempt, release.Code, release.Body.String())
		}
	}
	if deliveries != 1 {
		t.Fatalf("deliveries = %d, want 1", deliveries)
	}

	restored := httptest.NewRecorder()
	restoredContext, _ := gin.CreateTestContext(restored)
	restoredContext.Request = httptest.NewRequest(http.MethodGet, "/internal/messages/id?mailbox=user@example.com", nil)
	restoredContext.Params = gin.Params{{Key: "message_id", Value: "<isolated@example.net>"}}
	handler.GetMessageBody(restoredContext)
	if restored.Code != http.StatusOK {
		t.Fatalf("restored query status = %d, body=%s", restored.Code, restored.Body.String())
	}
}
