package configstate

import (
	"testing"
	"time"

	"github.com/ticket/email-mgmt-system/internal/configschema"
	"github.com/ticket/email-mgmt-system/internal/model"
)

func TestResolveHotApplyStates(t *testing.T) {
	now := time.Now()
	definition := configschema.Definition{ApplyStrategy: configschema.ReadThrough}
	base := model.MailServer{DesiredRevision: 2, AppliedRevision: 1, ConfigChangedAt: &now}
	snapshot := &model.ServerConfigSnapshot{EffectiveValue: "24", Source: "global", AppliedRevision: 1}

	tests := []struct {
		name string
		srv  model.MailServer
		want string
	}{
		{"pending apply", base, PendingApply},
		{"pending retry", withReloadError(base), PendingRetry},
		{"apply failed", withApplyError(base), ApplyFailed},
	}
	for _, tc := range tests {
		if got := Resolve(tc.srv, snapshot, definition, "48", "global", now, 15*time.Minute); got != tc.want {
			t.Fatalf("%s = %s, want %s", tc.name, got, tc.want)
		}
	}

	base.AppliedRevision = 2
	snapshot.AppliedRevision = 2
	snapshot.EffectiveValue = "48"
	if got := Resolve(base, snapshot, definition, "48", "global", now, 15*time.Minute); got != Applied {
		t.Fatalf("applied state = %s", got)
	}
}

func TestResolveRestartStates(t *testing.T) {
	now := time.Now()
	changedAt := now.Add(-20 * time.Minute)
	definition := configschema.Definition{ApplyStrategy: configschema.RestartProcess}
	server := model.MailServer{DesiredRevision: 2, AppliedRevision: 1, LastBootID: "boot-a", BootIDAtChange: "boot-a", ConfigChangedAt: &changedAt}
	snapshot := &model.ServerConfigSnapshot{EffectiveValue: "old", Source: "global", AppliedRevision: 1, BootID: "boot-a"}
	if got := Resolve(server, snapshot, definition, "new", "global", now, 15*time.Minute); got != RestartOverdue {
		t.Fatalf("overdue state = %s", got)
	}
	server.LastBootID = "boot-b"
	if got := Resolve(server, snapshot, definition, "new", "global", now, 15*time.Minute); got != RestartDetected {
		t.Fatalf("restart detected state = %s", got)
	}
	server.AppliedRevision = 2
	snapshot.AppliedRevision = 2
	snapshot.EffectiveValue = "new"
	snapshot.BootID = "boot-b"
	if got := Resolve(server, snapshot, definition, "new", "global", now, 15*time.Minute); got != Applied {
		t.Fatalf("restarted applied state = %s", got)
	}
}

func withReloadError(server model.MailServer) model.MailServer {
	server.LastReloadError = "connection refused"
	return server
}

func withApplyError(server model.MailServer) model.MailServer {
	server.LastApplyError = "invalid config"
	return server
}
