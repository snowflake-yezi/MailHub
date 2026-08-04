package store

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/ticket/email-mgmt-system/internal/configschema"
	"github.com/ticket/email-mgmt-system/internal/model"
	"gorm.io/gorm"
)

// =============================================================================
// 动态配置 CRUD
// =============================================================================

// ListConfigsByCategory 按分组列出配置项，category 为空则返回全部。
func (s *Store) ListConfigsByCategory(category string) ([]model.SystemConfig, error) {
	var configs []model.SystemConfig
	q := s.db.Order("category, id ASC")
	if category != "" {
		q = q.Where("category = ?", category)
	}
	if err := q.Find(&configs).Error; err != nil {
		return nil, fmt.Errorf("list configs: %w", err)
	}
	return configs, nil
}

// GetConfigByKey 按 key 获取单条配置。
func (s *Store) GetConfigByKey(key string) (*model.SystemConfig, error) {
	var cfg model.SystemConfig
	if err := s.db.Where("config_key = ?", key).First(&cfg).Error; err != nil {
		return nil, err
	}
	return &cfg, nil
}

// SetConfig 更新配置值。
func (s *Store) SetConfig(key, value string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.SystemConfig{}).
			Where("config_key = ?", key).
			Update("config_value", value)
		if result.Error != nil {
			return fmt.Errorf("set config %s: %w", key, result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("config key %s not found", key)
		}
		return s.BumpAllServerDesiredRevisions(tx)
	})
}

// BatchSetConfigs 批量更新（map[key]value）。
func (s *Store) BatchSetConfigs(updates map[string]string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		for key, value := range updates {
			result := tx.Model(&model.SystemConfig{}).
				Where("config_key = ?", key).
				Update("config_value", value)
			if result.Error != nil {
				return fmt.Errorf("batch set %s: %w", key, result.Error)
			}
			if result.RowsAffected == 0 {
				return fmt.Errorf("config key %s not found", key)
			}
		}
		return s.BumpAllServerDesiredRevisions(tx)
	})
}

// ResetConfig 恢复配置为默认值。
func (s *Store) ResetConfig(key string) error {
	var cfg model.SystemConfig
	if err := s.db.Where("config_key = ?", key).First(&cfg).Error; err != nil {
		return err
	}
	return s.SetConfig(key, cfg.DefaultValue)
}

// =============================================================================
// 类型化读取（带缓存 + 默认值回退）
// =============================================================================

// configCache 内存缓存，减少高频读取的 DB 压力。
var (
	configCache   = sync.Map{} // map[string]string
	configCacheTS = sync.Map{} // map[string]time.Time
	cacheTTL      = 30 * time.Second
)

// InvalidateConfigCache 使缓存失效（配置变更后调用）。
func (s *Store) InvalidateConfigCache() {
	configCache.Range(func(key, _ interface{}) bool {
		configCache.Delete(key)
		return true
	})
	configCacheTS.Range(func(key, _ interface{}) bool {
		configCacheTS.Delete(key)
		return true
	})
}

func (s *Store) getCached(key string) (string, bool) {
	ts, ok := configCacheTS.Load(key)
	if !ok {
		return "", false
	}
	if time.Since(ts.(time.Time)) > cacheTTL {
		configCache.Delete(key)
		configCacheTS.Delete(key)
		return "", false
	}
	val, ok := configCache.Load(key)
	if !ok {
		return "", false
	}
	return val.(string), true
}

func (s *Store) setCache(key, val string) {
	configCache.Store(key, val)
	configCacheTS.Store(key, time.Now())
}

// GetConfig 读取字符串配置（缓存 + DB fallback + defaultVal 兜底）。
func (s *Store) GetConfig(key, defaultVal string) string {
	if v, ok := s.getCached(key); ok {
		return v
	}

	var cfg model.SystemConfig
	if err := s.db.Where("config_key = ?", key).First(&cfg).Error; err == nil {
		s.setCache(key, cfg.ConfigValue)
		return cfg.ConfigValue
	}
	return defaultVal
}

// GetConfigInt 读取整数配置。
func (s *Store) GetConfigInt(key string, defaultVal int) int {
	v := s.GetConfig(key, "")
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}

// GetConfigInt64 读取 int64 配置。
func (s *Store) GetConfigInt64(key string, defaultVal int64) int64 {
	v := s.GetConfig(key, "")
	if v == "" {
		return defaultVal
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return defaultVal
	}
	return n
}

// GetConfigBool 读取布尔配置（"true"/"1" 为 true）。
func (s *Store) GetConfigBool(key string, defaultVal bool) bool {
	v := s.GetConfig(key, "")
	if v == "" {
		return defaultVal
	}
	switch v {
	case "true", "1", "yes":
		return true
	case "false", "0", "no", "":
		return false
	default:
		return defaultVal
	}
}

// GetConfigDuration 读取 duration 配置（秒为单位存储，返回 time.Duration）。
func (s *Store) GetConfigDuration(key string, defaultVal time.Duration) time.Duration {
	v := s.GetConfig(key, "")
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return time.Duration(n) * time.Second
}

// =============================================================================
// 种子数据 — 当前所有硬编码值的声明式定义
// =============================================================================

type seedConfig struct {
	Key, Value, Type, Category, Label, Desc, Default string
	Reloadable                                       bool
}

func defaultConfigs() []seedConfig {
	configs := []seedConfig{
		// ── forward（邮件转发引擎）── mail-node ──
		{Key: "forward.scan_interval", Value: "5", Type: "int", Category: "forward", Label: "扫描间隔（秒）", Desc: "Maildir 新邮件扫描频率", Default: "5", Reloadable: true},
		{Key: "forward.max_email_size", Value: "10485760", Type: "int", Category: "forward", Label: "最大邮件大小（字节）", Desc: "单封邮件最大处理字节数，默认 10MB", Default: "10485760", Reloadable: true},
		{Key: "forward.body_preview_size", Value: "65536", Type: "int", Category: "forward", Label: "正文预览大小（字节）", Desc: "过滤时读取的正文预览上限，默认 64KB", Default: "65536", Reloadable: true},
		{Key: "forward.smtp_dial_timeout", Value: "15", Type: "int", Category: "forward", Label: "SMTP 拨号超时（秒）", Desc: "连接 SMTP 服务器的超时时间", Default: "15", Reloadable: false},
		{Key: "forward.tls_insecure_skip", Value: "false", Type: "bool", Category: "forward", Label: "跳过 TLS 证书验证", Desc: "true=跳过 SMTP TLS 证书验证（仅受控自签名环境）", Default: "false", Reloadable: false},
		{Key: "forward.tls_min_version", Value: "12", Type: "int", Category: "forward", Label: "TLS 最低版本", Desc: "SMTP STARTTLS 最低 TLS 版本（12=1.2, 13=1.3）", Default: "12", Reloadable: false},
		{Key: "forward.target_address", Value: "union@example.com", Type: "string", Category: "forward", Label: "转发目标邮箱", Desc: "非垃圾邮件汇总转发的集成邮箱地址（当前生效项，由集成邮箱管理页联动写入）", Default: "union@example.com", Reloadable: true},

		// ── MIME 正文投影 ── mail-node ──
		{Key: "mime.body_projector_mode", Value: "legacy", Type: "string", Category: "mime", Label: "正文投影模式", Desc: "legacy=兼容输出 / shadow=影子比较 / enforce=新投影接管", Default: "legacy", Reloadable: true},
		{Key: "mime.max_message_bytes", Value: "26214400", Type: "int", Category: "mime", Label: "MIME 最大邮件大小（字节）", Desc: "进入 MIME 解析器前允许的原始邮件最大字节数，默认 25 MiB", Default: "26214400", Reloadable: true},

		// ── filter（过滤引擎）── mail-node ──
		{Key: "filter.default_action", Value: "pass", Type: "string", Category: "filter", Label: "默认过滤动作", Desc: "pass=放行转发 / flag=标记后转发 / block=停止转发并保留原件", Default: "pass", Reloadable: true},
		{Key: "filter.flag_subject_prefix", Value: "[疑似]", Type: "string", Category: "filter", Label: "标记邮件标题前缀", Desc: "filter action=flag 时添加的标题前缀", Default: "[疑似]", Reloadable: true},
		{Key: "filter.sync_interval", Value: "30", Type: "int", Category: "filter", Label: "规则同步间隔（秒）", Desc: "节点启动时立即同步，配置重载后在线更新周期；允许 1-86400 秒", Default: "30", Reloadable: true},
		{Key: "filter.engine_mode", Value: "legacy", Type: "string", Category: "filter", Label: "过滤引擎模式", Desc: "legacy=旧引擎 / dual_shadow=双引擎影子判定 / dual_filter=新引擎接管动作", Default: "legacy", Reloadable: true},
		{Key: "filter.auto_quarantine_enabled", Value: "false", Type: "bool", Category: "filter", Label: "自动隔离", Desc: "是否允许广告策略自动隔离邮件；P2 默认关闭", Default: "false", Reloadable: true},
		{Key: "filter.quarantine_base", Value: "/var/mail/mailhub-quarantine", Type: "string", Category: "filter", Label: "隔离目录", Desc: "Maildir 命名空间外的隔离原件目录，修改后需重启节点", Default: "/var/mail/mailhub-quarantine", Reloadable: false},

		// ── lifecycle（生命周期管理）── 双端 ──
		{Key: "lifecycle.trash_retention_hours", Value: "24", Type: "int", Category: "lifecycle", Label: "回收站保留时间（小时）", Desc: "超过此时间的 .trash 目录将被物理清除", Default: "24", Reloadable: true},
		{Key: "lifecycle.message_retention_days", Value: "0", Type: "int", Category: "lifecycle", Label: "节点邮件保留天数（已停用）", Desc: "兼容旧配置保留，邮件清理现统一使用 general.default_retention_days", Default: "0", Reloadable: true},
		{Key: "lifecycle.gc_interval_minutes", Value: "60", Type: "int", Category: "lifecycle", Label: "GC 执行间隔（分钟）", Desc: "回收站垃圾回收执行间隔", Default: "60", Reloadable: true},
		{Key: "lifecycle.drain_timeout_minutes", Value: "5", Type: "int", Category: "lifecycle", Label: "排空超时（分钟）", Desc: "删除前等待活跃转发排空的超时时间", Default: "5", Reloadable: true},
		{Key: "lifecycle.drain_poll_interval_ms", Value: "500", Type: "int", Category: "lifecycle", Label: "排空轮询间隔（毫秒）", Desc: "检查活跃转发数是否归零的轮询间隔", Default: "500", Reloadable: true},
		{Key: "lifecycle.delete_watchdog_minutes", Value: "15", Type: "int", Category: "lifecycle", Label: "删除看门狗超时（分钟）", Desc: "超过此时间的 deleting 任务将被重新下发", Default: "15", Reloadable: false},
		{Key: "lifecycle.schedule_interval_minutes", Value: "5", Type: "int", Category: "lifecycle", Label: "调度间隔（分钟）", Desc: "mgmt-system 生命周期 scheduler 执行间隔", Default: "5", Reloadable: false},

		// ── healthcheck（健康检查）── mgmt-system ──
		{Key: "healthcheck.probe_interval_seconds", Value: "30", Type: "int", Category: "healthcheck", Label: "探测间隔（秒）", Desc: "主动健康探测执行间隔", Default: "30", Reloadable: true},
		{Key: "healthcheck.probe_timeout_seconds", Value: "5", Type: "int", Category: "healthcheck", Label: "探测超时（秒）", Desc: "单次 HTTP 健康探测的超时时间", Default: "5", Reloadable: false},
		{Key: "healthcheck.degrade_threshold", Value: "3", Type: "int", Category: "healthcheck", Label: "降级阈值（次）", Desc: "连续探测失败达到此次数标记为 degraded", Default: "3", Reloadable: true},
		{Key: "healthcheck.down_threshold", Value: "5", Type: "int", Category: "healthcheck", Label: "宕机阈值（次）", Desc: "连续探测失败达到此次数标记为 down", Default: "5", Reloadable: true},
		{Key: "healthcheck.heartbeat_timeout_seconds", Value: "90", Type: "int", Category: "healthcheck", Label: "心跳超时（秒）", Desc: "超过此时间未收到心跳视为异常", Default: "90", Reloadable: true},

		// ── heartbeat（心跳上报）── mail-node ──
		{Key: "heartbeat.interval_seconds", Value: "60", Type: "int", Category: "heartbeat", Label: "心跳间隔（秒）", Desc: "向 mgmt-system 上报心跳的间隔", Default: "60", Reloadable: true},
		{Key: "heartbeat.interval_min", Value: "5", Type: "int", Category: "heartbeat", Label: "心跳最小间隔（秒）", Desc: "心跳间隔的合法下限", Default: "5", Reloadable: false},
		{Key: "heartbeat.interval_max", Value: "600", Type: "int", Category: "heartbeat", Label: "心跳最大间隔（秒）", Desc: "心跳间隔的合法上限", Default: "600", Reloadable: false},
		{Key: "heartbeat.interval_fallback", Value: "60", Type: "int", Category: "heartbeat", Label: "心跳回退间隔（秒）", Desc: "心跳间隔配置无效时的回退值", Default: "60", Reloadable: false},

		// ── session（管理会话）── mgmt-system ──
		{Key: "session.duration_hours", Value: "24", Type: "int", Category: "session", Label: "会话有效期（小时）", Desc: "管理后台登录会话的超时时间", Default: "24", Reloadable: false},
		{Key: "session.gc_interval_minutes", Value: "30", Type: "int", Category: "session", Label: "会话 GC 间隔（分钟）", Desc: "过期会话清理间隔", Default: "30", Reloadable: false},
		{Key: "session.cookie_name", Value: "mgmt_session", Type: "string", Category: "session", Label: "Cookie 名称", Desc: "管理后台 Session Cookie 名称", Default: "mgmt_session", Reloadable: false},
		{Key: "session.cookie_secure", Value: "false", Type: "bool", Category: "session", Label: "Cookie Secure 标志", Desc: "true=仅 HTTPS 传输 Cookie（生产环境应开启）", Default: "false", Reloadable: false},
		{Key: "session.token_bytes", Value: "32", Type: "int", Category: "session", Label: "Token 长度（字节）", Desc: "Session Token 随机字节数", Default: "32", Reloadable: false},

		// ── database（数据库连接池）── mgmt-system ──
		{Key: "database.max_open_conns", Value: "25", Type: "int", Category: "database", Label: "最大连接数", Desc: "DB 连接池最大打开连接数", Default: "25", Reloadable: false},
		{Key: "database.max_idle_conns", Value: "5", Type: "int", Category: "database", Label: "最大空闲连接数", Desc: "DB 连接池最大空闲连接数", Default: "5", Reloadable: false},
		{Key: "database.conn_max_lifetime_minutes", Value: "5", Type: "int", Category: "database", Label: "连接最大存活时间（分钟）", Desc: "DB 连接最大存活时间", Default: "5", Reloadable: false},

		// ── maildir（邮件存储）── mail-node ──
		{Key: "maildir.vmail_uid", Value: "5000", Type: "int", Category: "maildir", Label: "虚拟用户 UID", Desc: "Maildir 文件属主 UID", Default: "5000", Reloadable: false},
		{Key: "maildir.vmail_gid", Value: "5000", Type: "int", Category: "maildir", Label: "虚拟用户 GID", Desc: "Maildir 文件属组 GID", Default: "5000", Reloadable: false},

		// ── general（通用参数）── 双端 ──
		{Key: "general.default_retention_days", Value: "30", Type: "int", Category: "general", Label: "全局邮件保留天数", Desc: "对全部现有及新邮箱生效；保存后由下一轮生命周期调度按邮件文件时间清理，无需重启", Default: "30", Reloadable: true},
		{Key: "general.default_page_size", Value: "20", Type: "int", Category: "general", Label: "默认分页大小", Desc: "列表 API 默认每页条数", Default: "20", Reloadable: true},
		{Key: "general.max_page_size", Value: "100", Type: "int", Category: "general", Label: "最大分页大小", Desc: "列表 API 最大每页条数", Default: "100", Reloadable: true},
		{Key: "general.password_min_length", Value: "6", Type: "int", Category: "general", Label: "密码最小长度", Desc: "邮箱密码最小字符数", Default: "6", Reloadable: false},
		{Key: "general.password_length", Value: "16", Type: "int", Category: "general", Label: "生成密码长度", Desc: "自动生成密码的字符数", Default: "16", Reloadable: false},
		{Key: "general.default_server_capacity", Value: "5000", Type: "int", Category: "general", Label: "默认服务器容量", Desc: "新注册 mail-node 的默认邮箱容量", Default: "5000", Reloadable: true},
		{Key: "general.default_dkim_selector", Value: "mail", Type: "string", Category: "general", Label: "默认 DKIM 选择器", Desc: "新域名的默认 DKIM selector", Default: "mail", Reloadable: false},
	}
	for i := range configs {
		if definition, ok := configschema.Get(configs[i].Key); ok {
			configs[i].Reloadable = definition.Reloadable()
		}
	}
	return configs
}

// SeedDefaultConfigs 种子数据：INSERT 不存在的配置项，更新已存在项的 default_value/description（不覆盖当前值）。
func (s *Store) SeedDefaultConfigs() error {
	for _, sc := range defaultConfigs() {
		var existing model.SystemConfig
		err := s.db.Where("config_key = ?", sc.Key).First(&existing).Error
		if err != nil {
			// 不存在 → 插入
			cfg := model.SystemConfig{
				ConfigKey:    sc.Key,
				ConfigValue:  sc.Value,
				ValueType:    sc.Type,
				Category:     sc.Category,
				Label:        sc.Label,
				Description:  sc.Desc,
				DefaultValue: sc.Default,
				Reloadable:   sc.Reloadable,
			}
			if createErr := s.db.Create(&cfg).Error; createErr != nil {
				return fmt.Errorf("seed config %s: %w", sc.Key, createErr)
			}
		} else {
			// 已存在 → 更新元数据（不覆盖当前值）
			updates := map[string]interface{}{
				"value_type":    sc.Type,
				"category":      sc.Category,
				"label":         sc.Label,
				"description":   sc.Desc,
				"default_value": sc.Default,
				"reloadable":    sc.Reloadable,
			}
			if updateErr := s.db.Model(&existing).Updates(updates).Error; updateErr != nil {
				return fmt.Errorf("update config meta %s: %w", sc.Key, updateErr)
			}
		}
	}
	return nil
}
