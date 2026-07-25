package store

import (
	"testing"
	"time"

	"github.com/ticket/email-mgmt-system/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestNodeSessionStateFollowsLeaseWithoutChangingSchedulingIntent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE mail_servers (
		id INTEGER PRIMARY KEY, node_uuid TEXT, enrollment_state TEXT, connection_state TEXT,
		readiness_state TEXT, allocation_state TEXT, transport_mode TEXT, lease_expires_at DATETIME,
		agent_version TEXT, protocol_version TEXT, capabilities_json TEXT, last_connected_at DATETIME,
		last_disconnected_at DATETIME, last_heartbeat DATETIME, last_boot_id TEXT, last_started_at DATETIME,
		current_load INTEGER, desired_revision INTEGER, applied_revision INTEGER, last_apply_error TEXT,
		status TEXT, updated_at DATETIME
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO mail_servers
		(id, enrollment_state, connection_state, readiness_state, allocation_state, transport_mode,
		 current_load, desired_revision, applied_revision, last_apply_error, status)
		VALUES (?, ?, ?, ?, ?, ?, 0, 4, 0, '', 'down')`,
		7, model.EnrollmentApproved, model.ConnectionDisconnected, model.ReadinessUnknown,
		model.AllocationDisabled, model.TransportControlStream).Error; err != nil {
		t.Fatal(err)
	}
	store := &Store{db: db}
	now := time.Now().UTC()
	lease := now.Add(time.Minute)
	if err := store.UpdateNodeSessionConnected(7, lease, "agent-1", 1, []string{"heartbeat.v1"}, "boot-1", now.Add(-time.Minute), 2); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateNodeControlHeartbeat(7, lease, 11, model.ReadinessReady, 4, "", "boot-1"); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetServer(7)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ConnectionState != model.ConnectionConnected || loaded.ReadinessState != model.ReadinessReady ||
		loaded.CurrentLoad != 11 || loaded.Status != "healthy" || loaded.AllocationState != model.AllocationDisabled {
		t.Fatalf("connected server = %+v", loaded)
	}
	if err := store.ExpireNodeControlLeases(lease.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.GetServer(7)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ConnectionState != model.ConnectionDisconnected || loaded.Status != "down" ||
		loaded.EnrollmentState != model.EnrollmentApproved || loaded.AllocationState != model.AllocationDisabled {
		t.Fatalf("expired server = %+v", loaded)
	}
}
