package configstate

import (
	"time"

	"github.com/ticket/email-mgmt-system/internal/configschema"
	"github.com/ticket/email-mgmt-system/internal/model"
)

const (
	Unreported      = "unreported"
	PendingApply    = "pending_apply"
	PendingRetry    = "pending_retry"
	ApplyFailed     = "apply_failed"
	PendingRestart  = "pending_restart"
	RestartDetected = "restart_detected"
	RestartOverdue  = "restart_overdue"
	Applied         = "applied"
)

func Resolve(server model.MailServer, snapshot *model.ServerConfigSnapshot, definition configschema.Definition, desiredValue, desiredSource string, now time.Time, restartOverdueAfter time.Duration) string {
	matches := snapshot != nil &&
		server.AppliedRevision >= server.DesiredRevision &&
		snapshot.AppliedRevision >= server.DesiredRevision &&
		snapshot.EffectiveValue == desiredValue &&
		snapshot.Source == desiredSource
	if matches {
		if definition.RequiresRestart() && server.BootIDAtChange != "" && snapshot.BootID == server.BootIDAtChange {
			return PendingRestart
		}
		return Applied
	}

	if definition.RequiresRestart() {
		if server.BootIDAtChange != "" && server.LastBootID != "" && server.LastBootID != server.BootIDAtChange {
			return RestartDetected
		}
		if restartOverdueAfter > 0 && server.ConfigChangedAt != nil && now.Sub(*server.ConfigChangedAt) >= restartOverdueAfter {
			return RestartOverdue
		}
		return PendingRestart
	}

	if server.LastApplyError != "" {
		return ApplyFailed
	}
	if server.LastReloadError != "" {
		return PendingRetry
	}
	if snapshot == nil && server.ConfigChangedAt == nil && server.DesiredRevision == 0 {
		return Unreported
	}
	return PendingApply
}
