package handler

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ticket/email-mail-node/internal/filterquarantine"
	"github.com/ticket/email-mail-node/internal/mailbox"
	"github.com/ticket/email-mail-node/internal/nodedata"
	nodecontract "github.com/ticket/email-node-contract"
	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
)

func TestOpenControlDataCoversMessageReadPaths(t *testing.T) {
	root := t.TempDir()
	manager := mailbox.NewManagerWithFiles(root, 5000, 5000, filepath.Join(root, "users.conf"), filepath.Join(root, "vmailbox"))
	handler := &NodeHandler{mailboxMgr: manager, messageIndex: newMessagePathIndex(defaultMessagePathIndexEntries)}
	messagePath := filepath.Join(root, "example.com", "alice", "new", "message.eml")
	message := testDataMessage("<data-1@example.com>")
	writeTestFile(t, messagePath, message)

	list, err := handler.OpenControlData(context.Background(), &nodev1.SystemDataRequest{
		Type:    string(nodecontract.DataRequestMessageList),
		Locator: &nodev1.DataLocator{Mailbox: "alice@example.com", OptionsJson: []byte(`{"page":1,"size":20}`)},
	})
	if err != nil || list.StatusCode != http.StatusOK {
		t.Fatalf("list response = %#v, err=%v", list, err)
	}
	listBody := readControlDataBody(t, list)
	if !strings.Contains(listBody, `"total":1`) || !strings.Contains(listBody, `data-1@example.com`) {
		t.Fatalf("list body = %s", listBody)
	}

	body, err := handler.OpenControlData(context.Background(), &nodev1.SystemDataRequest{
		Type:    string(nodecontract.DataRequestMessageBody),
		Locator: &nodev1.DataLocator{Mailbox: "alice@example.com", MessageId: "<data-1@example.com>"},
	})
	if err != nil || body.StatusCode != http.StatusOK || !strings.Contains(readControlDataBody(t, body), "hello body") {
		t.Fatalf("message body response = %#v, err=%v", body, err)
	}

	raw, err := handler.OpenControlData(context.Background(), &nodev1.SystemDataRequest{
		Type:    string(nodecontract.DataRequestMessageRaw),
		Locator: &nodev1.DataLocator{Mailbox: "alice@example.com", MessageId: "<data-1@example.com>"},
	})
	if err != nil || raw.StatusCode != http.StatusOK || raw.ContentLength != int64(len(message)) || raw.Header.Get("Content-Type") != "message/rfc822" {
		t.Fatalf("raw response = %#v, err=%v", raw, err)
	}
	if got := readControlDataBody(t, raw); got != message {
		t.Fatalf("raw body changed: %q", got)
	}

	for _, requestType := range []nodecontract.DataRequestType{
		nodecontract.DataRequestMessageAttachment,
		nodecontract.DataRequestMessageAttachmentPreview,
	} {
		response, err := handler.OpenControlData(context.Background(), &nodev1.SystemDataRequest{
			Type: string(requestType), Locator: &nodev1.DataLocator{
				Mailbox: "alice@example.com", MessageId: "<data-1@example.com>", AttachmentIndex: 0,
			},
		})
		if err != nil || response.StatusCode != http.StatusOK || readControlDataBody(t, response) != "PDFDATA" {
			t.Fatalf("%s response = %#v, err=%v", requestType, response, err)
		}
		if !strings.Contains(response.Header.Get("Content-Disposition"), "report.pdf") {
			t.Fatalf("%s disposition = %q", requestType, response.Header.Get("Content-Disposition"))
		}
	}
}

func TestOpenControlDataCoversQuarantineReadPaths(t *testing.T) {
	root := t.TempDir()
	maildirRoot := filepath.Join(root, "maildir")
	quarantineRoot := filepath.Join(root, "quarantine")
	manager := mailbox.NewManagerWithFiles(maildirRoot, 5000, 5000, filepath.Join(root, "users.conf"), filepath.Join(root, "vmailbox"))
	handler := &NodeHandler{mailboxMgr: manager, messageIndex: newMessagePathIndex(defaultMessagePathIndexEntries)}
	store, err := filterquarantine.New(quarantineRoot, maildirRoot)
	if err != nil {
		t.Fatal(err)
	}
	handler.ConfigureQuarantine(store, nil)
	source := filepath.Join(maildirRoot, "example.com", "alice", "new", "quarantine.eml")
	writeTestFile(t, source, testDataMessage("<quarantine-1@example.com>"))
	metadata, err := store.Quarantine(source, "alice@example.com", "<quarantine-1@example.com>", strings.Repeat("a", 64), time.Now())
	if err != nil {
		t.Fatal(err)
	}

	message, err := handler.OpenControlData(context.Background(), &nodev1.SystemDataRequest{
		Type: string(nodecontract.DataRequestQuarantineMessage), Locator: &nodev1.DataLocator{QuarantineKey: metadata.QuarantineKey},
	})
	if err != nil || message.StatusCode != http.StatusOK || !strings.Contains(readControlDataBody(t, message), "quarantine-1@example.com") {
		t.Fatalf("quarantine message = %#v, err=%v", message, err)
	}
	attachment, err := handler.OpenControlData(context.Background(), &nodev1.SystemDataRequest{
		Type:    string(nodecontract.DataRequestQuarantineAttachment),
		Locator: &nodev1.DataLocator{QuarantineKey: metadata.QuarantineKey, AttachmentIndex: 0},
	})
	if err != nil || attachment.StatusCode != http.StatusOK || readControlDataBody(t, attachment) != "PDFDATA" {
		t.Fatalf("quarantine attachment = %#v, err=%v", attachment, err)
	}
}

func readControlDataBody(t *testing.T, response *nodedata.Response) string {
	t.Helper()
	body := response.Body
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func testDataMessage(messageID string) string {
	return strings.ReplaceAll(`Message-ID: `+messageID+`
From: sender@example.com
To: alice@example.com
Subject: data stream
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="mixed"

--mixed
Content-Type: text/plain; charset="utf-8"

hello body
--mixed
Content-Type: application/pdf; name="report.pdf"
Content-Disposition: attachment; filename="report.pdf"
Content-Transfer-Encoding: base64

UERGREFUQQ==
--mixed--
`, "\n", "\r\n")
}
