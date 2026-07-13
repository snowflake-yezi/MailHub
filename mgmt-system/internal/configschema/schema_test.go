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
		if definition.NodeOverridable {
			t.Fatalf("definition %s unexpectedly exposed as node override", key)
		}
	}
}
