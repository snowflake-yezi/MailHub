package filter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RuleType 规则类型
type RuleType string

const (
	WhitelistSender RuleType = "whitelist_sender"
	BlacklistSender RuleType = "blacklist_sender"
	Keyword         RuleType = "keyword"
	Regex           RuleType = "regex"
)

// Action 匹配后的动作
type Action string

const (
	ActionPass  Action = "pass"
	ActionBlock Action = "block"
	ActionFlag  Action = "flag"
)

const (
	SyncIntervalConfigKey       = "filter.sync_interval"
	minSyncIntervalSeconds      = 1
	maxSyncIntervalSeconds      = 24 * 60 * 60
	defaultSyncIntervalDuration = time.Hour
)

// Rule 一条过滤规则
type Rule struct {
	ID       uint64         `json:"id"`
	Name     string         `json:"name"`
	RuleType RuleType       `json:"rule_type"`
	Pattern  string         `json:"pattern"`
	Action   Action         `json:"action"`
	Priority int            `json:"priority"`
	Enabled  bool           `json:"enabled"`
	compiled *regexp.Regexp `json:"-"` // 预编译的正则（Regex 类型用）
}

// EmailMessage 待过滤的邮件
type EmailMessage struct {
	From    string
	To      string
	Subject string
	Body    string // 纯文本正文
}

// Result 过滤结果
type Result struct {
	Action Action `json:"action"`
	Reason string `json:"reason"`
	RuleID uint64 `json:"rule_id,omitempty"`
}

// Engine 过滤引擎（线程安全）
type Engine struct {
	mu            sync.RWMutex
	rules         []Rule
	defaultAction Action
	flagPrefix    string
	syncMu        sync.Mutex
	syncInterval  time.Duration
	syncReset     chan time.Duration
	authorize     func(*http.Request)
}

// ConfigureAuthorizer sets the outbound management request authenticator.
// A nil authorizer keeps the legacy X-Internal-Token behavior.
func (e *Engine) ConfigureAuthorizer(authorize func(*http.Request)) {
	e.mu.Lock()
	e.authorize = authorize
	e.mu.Unlock()
}

// New 创建过滤引擎
func New(defaultAction Action, flagPrefix string) *Engine {
	if !validAction(defaultAction) {
		defaultAction = ActionPass
	}
	return &Engine{
		defaultAction: defaultAction,
		flagPrefix:    flagPrefix,
	}
}

func validAction(action Action) bool {
	return action == ActionPass || action == ActionBlock || action == ActionFlag
}

// ValidateConfig validates filter values before RemoteConfig commits a revision.
func ValidateConfig(_, next map[string]string) error {
	if value, ok := next["filter.default_action"]; ok && !validAction(Action(value)) {
		return fmt.Errorf("filter.default_action must be pass, block or flag")
	}
	if value, ok := next[SyncIntervalConfigKey]; ok {
		if _, err := parseSyncInterval(value); err != nil {
			return err
		}
	}
	return nil
}

// UpdateConfig applies an already validated remote configuration revision.
func (e *Engine) UpdateConfig(values map[string]string) {
	e.mu.Lock()
	if value, ok := values["filter.default_action"]; ok && validAction(Action(value)) {
		e.defaultAction = Action(value)
	}
	if value, ok := values["filter.flag_subject_prefix"]; ok {
		e.flagPrefix = value
	}
	e.mu.Unlock()

	if value, ok := values[SyncIntervalConfigKey]; ok {
		if seconds, err := parseSyncInterval(value); err == nil {
			e.setSyncInterval(seconds)
		}
	}
}

func parseSyncInterval(value string) (int, error) {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds < minSyncIntervalSeconds || seconds > maxSyncIntervalSeconds {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", SyncIntervalConfigKey, minSyncIntervalSeconds, maxSyncIntervalSeconds)
	}
	return seconds, nil
}

// LoadRules 加载规则
func (e *Engine) LoadRules(rules []Rule) {
	e.mu.Lock()
	defer e.mu.Unlock()

	loaded := append([]Rule(nil), rules...)
	// 预编译 regex 类型的规则
	for i := range loaded {
		if loaded[i].RuleType == Regex {
			compiled, err := regexp.Compile(loaded[i].Pattern)
			if err == nil {
				loaded[i].compiled = compiled
			}
		}
	}
	sort.SliceStable(loaded, func(i, j int) bool {
		if loaded[i].Priority != loaded[j].Priority {
			return loaded[i].Priority < loaded[j].Priority
		}
		return loaded[i].ID < loaded[j].ID
	})
	e.rules = loaded
}

// Filter 执行过滤
func (e *Engine) Filter(msg *EmailMessage) Result {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, rule := range e.rules {
		if !rule.Enabled {
			continue
		}

		var matched bool
		switch rule.RuleType {
		case WhitelistSender:
			matched = matchSender(rule.Pattern, msg.From)
		case BlacklistSender:
			matched = matchSender(rule.Pattern, msg.From)
		case Keyword:
			matched = matchKeyword(rule.Pattern, msg.Subject, msg.Body)
		case Regex:
			if rule.compiled != nil {
				matched = rule.compiled.MatchString(msg.Subject) ||
					rule.compiled.MatchString(msg.Body)
			}
		}

		if matched {
			return Result{
				Action: rule.Action,
				Reason: fmt.Sprintf("matched rule #%d: %s", rule.ID, rule.Name),
				RuleID: rule.ID,
			}
		}
	}

	// 默认动作
	return Result{Action: e.defaultAction, Reason: "default action"}
}

// GetFlagPrefix 获取疑似邮件的标题前缀
func (e *Engine) GetFlagPrefix() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.flagPrefix
}

func (e *Engine) ruleCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.rules)
}

// SyncFromManager 从管理系统拉取最新规则
func (e *Engine) SyncFromManager(managerURL, sharedSecret string) error {
	url := fmt.Sprintf("%s/api/v1/internal/filters", managerURL)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	e.mu.RLock()
	authorize := e.authorize
	e.mu.RUnlock()
	if authorize != nil {
		authorize(req)
	} else {
		req.Header.Set("X-Internal-Token", sharedSecret)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch rules: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var apiResp struct {
		Code int    `json:"code"`
		Data []Rule `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	e.LoadRules(apiResp.Data)
	return nil
}

// StartAutoSync 启动定时同步，并允许后续配置 revision 在线重置周期。
func (e *Engine) StartAutoSync(managerURL string, intervalSec int, sharedSecret string) {
	e.startAutoSync(context.Background(), managerURL, intervalSec, sharedSecret)
}

func (e *Engine) startAutoSync(ctx context.Context, managerURL string, intervalSec int, sharedSecret string) {
	interval := filterSyncInterval(intervalSec)
	e.syncMu.Lock()
	if e.syncInterval > 0 {
		interval = e.syncInterval
	} else {
		e.syncInterval = interval
	}
	if e.syncReset != nil {
		reset := e.syncReset
		e.syncMu.Unlock()
		signalSyncInterval(reset, interval)
		return
	}
	reset := make(chan time.Duration, 1)
	e.syncReset = reset
	e.syncMu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		defer func() {
			e.syncMu.Lock()
			if e.syncReset == reset {
				e.syncReset = nil
			}
			e.syncMu.Unlock()
		}()

		// 启动时立即同步一次
		if err := e.SyncFromManager(managerURL, sharedSecret); err != nil {
			fmt.Printf("filter sync failed: %v\n", err)
		} else {
			fmt.Printf("filter synced: %d rules loaded\n", e.ruleCount())
		}

		for {
			select {
			case <-ctx.Done():
				return
			case interval = <-reset:
				ticker.Reset(interval)
			case <-ticker.C:
				if err := e.SyncFromManager(managerURL, sharedSecret); err != nil {
					fmt.Printf("filter sync failed: %v\n", err)
				}
			}
		}
	}()
}

func filterSyncInterval(intervalSec int) time.Duration {
	if intervalSec <= 0 {
		return defaultSyncIntervalDuration
	}
	return time.Duration(intervalSec) * time.Second
}

func (e *Engine) setSyncInterval(intervalSec int) {
	interval := filterSyncInterval(intervalSec)
	e.syncMu.Lock()
	if e.syncInterval == interval {
		e.syncMu.Unlock()
		return
	}
	e.syncInterval = interval
	reset := e.syncReset
	e.syncMu.Unlock()
	if reset != nil {
		signalSyncInterval(reset, interval)
	}
}

func signalSyncInterval(reset chan time.Duration, interval time.Duration) {
	select {
	case reset <- interval:
	default:
		select {
		case <-reset:
		default:
		}
		select {
		case reset <- interval:
		default:
		}
	}
}

func (e *Engine) SyncIntervalSeconds() int {
	e.syncMu.Lock()
	defer e.syncMu.Unlock()
	if e.syncInterval <= 0 {
		return int(defaultSyncIntervalDuration / time.Second)
	}
	return int(e.syncInterval / time.Second)
}

// ===== 匹配函数 =====

func matchSender(pattern string, from string) bool {
	from = strings.ToLower(from)
	pattern = strings.ToLower(pattern)
	// 支持 @domain 匹配和完整地址匹配
	return strings.Contains(from, pattern)
}

func matchKeyword(pattern string, subject, body string) bool {
	p := strings.ToLower(pattern)
	return strings.Contains(strings.ToLower(subject), p) ||
		strings.Contains(strings.ToLower(body), p)
}
