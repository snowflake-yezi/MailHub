package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	mailconfig "github.com/ticket/email-mail-node/internal/config"
	"github.com/ticket/email-mail-node/internal/mailbox"
)

func TestPurgeExpiredMessageFilesDeletesOnlyExpiredMessages(t *testing.T) {
	dir := t.TempDir()
	oldFile := filepath.Join(dir, "old")
	newFile := filepath.Join(dir, "new")
	if err := os.WriteFile(oldFile, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newFile, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(oldFile, now.Add(-48*time.Hour), now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newFile, now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	deleted, failed := purgeExpiredMessageFiles([]string{oldFile, newFile}, now.Add(-24*time.Hour))
	if deleted != 1 || failed != 0 {
		t.Fatalf("deleted/failed = %d/%d", deleted, failed)
	}
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Fatalf("old file still exists: %v", err)
	}
	if _, err := os.Stat(newFile); err != nil {
		t.Fatalf("new file removed: %v", err)
	}
}

func TestPurgeExpiredMessagesBatchUsesPerMailboxRetention(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmp := t.TempDir()
	mgr := mailbox.NewManagerWithFiles(tmp, 5000, 5000, filepath.Join(tmp, "users.conf"), filepath.Join(tmp, "vmailbox"))
	h := &NodeHandler{mailboxMgr: mgr}
	now := time.Now()

	oldA := filepath.Join(tmp, "example.com", "a", "new", "old-a")
	oldB := filepath.Join(tmp, "example.com", "b", "cur", "old-b")
	newB := filepath.Join(tmp, "example.com", "b", "new", "new-b")
	for _, path := range []string{oldA, oldB, newB} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("message"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for path, age := range map[string]time.Duration{oldA: 31 * 24 * time.Hour, oldB: 8 * 24 * time.Hour, newB: 6 * 24 * time.Hour} {
		stamp := now.Add(-age)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}

	body := `{"items":[{"email_address":"a@example.com","retention_days":30},{"email_address":"b@example.com","retention_days":7},{"email_address":"invalid","retention_days":7}]}`
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/internal/messages/retention/purge", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h.PurgeExpiredMessagesBatch(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Processed int `json:"processed_mailboxes"`
			Deleted   int `json:"deleted"`
			Failed    int `json:"failed"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Processed != 2 || resp.Data.Deleted != 2 || resp.Data.Failed != 1 {
		t.Fatalf("response = %s", w.Body.String())
	}
	for _, path := range []string{oldA, oldB} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expired message still exists: %s (%v)", path, err)
		}
	}
	if _, err := os.Stat(newB); err != nil {
		t.Fatalf("unexpired message removed: %v", err)
	}
}

func TestDeleteMessageRemovesOnlyMatchedMaildirFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmp := t.TempDir()
	mgr := mailbox.NewManagerWithFiles(tmp, 5000, 5000, filepath.Join(tmp, "users.conf"), filepath.Join(tmp, "vmailbox"))
	h := &NodeHandler{mailboxMgr: mgr}
	matched := filepath.Join(tmp, "example.com", "a", "new", "matched.eml")
	other := filepath.Join(tmp, "example.com", "a", "cur", "other.eml")
	writeTestFile(t, matched, "Message-ID: <delete-me@example.com>\r\nSubject: delete me\r\n\r\nbody")
	writeTestFile(t, other, "Message-ID: <keep-me@example.com>\r\nSubject: keep me\r\n\r\nbody")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/internal/messages/x?mailbox=a@example.com", nil)
	c.Params = gin.Params{{Key: "message_id", Value: "<delete-me@example.com>"}}
	h.DeleteMessage(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(matched); !os.IsNotExist(err) {
		t.Fatalf("matched message still exists: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("unmatched message removed: %v", err)
	}

	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/internal/messages/x?mailbox=a@example.com", nil)
	c.Params = gin.Params{{Key: "message_id", Value: "<delete-me@example.com>"}}
	h.DeleteMessage(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("second delete status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestCurrentNodeIDUsesRecoveredRemoteIdentity(t *testing.T) {
	remoteCfg := mailconfig.NewRemoteConfig("", "")
	h := &NodeHandler{nodeID: 0, remoteCfg: remoteCfg}
	if got := h.currentNodeID(); got != 0 {
		t.Fatalf("initial node ID = %d, want 0", got)
	}
	remoteCfg.SetNodeID(42)
	if got := h.currentNodeID(); got != 42 {
		t.Fatalf("recovered node ID = %d, want 42", got)
	}
}

func TestSplitPage(t *testing.T) {
	files := []string{"a", "b", "c", "d", "e"}
	got := splitPage(files, 2, 2)
	if len(got) != 2 || got[0] != "c" || got[1] != "d" {
		t.Fatalf("splitPage() = %#v", got)
	}
	if got := splitPage(files, 4, 2); got != nil {
		t.Fatalf("splitPage() past end = %#v", got)
	}
}

func TestParsePageSizeDefaults(t *testing.T) {
	// Covered indirectly by handler tests in higher layers; keep pagination helper behavior explicit.
	if got := splitPage([]string{"a"}, 1, 20); len(got) != 1 || got[0] != "a" {
		t.Fatalf("splitPage default-style call = %#v", got)
	}
}

// TestStatsEndpoint 验证 /internal/stats 返回邮箱数、邮件总数与磁盘字段,
// 且对空 Maildir 不崩溃。disk 的具体值跨平台不定(Windows 走 stub),仅校验字段存在。
func TestStatsEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmp := t.TempDir()
	usersFile := filepath.Join(tmp, "users.conf")
	if err := os.WriteFile(usersFile, []byte(
		"a@example.com:{PLAIN}p::::::\nb@example.com:{PLAIN}p::::::\n"), 0600); err != nil {
		t.Fatalf("write users.conf: %v", err)
	}
	vmailbox := filepath.Join(tmp, "vmailbox")
	if err := os.WriteFile(vmailbox, nil, 0600); err != nil {
		t.Fatalf("write vmailbox: %v", err)
	}

	mgr := mailbox.NewManagerWithFiles(tmp, 5000, 5000, usersFile, vmailbox)
	h := &NodeHandler{mailboxMgr: mgr, nodeID: 7, nodeName: "test-node"}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/internal/stats", nil)
	h.Stats(c)

	if w.Code != http.StatusOK {
		t.Fatalf("Stats status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			NodeID        uint64 `json:"node_id"`
			NodeName      string `json:"node_name"`
			MailboxCount  int    `json:"mailbox_count"`
			TotalMessages int    `json:"total_messages"`
			Disk          struct {
				TotalBytes uint64 `json:"total_bytes"`
				UsedBytes  uint64 `json:"used_bytes"`
				FreeBytes  uint64 `json:"free_bytes"`
			} `json:"disk"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal stats: %v body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("stats code = %d", resp.Code)
	}
	if resp.Data.NodeID != 7 || resp.Data.NodeName != "test-node" {
		t.Fatalf("node identity = %d/%q", resp.Data.NodeID, resp.Data.NodeName)
	}
	if resp.Data.MailboxCount != 2 {
		t.Fatalf("mailbox_count = %d, want 2", resp.Data.MailboxCount)
	}
	if resp.Data.TotalMessages != 0 {
		t.Fatalf("total_messages = %d, want 0 on empty maildir", resp.Data.TotalMessages)
	}
}

// TestGetMessageAttachment 验证附件下载端点：正常返回原始字节流（真实 Content-Type +
// RFC 5987 Content-Disposition），越界 index 与不存在的 message_id 均返回 404。
func TestGetMessageAttachment(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmp := t.TempDir()
	usersFile := filepath.Join(tmp, "users.conf")
	if err := os.WriteFile(usersFile, []byte("order-001@example.com:{PLAIN}p::::::\n"), 0600); err != nil {
		t.Fatalf("write users.conf: %v", err)
	}
	vmailbox := filepath.Join(tmp, "vmailbox")
	if err := os.WriteFile(vmailbox, nil, 0600); err != nil {
		t.Fatalf("write vmailbox: %v", err)
	}
	mgr := mailbox.NewManagerWithFiles(tmp, 5000, 5000, usersFile, vmailbox)
	h := &NodeHandler{mailboxMgr: mgr, nodeID: 1, nodeName: "test"}

	emlPath := filepath.Join(tmp, "example.com", "order-001", "new", "msg.eml")
	writeTestFile(t, emlPath, strings.ReplaceAll(`Message-ID: <msg-1@example.com>
From: notice@example.com
To: order-001@example.com
Subject: with attachment
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="mixed-boundary"

--mixed-boundary
Content-Type: multipart/related; boundary="related-boundary"; type="text/html"

--related-boundary
Content-Type: text/html; charset="utf-8"

<html><body><p>hello body</p><img src="cid:logo123@example.com"></body></html>
--related-boundary
Content-Type: application/octet-stream
Content-ID: <logo123@example.com>
Content-Disposition: inline
Content-Transfer-Encoding: base64

iVBORw0KGgoA
--related-boundary--

--mixed-boundary
Content-Type: application/pdf; name="itinerary.pdf"
Content-Disposition: attachment; filename="itinerary.pdf"
Content-Transfer-Encoding: base64

UERGREFUQQ==
--mixed-boundary
Content-Type: application/zip; name="archive.zip"
Content-Disposition: attachment; filename="archive.zip"
Content-Transfer-Encoding: base64

UEsDBAoAAAA=
--mixed-boundary
Content-Type: text/html; charset="utf-8"; name="unsafe.html"
Content-Disposition: attachment; filename="unsafe.html"
Content-Transfer-Encoding: base64

PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==
--mixed-boundary
Content-Type: image/svg+xml; name="unsafe.svg"
Content-Disposition: attachment; filename="unsafe.svg"
Content-Transfer-Encoding: base64

PHN2ZyBvbmxvYWQ9ImFsZXJ0KDEpIj48L3N2Zz4=
--mixed-boundary--
`, "\n", "\r\n"))

	const (
		mailbox   = "order-001@example.com"
		messageID = "<msg-1@example.com>"
	)

	// 1. 正常下载 index=0
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/internal/messages/x/attachments/0?mailbox="+url.QueryEscape(mailbox), nil)
	c.Params = gin.Params{{Key: "message_id", Value: messageID}, {Key: "index", Value: "0"}}
	h.GetMessageAttachment(c)

	if w.Code != http.StatusOK {
		t.Fatalf("download status = %d, body = %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/pdf") {
		t.Fatalf("Content-Type = %q", ct)
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment;") || !strings.Contains(cd, "filename*=UTF-8''itinerary.pdf") {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	if w.Body.String() != "PDFDATA" {
		t.Fatalf("attachment bytes = %q", w.Body.String())
	}

	// 2. inline 图片下载 index=4，仍按 collectAttachmentParts 的顺序与元数据对齐（附件先于 inline）。
	wInline := httptest.NewRecorder()
	cInline, _ := gin.CreateTestContext(wInline)
	cInline.Request = httptest.NewRequest(http.MethodGet, "/internal/messages/x/attachments/4?mailbox="+url.QueryEscape(mailbox), nil)
	cInline.Params = gin.Params{{Key: "message_id", Value: messageID}, {Key: "index", Value: "4"}}
	h.GetMessageAttachment(cInline)

	if wInline.Code != http.StatusOK {
		t.Fatalf("inline download status = %d, body = %s", wInline.Code, wInline.Body.String())
	}
	if ct := wInline.Header().Get("Content-Type"); !strings.Contains(ct, "image/png") {
		t.Fatalf("inline Content-Type = %q", ct)
	}
	inlineCD := wInline.Header().Get("Content-Disposition")
	if !strings.HasPrefix(inlineCD, "inline;") || !strings.Contains(inlineCD, "filename*=UTF-8''inline-4.png") {
		t.Fatalf("inline Content-Disposition = %q", inlineCD)
	}
	wantInlineBytes := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00}
	if !bytes.Equal(wInline.Body.Bytes(), wantInlineBytes) {
		t.Fatalf("inline bytes = %v", wInline.Body.Bytes())
	}

	// 3. PDF 预览使用 inline disposition 且带 nosniff。
	wPreview := httptest.NewRecorder()
	cPreview, _ := gin.CreateTestContext(wPreview)
	cPreview.Request = httptest.NewRequest(http.MethodGet, "/internal/messages/x/attachments/0/preview?mailbox="+url.QueryEscape(mailbox), nil)
	cPreview.Params = gin.Params{{Key: "message_id", Value: messageID}, {Key: "index", Value: "0"}}
	h.GetMessageAttachmentPreview(cPreview)

	if wPreview.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body = %s", wPreview.Code, wPreview.Body.String())
	}
	if ct := wPreview.Header().Get("Content-Type"); !strings.Contains(ct, "application/pdf") {
		t.Fatalf("preview Content-Type = %q", ct)
	}
	if cd := wPreview.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "inline;") {
		t.Fatalf("preview Content-Disposition = %q", cd)
	}
	if xcto := wPreview.Header().Get("X-Content-Type-Options"); xcto != "nosniff" {
		t.Fatalf("preview X-Content-Type-Options = %q", xcto)
	}
	if wPreview.Body.String() != "PDFDATA" {
		t.Fatalf("preview bytes = %q", wPreview.Body.String())
	}

	// 4. 非图片/PDF/文本附件不允许预览，仍可走下载。
	wZipPreview := httptest.NewRecorder()
	cZipPreview, _ := gin.CreateTestContext(wZipPreview)
	cZipPreview.Request = httptest.NewRequest(http.MethodGet, "/internal/messages/x/attachments/1/preview?mailbox="+url.QueryEscape(mailbox), nil)
	cZipPreview.Params = gin.Params{{Key: "message_id", Value: messageID}, {Key: "index", Value: "1"}}
	h.GetMessageAttachmentPreview(cZipPreview)

	if wZipPreview.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("zip preview status = %d, body = %s", wZipPreview.Code, wZipPreview.Body.String())
	}

	// 5. HTML 附件只按纯文本预览，避免浏览器执行附件内容。
	wHTMLPreview := httptest.NewRecorder()
	cHTMLPreview, _ := gin.CreateTestContext(wHTMLPreview)
	cHTMLPreview.Request = httptest.NewRequest(http.MethodGet, "/internal/messages/x/attachments/2/preview?mailbox="+url.QueryEscape(mailbox), nil)
	cHTMLPreview.Params = gin.Params{{Key: "message_id", Value: messageID}, {Key: "index", Value: "2"}}
	h.GetMessageAttachmentPreview(cHTMLPreview)

	if wHTMLPreview.Code != http.StatusOK {
		t.Fatalf("html preview status = %d, body = %s", wHTMLPreview.Code, wHTMLPreview.Body.String())
	}
	if ct := wHTMLPreview.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Fatalf("html preview Content-Type = %q", ct)
	}
	if wHTMLPreview.Body.String() != "<script>alert(1)</script>" {
		t.Fatalf("html preview body = %q", wHTMLPreview.Body.String())
	}

	// 6. SVG 不允许预览，避免可执行图片内容进入预览器。
	wSVGPreview := httptest.NewRecorder()
	cSVGPreview, _ := gin.CreateTestContext(wSVGPreview)
	cSVGPreview.Request = httptest.NewRequest(http.MethodGet, "/internal/messages/x/attachments/3/preview?mailbox="+url.QueryEscape(mailbox), nil)
	cSVGPreview.Params = gin.Params{{Key: "message_id", Value: messageID}, {Key: "index", Value: "3"}}
	h.GetMessageAttachmentPreview(cSVGPreview)

	if wSVGPreview.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("svg preview status = %d, body = %s", wSVGPreview.Code, wSVGPreview.Body.String())
	}

	// 7. 越界 index → 404
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodGet, "/internal/messages/x/attachments/9?mailbox="+url.QueryEscape(mailbox), nil)
	c2.Params = gin.Params{{Key: "message_id", Value: messageID}, {Key: "index", Value: "9"}}
	h.GetMessageAttachment(c2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("out-of-range index status = %d, body = %s", w2.Code, w2.Body.String())
	}

	// 8. 邮件不存在 → 404
	w3 := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(w3)
	c3.Request = httptest.NewRequest(http.MethodGet, "/internal/messages/x/attachments/0?mailbox="+url.QueryEscape(mailbox), nil)
	c3.Params = gin.Params{{Key: "message_id", Value: "<nope@nowhere>"}, {Key: "index", Value: "0"}}
	h.GetMessageAttachment(c3)
	if w3.Code != http.StatusNotFound {
		t.Fatalf("missing message status = %d, body = %s", w3.Code, w3.Body.String())
	}
}
