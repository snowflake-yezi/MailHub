package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var rawEmailProxyHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

type upstreamHTTPError struct {
	StatusCode int
	Body       []byte
}

func (e *upstreamHTTPError) Error() string {
	return fmt.Sprintf("upstream error: %d - %s", e.StatusCode, string(e.Body))
}

// uuidShort 返回短 UUID（前 8 位），用作 request_id
func uuidShort() string {
	return uuid.New().String()[:8]
}

// generatePassword 生成 16 位随机密码
func generatePassword() string {
	return fmt.Sprintf("%x-%s", time.Now().UnixNano(), uuid.New().String()[:4])[:16]
}

// parseUint64 安全解析 uint64
func parseUint64(s string) uint64 {
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}

// proxyToServer 代理请求到邮箱服务器的内部 API
func proxyToServer(serverAPIHost string, method string, path string, body io.Reader, sharedSecret string) ([]byte, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	url := fmt.Sprintf("http://%s%s", serverAPIHost, path)

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", sharedSecret)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("proxy request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, &upstreamHTTPError{StatusCode: resp.StatusCode, Body: data}
	}

	return data, nil
}

// writeUpstreamJSONError preserves a mail-node JSON error response while
// attaching the mgmt-system request ID. Transport and malformed responses are
// not handled here; callers should map those to ErrCodeExternalFail.
func writeUpstreamJSONError(c *gin.Context, err error) bool {
	var upstreamErr *upstreamHTTPError
	if !errors.As(err, &upstreamErr) {
		return false
	}

	var rawResp map[string]interface{}
	if json.Unmarshal(upstreamErr.Body, &rawResp) != nil {
		return false
	}
	if _, ok := rawResp["code"]; !ok {
		return false
	}
	if message, ok := rawResp["message"].(string); !ok || message == "" {
		return false
	}

	rawResp["request_id"] = uuidShort()
	c.JSON(upstreamErr.StatusCode, rawResp)
	return true
}

// writeJSON 写入 JSON 响应（代理转发用）
func writeJSON(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// unmarshalProxyResp 解析代理响应中的标准格式
func unmarshalProxyResp(data []byte) (map[string]interface{}, error) {
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// proxyAttachmentToServer 透传邮件服务器的二进制响应（附件下载专用）。
//
// 与 proxyToServer 的关键差异：附件下载例外于统一 JSON 信封——
//   - 保留上游 Content-Type / Content-Disposition 响应头（直接复用给前端/调用方）
//   - 流式 io.Copy 写出 body，避免把整个附件读进内存再封信封
//   - 上游 4xx/5xx 时透传状态码与原始 body（JSON 错误信封），不吞不重封
//
// 仅本函数自身的请求失败（建连/请求异常）才回落到 serverError 统一信封。
// 超时取 60s（大于 proxyToServer 的 10s），适配附件下载体积。
func proxyAttachmentToServer(c *gin.Context, serverAPIHost, method, path, sharedSecret string) {
	proxyBinaryToServer(c, serverAPIHost, method, path, sharedSecret, &http.Client{Timeout: 60 * time.Second}, "attachment")
}

// proxyRawEmailToServer waits at most 60 seconds for upstream headers, then
// lets the HTTP server write timeout govern the streamed response body.
func proxyRawEmailToServer(c *gin.Context, serverAPIHost, method, path, sharedSecret string) {
	proxyBinaryToServer(c, serverAPIHost, method, path, sharedSecret, rawEmailProxyHTTPClient, "raw email")
}

func proxyBinaryToServer(c *gin.Context, serverAPIHost, method, path, sharedSecret string, client *http.Client, resource string) {
	url := fmt.Sprintf("http://%s%s", serverAPIHost, path)
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		serverError(c, ErrCodeExternalFail, "create "+resource+" request: "+err.Error())
		return
	}
	req.Header.Set("X-Internal-Token", sharedSecret)

	resp, err := client.Do(req)
	if err != nil {
		serverError(c, ErrCodeExternalFail, "fetch "+resource+" failed: "+err.Error())
		return
	}
	defer resp.Body.Close()

	for _, header := range []string{
		"Cache-Control",
		"Content-Disposition",
		"Content-Length",
		"Content-Type",
		"X-Content-Type-Options",
	} {
		if value := resp.Header.Get(header); value != "" {
			c.Header(header, value)
		}
	}

	c.Status(resp.StatusCode)
	// 状态码与响应头已写出，body 读取错误无法回滚；调用方据状态码/字节数判断。
	_, _ = io.Copy(c.Writer, resp.Body)
}
