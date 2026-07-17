package filter

import (
	"sync"
	"testing"
)

func TestInvalidStartupActionFallsBackToPass(t *testing.T) {
	engine := New(Action("drop"), "")
	if got := engine.Filter(&EmailMessage{}).Action; got != ActionPass {
		t.Fatalf("invalid startup action = %q, want pass", got)
	}
}

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
		t.Fatalf("flag prefix = %q, want [new]", got)
	}
}

func TestUpdateConfigAllowsEmptyFlagPrefix(t *testing.T) {
	engine := New(ActionFlag, "[old]")
	engine.UpdateConfig(map[string]string{"filter.flag_subject_prefix": ""})
	if got := engine.GetFlagPrefix(); got != "" {
		t.Fatalf("flag prefix = %q, want empty", got)
	}
}

func TestValidateConfigRejectsInvalidDefaultAction(t *testing.T) {
	if err := ValidateConfig(nil, map[string]string{"filter.default_action": "drop"}); err == nil {
		t.Fatal("invalid default action accepted")
	}
}

func TestConfigUpdateIsConcurrentWithFiltering(t *testing.T) {
	engine := New(ActionPass, "[old]")
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		for i := 0; i < 1000; i++ {
			engine.UpdateConfig(map[string]string{
				"filter.default_action":      "flag",
				"filter.flag_subject_prefix": "[new]",
			})
			engine.UpdateConfig(map[string]string{
				"filter.default_action":      "pass",
				"filter.flag_subject_prefix": "[old]",
			})
		}
	}()
	go func() {
		defer workers.Done()
		for i := 0; i < 1000; i++ {
			action := engine.Filter(&EmailMessage{}).Action
			if action != ActionPass && action != ActionFlag {
				t.Errorf("unexpected action %q", action)
				return
			}
			_ = engine.GetFlagPrefix()
		}
	}()
	workers.Wait()
}
