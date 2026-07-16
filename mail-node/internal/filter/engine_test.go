package filter

import "testing"

func TestUpdateConfigChangesDefaultActionAndFlagPrefix(t *testing.T) {
	engine := New(ActionPass, "[old]")
	next := map[string]string{
		"filter.default_action":      "block",
		"filter.flag_subject_prefix": "[new]",
	}
	if err := ValidateConfig(nil, next); err != nil {
		t.Fatal(err)
	}
	engine.UpdateConfig(next)
	if got := engine.Filter(&EmailMessage{}).Action; got != ActionBlock {
		t.Fatalf("default action = %q, want block", got)
	}
	if got := engine.GetFlagPrefix(); got != "[new]" {
		t.Fatalf("flag prefix = %q", got)
	}
}

func TestValidateConfigRejectsInvalidDefaultAction(t *testing.T) {
	if err := ValidateConfig(nil, map[string]string{"filter.default_action": "drop"}); err == nil {
		t.Fatal("invalid default action accepted")
	}
	if got := New(Action("drop"), "").Filter(&EmailMessage{}).Action; got != ActionPass {
		t.Fatalf("invalid startup action fallback = %q, want pass", got)
	}
}
