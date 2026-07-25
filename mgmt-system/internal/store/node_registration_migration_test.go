package store

import (
	"testing"
	"time"

	"github.com/ticket/email-mgmt-system/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type legacyMailServer struct {
	ID                uint64 `gorm:"primaryKey;autoIncrement"`
	Name              string `gorm:"size:128;not null"`
	APIHost           string `gorm:"size:255;not null"`
	SMTPHost          string `gorm:"size:255;not null"`
	IMAPHost          string `gorm:"size:255;not null"`
	PublicHost        string `gorm:"size:255"`
	Capacity          int    `gorm:"not null;default:5000"`
	CurrentLoad       int    `gorm:"not null;default:0"`
	Status            string `gorm:"default:healthy"`
	LastHeartbeat     *time.Time
	HeartbeatInterval int `gorm:"not null;default:30"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (legacyMailServer) TableName() string { return "mail_servers" }

// sqliteMailServerMigration mirrors the NR-P1 additive columns while keeping
// the pre-existing MySQL ENUM status out of this dialect-specific test.
type sqliteMailServerMigration struct {
	ID                 uint64     `gorm:"primaryKey;autoIncrement"`
	NodeUUID           *string    `gorm:"type:char(36);uniqueIndex:uk_mail_server_node_uuid"`
	EnrollmentState    string     `gorm:"size:24;not null;default:legacy_approved;index"`
	ConnectionState    string     `gorm:"size:24;not null;default:unknown;index"`
	ReadinessState     string     `gorm:"size:24;not null;default:unknown;index"`
	AllocationState    string     `gorm:"size:24;not null;default:active;index"`
	TransportMode      string     `gorm:"size:24;not null;default:legacy_http;index"`
	LeaseExpiresAt     *time.Time `gorm:"index"`
	AgentVersion       string     `gorm:"size:64"`
	ProtocolVersion    string     `gorm:"size:32"`
	Capabilities       []string   `gorm:"column:capabilities_json;serializer:json;type:text"`
	MailPublicIPs      []string   `gorm:"column:mail_public_ips_json;serializer:json;type:text"`
	LastConnectedAt    *time.Time
	LastDisconnectedAt *time.Time
}

func (sqliteMailServerMigration) TableName() string { return "mail_servers" }

func TestNodeRegistrationSchemaMigratesLegacyServersWithoutChangingAddresses(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&legacyMailServer{}); err != nil {
		t.Fatal(err)
	}
	legacyRows := []legacyMailServer{
		{Name: "healthy-node", APIHost: "10.0.0.1:8081", SMTPHost: "smtp-a.example", IMAPHost: "imap-a.example", PublicHost: "mail-a.example", Capacity: 10, CurrentLoad: 2, Status: "healthy"},
		{Name: "draining-node", APIHost: "10.0.0.2:8081", SMTPHost: "smtp-b.example", IMAPHost: "imap-b.example", PublicHost: "mail-b.example", Capacity: 10, CurrentLoad: 3, Status: "draining"},
	}
	if err := db.Create(&legacyRows).Error; err != nil {
		t.Fatal(err)
	}

	registrationModels := append([]any{&sqliteMailServerMigration{}}, nodeRegistrationMigrationModels()...)
	if err := db.AutoMigrate(registrationModels...); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	if err := backfillLegacyNodeStates(db); err != nil {
		t.Fatalf("legacy state backfill: %v", err)
	}
	if err := db.AutoMigrate(registrationModels...); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}

	var servers []model.MailServer
	if err := db.Order("id ASC").Find(&servers).Error; err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 {
		t.Fatalf("server count = %d", len(servers))
	}
	if servers[0].NodeUUID != nil || servers[1].NodeUUID != nil {
		t.Fatal("legacy rows must keep a NULL node_uuid")
	}
	if servers[0].EnrollmentState != model.EnrollmentLegacyApproved ||
		servers[0].ConnectionState != model.ConnectionUnknown ||
		servers[0].ReadinessState != model.ReadinessReady ||
		servers[0].AllocationState != model.AllocationActive ||
		servers[0].TransportMode != model.TransportLegacyHTTP {
		t.Fatalf("healthy legacy state = %+v", servers[0])
	}
	if servers[1].ReadinessState != model.ReadinessReady || servers[1].AllocationState != model.AllocationDraining {
		t.Fatalf("draining legacy state = %+v", servers[1])
	}
	if servers[0].APIHost != legacyRows[0].APIHost || servers[0].SMTPHost != legacyRows[0].SMTPHost ||
		servers[0].IMAPHost != legacyRows[0].IMAPHost || servers[0].PublicHost != legacyRows[0].PublicHost ||
		servers[0].Capacity != legacyRows[0].Capacity || servers[0].CurrentLoad != legacyRows[0].CurrentLoad {
		t.Fatalf("legacy server fields changed: %+v", servers[0])
	}
	legacyStore := &Store{db: db}
	selected, err := legacyStore.GetHealthyServerWithMinLoad()
	if err != nil || selected.ID != servers[0].ID {
		t.Fatalf("legacy allocation result = %+v, error = %v", selected, err)
	}

	if err := db.Model(&sqliteMailServerMigration{}).Where("id = ?", servers[0].ID).Updates(map[string]any{
		"readiness_state": "", "allocation_state": model.AllocationDisabled,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillLegacyNodeStates(db); err != nil {
		t.Fatalf("repeat legacy state backfill: %v", err)
	}
	var preserved sqliteMailServerMigration
	if err := db.First(&preserved, servers[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if preserved.ReadinessState != model.ReadinessReady || preserved.AllocationState != model.AllocationDisabled {
		t.Fatalf("repeat backfill state = %+v", preserved)
	}

	for _, table := range []any{
		&model.NodeEnrollmentToken{}, &model.NodeEnrollmentRequest{}, &model.NodeCredential{}, &model.NodeCommand{}, &model.NodeRegistrationAudit{},
	} {
		if !db.Migrator().HasTable(table) {
			t.Errorf("missing table for %T", table)
		}
	}
	if !db.Migrator().HasIndex(&model.MailServer{}, "uk_mail_server_node_uuid") {
		t.Error("missing nullable node_uuid unique index")
	}
	if !db.Migrator().HasIndex(&model.NodeCommand{}, "uk_node_command_sequence") ||
		!db.Migrator().HasIndex(&model.NodeCommand{}, "uk_node_command_idempotency") {
		t.Error("missing node command uniqueness indexes")
	}
	for _, column := range []string{"capabilities_json", "mail_public_ips_json"} {
		if !db.Migrator().HasColumn(&sqliteMailServerMigration{}, column) {
			t.Errorf("missing mail_servers.%s", column)
		}
	}
	for _, column := range []string{"payload_json", "result_json"} {
		if !db.Migrator().HasColumn(&model.NodeCommand{}, column) {
			t.Errorf("missing node_commands.%s", column)
		}
	}

	nodeUUID := "6ba7b810-9dad-41d1-80b4-00c04fd430c8"
	if err := db.Model(&sqliteMailServerMigration{}).Where("id = ?", servers[0].ID).Update("node_uuid", nodeUUID).Error; err != nil {
		t.Fatalf("assign first node UUID: %v", err)
	}
	if err := db.Model(&sqliteMailServerMigration{}).Where("id = ?", servers[1].ID).Update("node_uuid", nodeUUID).Error; err == nil {
		t.Fatal("duplicate non-NULL node_uuid was accepted")
	}
}
