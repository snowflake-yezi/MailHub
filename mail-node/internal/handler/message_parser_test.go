package handler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jhillyerd/enmime"
)

func TestParseMessageFileMultipartWithAttachment(t *testing.T) {
	tmp := t.TempDir()
	maildirBase := filepath.Join(tmp, "mail")
	filePath := filepath.Join(maildirBase, "example.com", "order-001", "new", "msg.eml")
	writeTestFile(t, filePath, strings.ReplaceAll(`Message-ID: <msg-1@example.com>
From: =?UTF-8?B?6Iiq56m65YWs5Y+4?= <notice@example.com>
To: order-001@example.com
Subject: =?UTF-8?B?6Iiq5Y+Y6YCa55+l?=
Date: Sat, 27 Jun 2026 10:20:30 +0000
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="mixed-boundary"

--mixed-boundary
Content-Type: multipart/alternative; boundary="alt-boundary"

--alt-boundary
Content-Type: text/plain; charset="utf-8"
Content-Transfer-Encoding: quoted-printable

=E6=82=A8=E7=9A=84=E8=88=AA=E7=8F=AD=E5=B7=B2=E5=8F=98=E6=9B=B4
--alt-boundary
Content-Type: text/html; charset="utf-8"

<html><body><p>您的航班已变更</p></body></html>
--alt-boundary--

--mixed-boundary
Content-Type: application/pdf; name="itinerary.pdf"
Content-Disposition: attachment; filename="itinerary.pdf"
Content-Transfer-Encoding: base64

UERGREFUQQ==
--mixed-boundary--
`, "\n", "\r\n"))

	msg, err := parseFullMessage(filePath, "order-001@example.com", maildirBase)
	if err != nil {
		t.Fatalf("parseFullMessage() error = %v", err)
	}
	if msg.MessageID != "<msg-1@example.com>" {
		t.Fatalf("MessageID = %q", msg.MessageID)
	}
	if msg.Subject != "航变通知" {
		t.Fatalf("Subject = %q", msg.Subject)
	}
	if !strings.Contains(msg.TextBody, "您的航班已变更") {
		t.Fatalf("TextBody = %q", msg.TextBody)
	}
	if msg.HTMLBody == "" {
		t.Fatalf("HTMLBody is empty")
	}
	if len(msg.Attachments) != 1 {
		t.Fatalf("attachments len = %d", len(msg.Attachments))
	}
	if msg.Attachments[0].Filename != "itinerary.pdf" || msg.Attachments[0].ContentType != "application/pdf" {
		t.Fatalf("attachment = %+v", msg.Attachments[0])
	}
}

func TestParseMessageFileFallbackID(t *testing.T) {
	tmp := t.TempDir()
	maildirBase := filepath.Join(tmp, "mail")
	filePath := filepath.Join(maildirBase, "example.com", "order-002", "new", "msg.eml")
	writeTestFile(t, filePath, "From: sender@example.com\r\nSubject: No ID\r\n\r\nhello")

	msg1, err := parseFullMessage(filePath, "order-002@example.com", maildirBase)
	if err != nil {
		t.Fatalf("parseFullMessage() error = %v", err)
	}
	msg2, err := parseFullMessage(filePath, "order-002@example.com", maildirBase)
	if err != nil {
		t.Fatalf("parseFullMessage() second error = %v", err)
	}
	if msg1.MessageID == "" || !strings.HasPrefix(msg1.MessageID, "fallback-") {
		t.Fatalf("fallback MessageID = %q", msg1.MessageID)
	}
	if msg1.MessageID != msg2.MessageID {
		t.Fatalf("fallback IDs differ: %q vs %q", msg1.MessageID, msg2.MessageID)
	}
}

func TestHTMLToPlainTextFallback(t *testing.T) {
	got := htmlToPlainText("<html><body><p>Hello&nbsp;World</p><br><b>OK</b></body></html>")
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "World") || !strings.Contains(got, "OK") {
		t.Fatalf("htmlToPlainText() = %q", got)
	}
}

// TestCollectAttachmentPartsOrderAndContent 验证附件 part 展平顺序（先 attachment 后 inline）
// 与字节保留；并断言 collectAttachments 元数据的 index 与该顺序完全对齐——
// 这是下载端点按 index 取字节的正确性根基。
func TestCollectAttachmentPartsOrderAndContent(t *testing.T) {
	env := &enmime.Envelope{
		Attachments: []*enmime.Part{
			{FileName: "a.pdf", Content: []byte("PDF-AAA"), ContentType: "application/pdf"},
		},
		Inlines: []*enmime.Part{
			{FileName: "logo.png", Content: []byte("PNG-BBB"), ContentType: "image/png"},
		},
	}
	parts := collectAttachmentParts(env)
	if len(parts) != 2 {
		t.Fatalf("parts len = %d", len(parts))
	}
	if parts[0].FileName != "a.pdf" || string(parts[0].Content) != "PDF-AAA" {
		t.Fatalf("parts[0] = %+v", parts[0])
	}
	if parts[1].FileName != "logo.png" || string(parts[1].Content) != "PNG-BBB" {
		t.Fatalf("parts[1] = %+v", parts[1])
	}

	atts := collectAttachments(env)
	if len(atts) != 2 {
		t.Fatalf("attachments len = %d", len(atts))
	}
	if atts[0].Index != 0 || atts[0].Filename != "a.pdf" || atts[0].Inline {
		t.Fatalf("atts[0] = %+v", atts[0])
	}
	if atts[1].Index != 1 || atts[1].Filename != "logo.png" || !atts[1].Inline {
		t.Fatalf("atts[1] = %+v", atts[1])
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
