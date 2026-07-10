package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestProxyAttachmentToServerPassesBinary 验证附件代理透传：上游字节、Content-Type、
// Content-Disposition 原样透传给前端，不封信封、不全量缓冲。
func TestProxyAttachmentToServerPassesBinary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Token") != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="x.pdf"; filename*=UTF-8''x.pdf`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("PDFDATA"))
	}))
	defer upstream.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/anything", nil)
	proxyAttachmentToServer(c, strings.TrimPrefix(upstream.URL, "http://"), "GET",
		"/internal/messages/m/attachments/0?mailbox=a@b.com", "secret")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/pdf") {
		t.Fatalf("Content-Type = %q", ct)
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, `filename="x.pdf"`) || !strings.Contains(cd, "filename*=UTF-8''x.pdf") {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	if xcto := w.Header().Get("X-Content-Type-Options"); xcto != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", xcto)
	}
	if w.Body.String() != "PDFDATA" {
		t.Fatalf("body = %q", w.Body.String())
	}
}

// TestProxyAttachmentToServerPassesUpstreamError 验证上游 4xx 透传：状态码与原始 JSON 错误体原样透传，
// 不被重封成统一信封（调用方据状态码判断）。
func TestProxyAttachmentToServerPassesUpstreamError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"code":2003,"message":"attachment index out of range"}`))
	}))
	defer upstream.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/anything", nil)
	proxyAttachmentToServer(c, strings.TrimPrefix(upstream.URL, "http://"), "GET",
		"/internal/messages/m/attachments/9?mailbox=a@b.com", "secret")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "2003") {
		t.Fatalf("error body not passed through: %q", w.Body.String())
	}
}

// TestProxyAttachmentToServerFallsBackOnConnError 验证请求自身失败（连接拒绝）回落到统一信封 500。
func TestProxyAttachmentToServerFallsBackOnConnError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 起后立即关闭，得到一个端口已释放的地址，连接必然 refused
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := strings.TrimPrefix(dead.URL, "http://")
	dead.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/anything", nil)
	proxyAttachmentToServer(c, addr, "GET", "/internal/messages/m/attachments/0?mailbox=a@b.com", "secret")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "fetch attachment failed") {
		t.Fatalf("expected fallback error envelope, body = %q", w.Body.String())
	}
}
