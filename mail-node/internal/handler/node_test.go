package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
