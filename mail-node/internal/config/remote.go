package config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RemoteConfig 从 mgmt-system 拉取的动态配置（线程安全）。
type RemoteConfig struct {
	mu              sync.RWMutex
	pullMu          sync.Mutex
	configs         map[string]string
	mgmtURL         string
	secret          string
	nodeUUID        string
	credential      string
	nodeID          uint64
	sources         map[string]string
	desiredRevision uint64
	appliedRevision uint64
	applyHooks      []ApplyFunc
	afterApplyHooks []func(desiredRevision, appliedRevision uint64)
	lastApplyError  string
	bootID          string
	startedAt       time.Time
}

type ApplyFunc func(current, next map[string]string) error

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
	rc.pullMu.Lock()
	defer rc.pullMu.Unlock()
	url := fmt.Sprintf("%s/api/v1/internal/configs", rc.mgmtURL)
	nodeID := rc.NodeID()
	if nodeID != 0 {
		url += "?server_id=" + strconv.FormatUint(nodeID, 10)
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	rc.authorize(req)

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
			Configs         map[string]string `json:"configs"`
			Sources         map[string]string `json:"sources"`
			DesiredRevision uint64            `json:"desired_revision"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if apiResp.Code != 0 {
		return fmt.Errorf("mgmt error code %d", apiResp.Code)
	}

	rc.mu.RLock()
	current := cloneMap(rc.configs)
	currentSources := cloneMap(rc.sources)
	hooks := append([]ApplyFunc(nil), rc.applyHooks...)
	observedDesiredRevision := rc.desiredRevision
	appliedRevision := rc.appliedRevision
	rc.mu.RUnlock()
	if apiResp.Data.DesiredRevision < observedDesiredRevision {
		return fmt.Errorf("stale desired revision %d, already observed %d", apiResp.Data.DesiredRevision, observedDesiredRevision)
	}
	rc.mu.Lock()
	if apiResp.Data.DesiredRevision > rc.desiredRevision {
		rc.desiredRevision = apiResp.Data.DesiredRevision
	}
	rc.mu.Unlock()
	if apiResp.Data.DesiredRevision <= appliedRevision && equalMap(current, apiResp.Data.Configs) && equalMap(currentSources, apiResp.Data.Sources) {
		return nil
	}
	for _, apply := range hooks {
		if err := apply(current, apiResp.Data.Configs); err != nil {
			rc.setLastApplyError(err.Error())
			return fmt.Errorf("apply config: %w", err)
		}
	}

	rc.mu.Lock()
	rc.configs = cloneMap(apiResp.Data.Configs)
	rc.sources = cloneMap(apiResp.Data.Sources)
	rc.desiredRevision = apiResp.Data.DesiredRevision
	rc.appliedRevision = apiResp.Data.DesiredRevision
	rc.lastApplyError = ""
	afterApplyHooks := append([]func(uint64, uint64){}, rc.afterApplyHooks...)
	rc.mu.Unlock()
	for _, afterApply := range afterApplyHooks {
		afterApply(apiResp.Data.DesiredRevision, apiResp.Data.DesiredRevision)
	}

	return nil
}

func (rc *RemoteConfig) NodeID() uint64 {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.nodeID
}

func (rc *RemoteConfig) SetNodeID(nodeID uint64) {
	if nodeID == 0 {
		return
	}
	rc.mu.Lock()
	rc.nodeID = nodeID
	rc.mu.Unlock()
}

func (rc *RemoteConfig) ConfigureNodeCredential(nodeUUID, credential string) {
	rc.mu.Lock()
	rc.nodeUUID = strings.TrimSpace(nodeUUID)
	rc.credential = strings.TrimSpace(credential)
	rc.mu.Unlock()
}

func (rc *RemoteConfig) authorize(request *http.Request) {
	rc.mu.RLock()
	nodeUUID, credential, secret := rc.nodeUUID, rc.credential, rc.secret
	rc.mu.RUnlock()
	if nodeUUID != "" && credential != "" {
		request.Header.Set("Authorization", "Node "+credential)
		request.Header.Set("X-MailHub-Node-UUID", nodeUUID)
		return
	}
	request.Header.Set("X-Internal-Token", secret)
}

// Authorize applies the currently active node credential to an outbound request.
// It is exported so auxiliary clients (filter sync, lifecycle, outbox) use the
// same credential rotation state as the primary config client.
func (rc *RemoteConfig) Authorize(request *http.Request) {
	if rc == nil || request == nil {
		return
	}
	rc.authorize(request)
}

func (rc *RemoteConfig) HasNodeCredential() bool {
	if rc == nil {
		return false
	}
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.nodeUUID != "" && rc.credential != ""
}

func (rc *RemoteConfig) NodeCredential() (string, string) {
	if rc == nil {
		return "", ""
	}
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.nodeUUID, rc.credential
}

func (rc *RemoteConfig) SetBootIdentity(bootID string, startedAt time.Time) {
	rc.mu.Lock()
	rc.bootID = bootID
	rc.startedAt = startedAt.UTC()
	rc.mu.Unlock()
}

func (rc *RemoteConfig) BootIdentity() (string, time.Time) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.bootID, rc.startedAt
}

func (rc *RemoteConfig) setLastApplyError(message string) {
	rc.mu.Lock()
	rc.lastApplyError = message
	rc.mu.Unlock()
}

func (rc *RemoteConfig) LastApplyError() string {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.lastApplyError
}

func cloneMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func equalMap(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func (rc *RemoteConfig) RegisterApplyHook(apply ApplyFunc) {
	if apply == nil {
		return
	}
	rc.mu.Lock()
	rc.applyHooks = append(rc.applyHooks, apply)
	rc.mu.Unlock()
}

func (rc *RemoteConfig) RegisterAfterApplyHook(afterApply func(desiredRevision, appliedRevision uint64)) {
	if afterApply == nil {
		return
	}
	rc.mu.Lock()
	rc.afterApplyHooks = append(rc.afterApplyHooks, afterApply)
	rc.mu.Unlock()
}

func (rc *RemoteConfig) Revisions() (desired, applied uint64) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.desiredRevision, rc.appliedRevision
}

func (rc *RemoteConfig) StartPolling(ctx context.Context, interval time.Duration, onError func(error)) {
	if interval <= 0 {
		interval = time.Minute
	}
	backoff := interval
	for {
		jitterRange := interval / 5
		jitter := time.Duration(0)
		if jitterRange > 0 {
			jitter = time.Duration(rand.Int63n(int64(jitterRange)))
		}
		timer := time.NewTimer(backoff + jitter)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err := rc.Reload(); err != nil {
			rc.setLastApplyError(err.Error())
			if onError != nil {
				onError(err)
			}
			backoff *= 2
			if max := interval * 8; backoff > max {
				backoff = max
			}
			continue
		}
		backoff = interval
	}
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
	return rc.ReportSnapshots(map[string]string{key: value}, appliedAt)
}

// ReportSnapshots confirms a set of values actually applied by this process.
func (rc *RemoteConfig) ReportSnapshots(values map[string]string, appliedAt time.Time) error {
	nodeID := rc.NodeID()
	if nodeID == 0 {
		return nil
	}
	payload := struct {
		ReportedAt      time.Time `json:"reported_at"`
		DesiredRevision uint64    `json:"desired_revision"`
		AppliedRevision uint64    `json:"applied_revision"`
		BootID          string    `json:"boot_id"`
		StartedAt       time.Time `json:"started_at"`
		Items           []struct {
			ConfigKey      string    `json:"config_key"`
			EffectiveValue string    `json:"effective_value"`
			Source         string    `json:"source"`
			AppliedAt      time.Time `json:"applied_at"`
		} `json:"items"`
	}{ReportedAt: time.Now().UTC()}
	payload.DesiredRevision, payload.AppliedRevision = rc.Revisions()
	payload.BootID, payload.StartedAt = rc.BootIdentity()
	for key, value := range values {
		payload.Items = append(payload.Items, struct {
			ConfigKey      string    `json:"config_key"`
			EffectiveValue string    `json:"effective_value"`
			Source         string    `json:"source"`
			AppliedAt      time.Time `json:"applied_at"`
		}{key, value, rc.Source(key), appliedAt.UTC()})
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode config snapshot: %w", err)
	}
	url := fmt.Sprintf("%s/api/v1/internal/servers/%d/config-snapshot", rc.mgmtURL, nodeID)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build snapshot request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	rc.authorize(req)
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
	err := rc.PullAll()
	if err != nil {
		rc.setLastApplyError(err.Error())
	}
	return err
}

// Configs returns a copy of all configs (for logging/inspection).
func (rc *RemoteConfig) Configs() map[string]string {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return cloneMap(rc.configs)
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
