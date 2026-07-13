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
