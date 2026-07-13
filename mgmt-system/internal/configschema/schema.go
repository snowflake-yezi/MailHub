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
	Label           string
	ValueType       string
	Min             int
	Max             int
	NodeOverridable bool
	ApplyStrategy   ApplyStrategy
}

var definitions = map[string]Definition{
	"forward.scan_interval": {
		Key: "forward.scan_interval", Owner: "mail-node", Label: "扫描间隔", ValueType: "int",
		Min: 1, Max: 3600, ApplyStrategy: ReloadHook,
	},
	"forward.max_email_size": {
		Key: "forward.max_email_size", Owner: "mail-node", Label: "最大邮件大小", ValueType: "int",
		Min: 1024, Max: 1073741824, ApplyStrategy: ReadThrough,
	},
	"forward.body_preview_size": {
		Key: "forward.body_preview_size", Owner: "mail-node", Label: "正文预览大小", ValueType: "int",
		Min: 1024, Max: 10485760, ApplyStrategy: ReadThrough,
	},
	"lifecycle.trash_retention_hours": {
		Key:             "lifecycle.trash_retention_hours",
		Owner:           "mail-node",
		Label:           "回收站保留时间",
		ValueType:       "int",
		Min:             1,
		Max:             8760,
		NodeOverridable: true,
		ApplyStrategy:   ReadThrough,
	},
	"lifecycle.gc_interval_minutes": {
		Key: "lifecycle.gc_interval_minutes", Owner: "mail-node", Label: "GC 执行间隔", ValueType: "int",
		Min: 1, Max: 10080, ApplyStrategy: ReloadHook,
	},
	"lifecycle.drain_timeout_minutes": {
		Key: "lifecycle.drain_timeout_minutes", Owner: "mail-node", Label: "排空超时", ValueType: "int",
		Min: 1, Max: 1440, ApplyStrategy: ReadThrough,
	},
	"lifecycle.drain_poll_interval_ms": {
		Key: "lifecycle.drain_poll_interval_ms", Owner: "mail-node", Label: "排空轮询间隔", ValueType: "int",
		Min: 10, Max: 60000, ApplyStrategy: ReadThrough,
	},
}

func Get(key string) (Definition, bool) {
	definition, ok := definitions[key]
	return definition, ok
}

func NodeOverrides() []Definition {
	result := make([]Definition, 0, len(definitions))
	for _, definition := range definitions {
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
