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
