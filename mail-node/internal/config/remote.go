package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RemoteConfig 从 mgmt-system 拉取的动态配置（线程安全）。
type RemoteConfig struct {
	mu      sync.RWMutex
	configs map[string]string
	mgmtURL string
	secret  string
	nodeID  uint64
	sources map[string]string
}

// NewRemoteConfig 创建远程配置客户端。mgmtURL 为 mgmt-system 地址（不含路径），secret 为共享密钥。
func NewRemoteConfig(mgmtURL, secret string, nodeID ...uint64) *RemoteConfig {
	id := uint64(0)
	if len(nodeID) > 0 {
		id = nodeID[0]
	}
	return &RemoteConfig{
		configs: make(map[string]string),
		sources: make(map[string]string),
		mgmtURL: mgmtURL,
		secret:  secret,
		nodeID:  id,
	}
}

// PullAll 从 mgmt-system 拉取全量配置并替换本地缓存。
func (rc *RemoteConfig) PullAll() error {
	url := fmt.Sprintf("%s/api/v1/internal/configs", rc.mgmtURL)
	if rc.nodeID != 0 {
		url += "?server_id=" + strconv.FormatUint(rc.nodeID, 10)
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-Internal-Token", rc.secret)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("pull configs: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var apiResp struct {
		Code int `json:"code"`
		Data struct {
			Configs map[string]string `json:"configs"`
			Sources map[string]string `json:"sources"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if apiResp.Code != 0 {
		return fmt.Errorf("mgmt error code %d", apiResp.Code)
	}

	rc.mu.Lock()
	rc.configs = apiResp.Data.Configs
	rc.sources = apiResp.Data.Sources
	rc.mu.Unlock()

	return nil
}

// Source returns where the effective remote value came from.
func (rc *RemoteConfig) Source(key string) string {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	if source := rc.sources[key]; source != "" {
		return source
	}
	return "local_config"
}

// ReportSnapshot confirms the value actually applied by this process.
func (rc *RemoteConfig) ReportSnapshot(key, value string, appliedAt time.Time) error {
	if rc.nodeID == 0 {
		return nil
	}
	payload := struct {
		ReportedAt time.Time `json:"reported_at"`
		Items      []struct {
			ConfigKey      string    `json:"config_key"`
			EffectiveValue string    `json:"effective_value"`
			Source         string    `json:"source"`
			AppliedAt      time.Time `json:"applied_at"`
		} `json:"items"`
	}{ReportedAt: time.Now().UTC()}
	payload.Items = append(payload.Items, struct {
		ConfigKey      string    `json:"config_key"`
		EffectiveValue string    `json:"effective_value"`
		Source         string    `json:"source"`
		AppliedAt      time.Time `json:"applied_at"`
	}{key, value, rc.Source(key), appliedAt.UTC()})
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode config snapshot: %w", err)
	}
	url := fmt.Sprintf("%s/api/v1/internal/servers/%d/config-snapshot", rc.mgmtURL, rc.nodeID)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build snapshot request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", rc.secret)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("report config snapshot: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("snapshot status %d", resp.StatusCode)
	}
	return nil
}

// Reload 增量重载配置（从 mgmt-system 拉取变更项）。
func (rc *RemoteConfig) Reload() error {
	// For now, just re-pull all. In the future, mgmt could send changed keys.
	return rc.PullAll()
}

// Configs returns a copy of all configs (for logging/inspection).
func (rc *RemoteConfig) Configs() map[string]string {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	out := make(map[string]string, len(rc.configs))
	for k, v := range rc.configs {
		out[k] = v
	}
	return out
}

// get returns the raw string value for a key.
func (rc *RemoteConfig) get(key string) (string, bool) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	v, ok := rc.configs[key]
	return v, ok
}

// GetString 获取字符串配置，未找到或远程值为空时返回 defaultVal。
func (rc *RemoteConfig) GetString(key, defaultVal string) string {
	if v, ok := rc.get(key); ok && v != "" {
		return v
	}
	return defaultVal
}

// GetInt 获取整数配置。
func (rc *RemoteConfig) GetInt(key string, defaultVal int) int {
	if v, ok := rc.get(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return defaultVal
}

// GetInt64 获取 int64 配置。
func (rc *RemoteConfig) GetInt64(key string, defaultVal int64) int64 {
	if v, ok := rc.get(key); ok && v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return defaultVal
}

// GetBool 获取布尔配置。
func (rc *RemoteConfig) GetBool(key string, defaultVal bool) bool {
	if v, ok := rc.get(key); ok {
		switch v {
		case "true", "1", "yes":
			return true
		case "false", "0", "no":
			return false
		}
	}
	return defaultVal
}

// GetDurationSeconds 获取以秒为单位的时长配置，返回 time.Duration。
func (rc *RemoteConfig) GetDurationSeconds(key string, defaultVal time.Duration) time.Duration {
	if v, ok := rc.get(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	return defaultVal
}

// GetDurationMinutes 获取以分钟为单位的时长配置，返回 time.Duration。
func (rc *RemoteConfig) GetDurationMinutes(key string, defaultVal time.Duration) time.Duration {
	if v, ok := rc.get(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Minute
		}
	}
	return defaultVal
}

// GetDurationHours obtains an integer hour duration.
func (rc *RemoteConfig) GetDurationHours(key string, defaultVal time.Duration) time.Duration {
	if v, ok := rc.get(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Hour
		}
	}
	return defaultVal
}
