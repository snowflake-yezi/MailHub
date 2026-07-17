package configschema

import "testing"

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
