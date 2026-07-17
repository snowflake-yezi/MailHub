package configschema

type ApplyStrategy string

const (
	ReadThrough    ApplyStrategy = "read_through"
	ReloadHook     ApplyStrategy = "reload_hook"
	RestartProcess ApplyStrategy = "restart_process"
	ExternalReload ApplyStrategy = "external_reload"
)

type Definition struct {
	Key             string
	Owner           string
	Category        string
	Label           string
	Description     string
	ValueType       string
	DefaultValue    string
	Unit            string
	Min             int
	Max             int
	NodeOverridable bool
	ApplyStrategy   ApplyStrategy
}

var definitionOrder = []string{
	"forward.scan_interval",
	"forward.max_email_size",
	"forward.body_preview_size",
	"forward.target_address",
	"forward.smtp_dial_timeout",
	"forward.tls_insecure_skip",
	"forward.tls_min_version",
	"filter.sync_interval",
	"lifecycle.trash_retention_hours",
	"lifecycle.message_retention_days",
	"lifecycle.gc_interval_minutes",
	"lifecycle.drain_timeout_minutes",
	"lifecycle.drain_poll_interval_ms",
}

var definitions = map[string]Definition{
	"forward.scan_interval": {
		Key: "forward.scan_interval", Owner: "mail-node", Category: "forward", Label: "扫描间隔", Description: "Maildir 新邮件扫描频率",
		ValueType: "int", DefaultValue: "5", Unit: "秒", Min: 1, Max: 3600, NodeOverridable: true, ApplyStrategy: ReloadHook,
	},
	"forward.max_email_size": {
		Key: "forward.max_email_size", Owner: "mail-node", Category: "forward", Label: "最大邮件大小", Description: "单封邮件最大处理字节数",
		ValueType: "int", DefaultValue: "10485760", Unit: "字节", Min: 1024, Max: 1073741824, NodeOverridable: true, ApplyStrategy: ReadThrough,
	},
	"forward.body_preview_size": {
		Key: "forward.body_preview_size", Owner: "mail-node", Category: "forward", Label: "正文预览大小", Description: "过滤时读取的正文预览上限",
		ValueType: "int", DefaultValue: "65536", Unit: "字节", Min: 1024, Max: 10485760, NodeOverridable: true, ApplyStrategy: ReadThrough,
	},
	"forward.target_address": {
		Key: "forward.target_address", Owner: "mail-node", Category: "forward", Label: "转发目标邮箱", Description: "非垃圾邮件汇总转发的目标邮箱地址",
		ValueType: "string", DefaultValue: "union@asadad.bond", Unit: "邮箱", Min: 3, Max: 191, NodeOverridable: true, ApplyStrategy: ReadThrough,
	},
	"forward.smtp_dial_timeout": {
		Key: "forward.smtp_dial_timeout", Owner: "mail-node", Category: "forward", Label: "SMTP 拨号超时", Description: "连接 SMTP 服务器的超时时间",
		ValueType: "int", DefaultValue: "15", Unit: "秒", Min: 1, Max: 300, NodeOverridable: true, ApplyStrategy: ReadThrough,
	},
	"forward.tls_insecure_skip": {
		Key: "forward.tls_insecure_skip", Owner: "mail-node", Category: "forward", Label: "跳过 TLS 证书验证", Description: "仅在使用受控自签名证书时启用",
		ValueType: "bool", DefaultValue: "false", Unit: "开关", Min: 0, Max: 1, NodeOverridable: true, ApplyStrategy: ReadThrough,
	},
	"forward.tls_min_version": {
		Key: "forward.tls_min_version", Owner: "mail-node", Category: "forward", Label: "TLS 最低版本", Description: "SMTP STARTTLS 最低版本，12 表示 TLS 1.2，13 表示 TLS 1.3",
		ValueType: "int", DefaultValue: "12", Unit: "版本", Min: 12, Max: 13, NodeOverridable: true, ApplyStrategy: ReadThrough,
	},
	"filter.sync_interval": {
		Key: "filter.sync_interval", Owner: "mail-node", Category: "filter", Label: "规则同步间隔", Description: "节点启动时立即同步，配置重载后在线更新周期",
		ValueType: "int", DefaultValue: "30", Unit: "秒", Min: 1, Max: 86400, NodeOverridable: true, ApplyStrategy: ReloadHook,
	},
	"lifecycle.trash_retention_hours": {
		Key: "lifecycle.trash_retention_hours", Owner: "mail-node", Category: "lifecycle",
		Label: "回收站保留时间", Description: "超过此时间的 .trash 目录将被物理清除",
		ValueType: "int", DefaultValue: "24", Unit: "小时",
		Min:             1,
		Max:             8760,
		NodeOverridable: true,
		ApplyStrategy:   ReadThrough,
	},
	"lifecycle.message_retention_days": {
		Key: "lifecycle.message_retention_days", Owner: "mgmt-system", Category: "lifecycle",
		Label: "节点邮件保留天数（已停用）", Description: "兼容旧配置保留，邮件清理统一使用 general.default_retention_days",
		ValueType: "int", DefaultValue: "0", Unit: "天",
		Min:             0,
		Max:             36500,
		NodeOverridable: false,
		ApplyStrategy:   ReadThrough,
	},
	"lifecycle.gc_interval_minutes": {
		Key: "lifecycle.gc_interval_minutes", Owner: "mail-node", Category: "lifecycle", Label: "GC 执行间隔", Description: "回收站垃圾回收执行间隔",
		ValueType: "int", DefaultValue: "60", Unit: "分钟", Min: 1, Max: 10080, NodeOverridable: true, ApplyStrategy: ReloadHook,
	},
	"lifecycle.drain_timeout_minutes": {
		Key: "lifecycle.drain_timeout_minutes", Owner: "mail-node", Category: "lifecycle", Label: "排空超时", Description: "删除前等待活跃转发排空的超时时间",
		ValueType: "int", DefaultValue: "5", Unit: "分钟", Min: 1, Max: 1440, NodeOverridable: true, ApplyStrategy: ReadThrough,
	},
	"lifecycle.drain_poll_interval_ms": {
		Key: "lifecycle.drain_poll_interval_ms", Owner: "mail-node", Category: "lifecycle", Label: "排空轮询间隔", Description: "检查活跃转发数是否归零的轮询间隔",
		ValueType: "int", DefaultValue: "500", Unit: "毫秒", Min: 10, Max: 60000, NodeOverridable: true, ApplyStrategy: ReadThrough,
	},
}

func Get(key string) (Definition, bool) {
	definition, ok := definitions[key]
	return definition, ok
}

func NodeOverrides() []Definition {
	result := make([]Definition, 0, len(definitionOrder))
	for _, key := range definitionOrder {
		definition := definitions[key]
		if definition.NodeOverridable {
			result = append(result, definition)
		}
	}
	return result
}

func (d Definition) Reloadable() bool {
	return d.ApplyStrategy != RestartProcess
}

func (d Definition) RequiresRestart() bool {
	return d.ApplyStrategy == RestartProcess
}
