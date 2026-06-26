package filter

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
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

// Rule 一条过滤规则
type Rule struct {
	ID       uint64   `json:"id"`
	Name     string   `json:"name"`
	RuleType RuleType `json:"rule_type"`
	Pattern  string   `json:"pattern"`
	Action   Action   `json:"action"`
	Priority int      `json:"priority"`
	Enabled  bool     `json:"enabled"`
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
	Action  Action `json:"action"`
	Reason  string `json:"reason"`
	RuleID  uint64 `json:"rule_id,omitempty"`
}

// Engine 过滤引擎（线程安全）
type Engine struct {
	mu            sync.RWMutex
	rules         []Rule
	defaultAction Action
	flagPrefix    string
}

// New 创建过滤引擎
func New(defaultAction Action, flagPrefix string) *Engine {
	return &Engine{
		defaultAction: defaultAction,
		flagPrefix:    flagPrefix,
	}
}

// LoadRules 加载规则
func (e *Engine) LoadRules(rules []Rule) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 预编译 regex 类型的规则
	for i := range rules {
		if rules[i].RuleType == Regex {
			compiled, err := regexp.Compile(rules[i].Pattern)
			if err == nil {
				rules[i].compiled = compiled
			}
		}
	}
	e.rules = rules
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
	return e.flagPrefix
}

// SyncFromManager 从管理系统拉取最新规则
func (e *Engine) SyncFromManager(managerURL string) error {
	url := fmt.Sprintf("%s/api/v1/internal/filters", managerURL)
	resp, err := http.Get(url)
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

// StartAutoSync 启动定时同步
func (e *Engine) StartAutoSync(managerURL string, intervalSec int) {
	go func() {
		ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
		defer ticker.Stop()

		// 启动时立即同步一次
		if err := e.SyncFromManager(managerURL); err != nil {
			fmt.Printf("filter sync failed: %v\n", err)
		} else {
			fmt.Printf("filter synced: %d rules loaded\n", len(e.rules))
		}

		for range ticker.C {
			if err := e.SyncFromManager(managerURL); err != nil {
				fmt.Printf("filter sync failed: %v\n", err)
			}
		}
	}()
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
