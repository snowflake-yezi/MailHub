package filter

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
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

func TestValidateConfigRejectsInvalidSyncInterval(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "86401", "invalid"} {
		if err := ValidateConfig(nil, map[string]string{SyncIntervalConfigKey: value}); err == nil {
			t.Fatalf("invalid sync interval %q accepted", value)
		}
	}
	for _, value := range []string{"1", "30", "86400"} {
		if err := ValidateConfig(nil, map[string]string{SyncIntervalConfigKey: value}); err != nil {
			t.Fatalf("valid sync interval %q rejected: %v", value, err)
		}
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

func TestFilterSyncInterval(t *testing.T) {
	tests := []struct {
		name     string
		seconds  int
		expected time.Duration
	}{
		{name: "zero", seconds: 0, expected: time.Hour},
		{name: "negative", seconds: -1, expected: time.Hour},
		{name: "configured", seconds: 30, expected: 30 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := filterSyncInterval(tt.seconds); got != tt.expected {
				t.Fatalf("filter sync interval = %s, want %s", got, tt.expected)
			}
		})
	}
}

func TestStartAutoSyncHandlesNonPositiveInterval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	synced := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"code":0,"data":[]}`)
		synced <- struct{}{}
	}))
	defer server.Close()

	New(ActionPass, "").startAutoSync(ctx, server.URL, 0, "")
	New(ActionPass, "").startAutoSync(ctx, server.URL, -1, "")

	for range 2 {
		select {
		case <-synced:
		case <-time.After(time.Second):
			t.Fatal("initial filter sync did not complete")
		}
	}
}

func TestUpdateConfigResetsRunningSyncInterval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	synced := make(chan time.Time, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"code":0,"data":[]}`)
		synced <- time.Now()
	}))
	defer server.Close()

	engine := New(ActionPass, "")
	engine.startAutoSync(ctx, server.URL, 3600, "")
	select {
	case <-synced:
	case <-time.After(time.Second):
		t.Fatal("initial filter sync did not complete")
	}

	engine.UpdateConfig(map[string]string{SyncIntervalConfigKey: "1"})
	if got := engine.SyncIntervalSeconds(); got != 1 {
		t.Fatalf("sync interval = %d, want 1", got)
	}
	select {
	case <-synced:
	case <-time.After(3 * time.Second):
		t.Fatal("updated filter sync interval did not reset the running ticker")
	}
}

func TestLoadRulesOrdersByPriorityThenID(t *testing.T) {
	engine := New(ActionPass, "")
	engine.LoadRules([]Rule{
		{ID: 30, Name: "later", RuleType: Keyword, Pattern: "sale", Action: ActionBlock, Priority: 30, Enabled: true},
		{ID: 20, Name: "same priority later ID", RuleType: Keyword, Pattern: "sale", Action: ActionFlag, Priority: 10, Enabled: true},
		{ID: 10, Name: "first", RuleType: Keyword, Pattern: "sale", Action: ActionPass, Priority: 10, Enabled: true},
	})

	result := engine.Filter(&EmailMessage{Subject: "summer sale"})
	if result.RuleID != 10 || result.Action != ActionPass {
		t.Fatalf("result = %+v, want rule 10 pass", result)
	}
}

func TestInvalidRegexNeverMatchesAndDoesNotHideLaterRules(t *testing.T) {
	engine := New(ActionPass, "")
	engine.LoadRules([]Rule{
		{ID: 1, Name: "invalid", RuleType: Regex, Pattern: "(", Action: ActionBlock, Priority: 1, Enabled: true},
		{ID: 2, Name: "valid fallback", RuleType: Keyword, Pattern: "sale", Action: ActionFlag, Priority: 2, Enabled: true},
	})

	result := engine.Filter(&EmailMessage{Subject: "summer sale"})
	if result.RuleID != 2 || result.Action != ActionFlag {
		t.Fatalf("result = %+v, want later valid rule", result)
	}
}

func TestLegacySenderSubstringBehaviorIsFrozenForMigrationReplay(t *testing.T) {
	if !matchSender("@trusted.example", "Sender <notice@trusted.example>") {
		t.Fatal("legacy domain pattern did not match the expected sender")
	}
	if !matchSender("@trusted.example", "Sender <notice@trusted.example.attacker>") {
		t.Fatal("legacy substring baseline changed before strict matching has shadow evidence")
	}
}
