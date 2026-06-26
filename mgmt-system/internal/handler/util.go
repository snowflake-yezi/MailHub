package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
)

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
		return nil, fmt.Errorf("upstream error: %d - %s", resp.StatusCode, string(data))
	}

	return data, nil
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
