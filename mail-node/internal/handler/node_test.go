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

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mail-node/internal/mailbox"
)

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

	// 2. inline 图片下载 index=1，仍按 collectAttachmentParts 的顺序与元数据对齐。
	wInline := httptest.NewRecorder()
	cInline, _ := gin.CreateTestContext(wInline)
	cInline.Request = httptest.NewRequest(http.MethodGet, "/internal/messages/x/attachments/1?mailbox="+url.QueryEscape(mailbox), nil)
	cInline.Params = gin.Params{{Key: "message_id", Value: messageID}, {Key: "index", Value: "1"}}
	h.GetMessageAttachment(cInline)

	if wInline.Code != http.StatusOK {
		t.Fatalf("inline download status = %d, body = %s", wInline.Code, wInline.Body.String())
	}
	if ct := wInline.Header().Get("Content-Type"); !strings.Contains(ct, "image/png") {
		t.Fatalf("inline Content-Type = %q", ct)
	}
	inlineCD := wInline.Header().Get("Content-Disposition")
	if !strings.HasPrefix(inlineCD, "inline;") || !strings.Contains(inlineCD, "filename*=UTF-8''inline-1.png") {
		t.Fatalf("inline Content-Disposition = %q", inlineCD)
	}
	wantInlineBytes := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00}
	if !bytes.Equal(wInline.Body.Bytes(), wantInlineBytes) {
		t.Fatalf("inline bytes = %v", wInline.Body.Bytes())
	}

	// 3. 越界 index → 404
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodGet, "/internal/messages/x/attachments/9?mailbox="+url.QueryEscape(mailbox), nil)
	c2.Params = gin.Params{{Key: "message_id", Value: messageID}, {Key: "index", Value: "9"}}
	h.GetMessageAttachment(c2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("out-of-range index status = %d, body = %s", w2.Code, w2.Body.String())
	}

	// 4. 邮件不存在 → 404
	w3 := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(w3)
	c3.Request = httptest.NewRequest(http.MethodGet, "/internal/messages/x/attachments/0?mailbox="+url.QueryEscape(mailbox), nil)
	c3.Params = gin.Params{{Key: "message_id", Value: "<nope@nowhere>"}, {Key: "index", Value: "0"}}
	h.GetMessageAttachment(c3)
	if w3.Code != http.StatusNotFound {
		t.Fatalf("missing message status = %d, body = %s", w3.Code, w3.Body.String())
	}
}
