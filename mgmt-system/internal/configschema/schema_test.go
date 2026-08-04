package configschema

import (
	"reflect"
	"testing"
)

func TestTrashRetentionSchema(t *testing.T) {
	definition, ok := Get("lifecycle.trash_retention_hours")
	if !ok {
		t.Fatal("trash retention definition missing")
	}
	if definition.Owner != "mail-node" || !definition.NodeOverridable {
		t.Fatalf("definition ownership = %#v", definition)
	}
	if definition.ApplyStrategy != ReadThrough || !definition.Reloadable() || definition.RequiresRestart() {
		t.Fatalf("definition apply capability = %#v", definition)
	}
}

func TestRuntimeConfigApplyStrategies(t *testing.T) {
	tests := map[string]ApplyStrategy{
		"forward.scan_interval":            ReloadHook,
		"forward.max_email_size":           ReadThrough,
		"forward.body_preview_size":        ReadThrough,
		"forward.target_address":           ReadThrough,
		"forward.smtp_dial_timeout":        ReadThrough,
		"forward.tls_insecure_skip":        ReadThrough,
		"forward.tls_min_version":          ReadThrough,
		"filter.sync_interval":             ReloadHook,
		"filter.engine_mode":               ReadThrough,
		"filter.auto_quarantine_enabled":   ReadThrough,
		"lifecycle.gc_interval_minutes":    ReloadHook,
		"lifecycle.drain_timeout_minutes":  ReadThrough,
		"lifecycle.drain_poll_interval_ms": ReadThrough,
	}
	for key, want := range tests {
		definition, ok := Get(key)
		if !ok {
			t.Fatalf("definition %s missing", key)
		}
		if definition.ApplyStrategy != want || !definition.Reloadable() || definition.RequiresRestart() {
			t.Fatalf("definition %s = %#v, want strategy %s", key, definition, want)
		}
		if !definition.NodeOverridable {
			t.Fatalf("definition %s is not exposed as node override", key)
		}
	}
}

func TestTLSVerificationIsSecureByDefault(t *testing.T) {
	definition, ok := Get("forward.tls_insecure_skip")
	if !ok {
		t.Fatal("TLS verification definition missing")
	}
	if definition.DefaultValue != "false" {
		t.Fatalf("TLS insecure default = %q, want false", definition.DefaultValue)
	}
}

func TestMIMEProjectorSchemaContract(t *testing.T) {
	mode, ok := Get("mime.body_projector_mode")
	if !ok || mode.Owner != "mail-node" || mode.DefaultValue != "legacy" || mode.ApplyStrategy != ReadThrough || !mode.NodeOverridable {
		t.Fatalf("mode definition = %#v", mode)
	}
	wantModes := []string{"legacy", "shadow", "enforce"}
	if !reflect.DeepEqual(mode.AllowedValues, wantModes) {
		t.Fatalf("mode allowed values = %v, want %v", mode.AllowedValues, wantModes)
	}
	maxBytes, ok := Get("mime.max_message_bytes")
	if !ok || maxBytes.Min != 1048576 || maxBytes.Max != 1073741824 || maxBytes.DefaultValue != "26214400" {
		t.Fatalf("max bytes definition = %#v", maxBytes)
	}
}

func TestQuarantineBaseRequiresRestart(t *testing.T) {
	definition, ok := Get("filter.quarantine_base")
	if !ok {
		t.Fatal("quarantine base definition missing")
	}
	if definition.ApplyStrategy != RestartProcess || definition.Reloadable() || !definition.RequiresRestart() {
		t.Fatalf("quarantine base apply capability = %#v", definition)
	}
}

func TestNodeOverridesAreStableAndComplete(t *testing.T) {
	definitions := NodeOverrides()
	expectedKeys := make([]string, 0, len(definitionOrder))
	for _, key := range definitionOrder {
		if definition, ok := Get(key); ok && definition.NodeOverridable {
			expectedKeys = append(expectedKeys, key)
		}
	}
	if len(definitions) != len(expectedKeys) {
		t.Fatalf("NodeOverrides() returned %d definitions, want %d", len(definitions), len(expectedKeys))
	}
	for index, definition := range definitions {
		if definition.Key != expectedKeys[index] {
			t.Fatalf("NodeOverrides()[%d].Key = %q, want %q", index, definition.Key, expectedKeys[index])
		}
		if definition.Category == "" || definition.Description == "" || definition.DefaultValue == "" || definition.Unit == "" {
			t.Fatalf("definition %s has incomplete UI metadata: %#v", definition.Key, definition)
		}
	}
}
