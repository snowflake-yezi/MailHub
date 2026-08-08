package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ticket/email-mgmt-system/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const (
	testNodeUUIDA = "6ba7b810-9dad-41d1-80b4-00c04fd430c8"
	testNodeUUIDB = "86bb60b4-6637-4b8d-90f5-b465e305d4e0"
)

type enrollmentTestStore struct{ db *gorm.DB }

// sqliteEnrollmentMailServer mirrors MailServer without its MySQL-only ENUM
// tag so the enrollment transactions run against an actual relational DB in
// unit tests.
type sqliteEnrollmentMailServer struct {
	ID                 uint64     `gorm:"primaryKey;autoIncrement"`
	Name               string     `gorm:"size:128;not null"`
	NodeUUID           *string    `gorm:"type:char(36);uniqueIndex:uk_mail_server_node_uuid"`
	APIHost            string     `gorm:"size:255;not null"`
	SMTPHost           string     `gorm:"size:255;not null"`
	IMAPHost           string     `gorm:"size:255;not null"`
	PublicHost         string     `gorm:"size:255"`
	MailPublicIPs      []string   `gorm:"column:mail_public_ips_json;serializer:json;type:text"`
	Capacity           int        `gorm:"not null;default:5000"`
	CurrentLoad        int        `gorm:"not null;default:0"`
	Status             string     `gorm:"size:24;default:healthy"`
	EnrollmentState    string     `gorm:"size:24;not null;default:legacy_approved;index"`
	ConnectionState    string     `gorm:"size:24;not null;default:unknown;index"`
	ReadinessState     string     `gorm:"size:24;not null;default:unknown;index"`
	AllocationState    string     `gorm:"size:24;not null;default:active;index"`
	TransportMode      string     `gorm:"size:24;not null;default:legacy_http;index"`
	LeaseExpiresAt     *time.Time `gorm:"index"`
	AgentVersion       string     `gorm:"size:64"`
	ProtocolVersion    string     `gorm:"size:32"`
	Capabilities       []string   `gorm:"column:capabilities_json;serializer:json;type:text"`
	LastConnectedAt    *time.Time
	LastDisconnectedAt *time.Time
	LastHeartbeat      *time.Time
	LastProbeAt        *time.Time
	ProbeFailCount     int `gorm:"not null;default:0"`
	HeartbeatInterval  int `gorm:"not null;default:30"`
	DesiredRevision    uint64
	AppliedRevision    uint64
	LastApplyError     string
	LastBootID         string
	LastStartedAt      *time.Time
	ConfigChangedAt    *time.Time
	BootIDAtChange     string
	LastReloadError    string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (sqliteEnrollmentMailServer) TableName() string { return "mail_servers" }

type sqliteEnrollmentMailboxReference struct {
	ID       uint64 `gorm:"primaryKey;autoIncrement"`
	ServerID uint64 `gorm:"not null;index"`
}

func (sqliteEnrollmentMailboxReference) TableName() string { return "mailbox_accounts" }

type sqliteEnrollmentDomainReference struct {
	ID       uint64 `gorm:"primaryKey;autoIncrement"`
	ServerID uint64 `gorm:"not null;index"`
}

func (sqliteEnrollmentDomainReference) TableName() string { return "server_domains" }

func (store *enrollmentTestStore) DB() *gorm.DB { return store.db }
func (store *enrollmentTestStore) GetServer(id uint64) (*model.MailServer, error) {
	var server model.MailServer
	if err := store.db.First(&server, id).Error; err != nil {
		return nil, err
	}
	return &server, nil
}

func newEnrollmentTestService(t *testing.T) (*NodeEnrollmentService, *gorm.DB, *time.Time) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&sqliteEnrollmentMailServer{}, &model.NodeEnrollmentToken{}, &model.NodeEnrollmentRequest{},
		&model.NodeCredential{}, &model.NodeRegistrationAudit{},
		&sqliteEnrollmentMailboxReference{}, &sqliteEnrollmentDomainReference{},
	); err != nil {
		t.Fatalf("migrate enrollment test schema: %v", err)
	}
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	service := &NodeEnrollmentService{store: &enrollmentTestStore{db: db}, now: func() time.Time { return now }}
	return service, db, &now
}

func TestNodeEnrollmentStandardLifecycleAndCredentialRevocation(t *testing.T) {
	service, db, now := newEnrollmentTestService(t)
	created, err := service.CreateEnrollment(CreateEnrollmentInput{
		Name: "production node", Environment: "prod", Region: "cn-east", Labels: map[string]string{"rack": "a1"},
		ExpiresIn: 30 * time.Minute, MaxUses: 1, Actor: "admin", SourceIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Token == "" || created.Invitation.TokenHash == created.Token || strings.Contains(created.Invitation.TokenHash, created.Token) {
		t.Fatal("enrollment token was not returned once and hashed at rest")
	}

	claim, err := service.Claim(EnrollmentClaimInput{
		Token: created.Token, NodeUUID: testNodeUUIDA, Name: "mail-node-a", Hostname: "mx-a",
		OS: "linux", Arch: "amd64", AgentVersion: "1.8.0", MachineFingerprint: "sha256:" + strings.Repeat("a", 64), SourceIP: "10.0.0.8",
	})
	if err != nil {
		t.Fatal(err)
	}
	if claim.Request.State != model.EnrollmentRequestPending || claim.RequestSecret == "" {
		t.Fatalf("claim = %+v", claim)
	}
	if _, _, err := service.CompleteRequest(claim.Request.ID, claim.RequestSecret, "10.0.0.8"); !errors.Is(err, ErrEnrollmentInvalidState) {
		t.Fatalf("complete before approval error = %v", err)
	}

	approved, err := service.ApproveRequest(claim.Request.ID, "admin", "verified asset", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if approved.ServerID == nil {
		t.Fatal("approval did not bind a server")
	}
	server, err := service.store.GetServer(*approved.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	if server.NodeUUID == nil || *server.NodeUUID != testNodeUUIDA || server.EnrollmentState != model.EnrollmentApproved ||
		server.AllocationState != model.AllocationDisabled {
		t.Fatalf("approved server = %+v", server)
	}

	rawCredential, credential, err := service.CompleteRequest(claim.Request.ID, claim.RequestSecret, "10.0.0.8")
	if err != nil {
		t.Fatal(err)
	}
	if rawCredential == "" || credential.CredentialHash == rawCredential || credential.State != model.NodeCredentialActive {
		t.Fatalf("credential delivery = %#v / %+v", rawCredential, credential)
	}
	if _, _, err := service.CompleteRequest(claim.Request.ID, claim.RequestSecret, "10.0.0.8"); !errors.Is(err, ErrEnrollmentInvalidState) {
		t.Fatalf("second completion error = %v", err)
	}
	principal, err := service.AuthenticateCredential(rawCredential, testNodeUUIDA, *now)
	if err != nil || principal.ServerID != *approved.ServerID {
		t.Fatalf("node authentication = %+v, %v", principal, err)
	}
	if _, err := service.AuthenticateCredential(rawCredential, testNodeUUIDB, *now); !errors.Is(err, ErrNodeCredentialInvalid) {
		t.Fatalf("wrong UUID authentication error = %v", err)
	}

	rotatedRaw, rotated, err := service.RotateCredential(*approved.ServerID, "admin", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Version != 2 || rotatedRaw == rawCredential {
		t.Fatalf("rotated credential = %+v", rotated)
	}
	if _, err := service.AuthenticateCredential(rawCredential, testNodeUUIDA, *now); err != nil {
		t.Fatalf("old credential must remain valid during overlap: %v", err)
	}
	thirdRaw, third, err := service.RotateCredential(*approved.ServerID, "admin", "127.0.0.1")
	if err != nil || third.Version != 3 {
		t.Fatalf("second rotation = %+v, %v", third, err)
	}
	if _, err := service.AuthenticateCredential(rawCredential, testNodeUUIDA, *now); !errors.Is(err, ErrNodeCredentialInvalid) {
		t.Fatalf("credential older than the overlap window error = %v", err)
	}
	if _, err := service.AuthenticateCredential(rotatedRaw, testNodeUUIDA, *now); err != nil {
		t.Fatalf("immediately previous credential must overlap: %v", err)
	}
	if err := service.RevokeCredentials(*approved.ServerID, "admin", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{rawCredential, rotatedRaw, thirdRaw} {
		if _, err := service.AuthenticateCredential(raw, testNodeUUIDA, *now); !errors.Is(err, ErrNodeCredentialInvalid) {
			t.Fatalf("revoked credential authentication error = %v", err)
		}
	}
	if err := db.First(server, server.ID).Error; err != nil {
		t.Fatal(err)
	}
	if server.EnrollmentState != model.EnrollmentRevoked || server.AllocationState != model.AllocationDisabled {
		t.Fatalf("revoked server = %+v", server)
	}

	var audits []model.NodeRegistrationAudit
	if err := db.Order("id ASC").Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if len(audits) < 6 {
		t.Fatalf("audit count = %d", len(audits))
	}
	for _, audit := range audits {
		if strings.Contains(audit.Details, created.Token) || strings.Contains(audit.Details, claim.RequestSecret) ||
			strings.Contains(audit.Details, rawCredential) || strings.Contains(audit.Details, rotatedRaw) || strings.Contains(audit.Details, thirdRaw) {
			t.Fatalf("audit contains a secret: %+v", audit)
		}
	}
}

func TestRevokedEnrollmentInvitationCanBeSoftDeleted(t *testing.T) {
	service, db, _ := newEnrollmentTestService(t)
	created, err := service.CreateEnrollment(CreateEnrollmentInput{
		Name: "deletable invitation", ExpiresIn: 30 * time.Minute, MaxUses: 1, Actor: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := service.Claim(EnrollmentClaimInput{
		Token: created.Token, NodeUUID: testNodeUUIDA, Name: "mail-node-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteEnrollment(created.Invitation.ID, "admin", "127.0.0.1"); !errors.Is(err, ErrEnrollmentInvalidState) {
		t.Fatalf("delete active invitation error = %v", err)
	}
	if err := service.RevokeEnrollment(created.Invitation.ID, "admin", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteEnrollment(created.Invitation.ID, "admin", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}

	var visible int64
	if err := db.Model(&model.NodeEnrollmentToken{}).Where("id = ?", created.Invitation.ID).Count(&visible).Error; err != nil || visible != 0 {
		t.Fatalf("visible invitation count = %d, error = %v", visible, err)
	}
	var deleted model.NodeEnrollmentToken
	if err := db.Unscoped().First(&deleted, created.Invitation.ID).Error; err != nil || !deleted.DeletedAt.Valid {
		t.Fatalf("soft-deleted invitation = %+v, error = %v", deleted, err)
	}
	requests, err := service.ListRequests("")
	if err != nil || len(requests) != 1 || requests[0].Request.ID != claim.Request.ID {
		t.Fatalf("request history after invitation deletion = %+v, error = %v", requests, err)
	}
	if _, err := service.GetRequest(claim.Request.ID); err != nil {
		t.Fatalf("request details after invitation deletion: %v", err)
	}
	if _, err := service.ApproveRequest(claim.Request.ID, "admin", "verified", "127.0.0.1"); err != nil {
		t.Fatalf("approve request after invitation deletion: %v", err)
	}
}

func TestEnrollmentInvitationBoundariesAndRejection(t *testing.T) {
	service, db, now := newEnrollmentTestService(t)

	expiring, err := service.CreateEnrollment(CreateEnrollmentInput{Name: "short", ExpiresIn: time.Minute, Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Minute)
	if _, err := service.Claim(EnrollmentClaimInput{Token: expiring.Token, NodeUUID: testNodeUUIDA, Name: "node"}); !errors.Is(err, ErrEnrollmentTokenExpired) {
		t.Fatalf("expired token error = %v", err)
	}
	var expired model.NodeEnrollmentToken
	if err := db.First(&expired, expiring.Invitation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if expired.State != model.EnrollmentTokenExpired {
		t.Fatalf("expired invitation state = %q", expired.State)
	}

	active, err := service.CreateEnrollment(CreateEnrollmentInput{Name: "revoked", Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RevokeEnrollment(active.Invitation.ID, "admin", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Claim(EnrollmentClaimInput{Token: active.Token, NodeUUID: testNodeUUIDA, Name: "node"}); !errors.Is(err, ErrEnrollmentTokenUnavailable) {
		t.Fatalf("revoked token error = %v", err)
	}

	bound, err := service.CreateEnrollment(CreateEnrollmentInput{Name: "bound", ExpectedNodeUUID: testNodeUUIDA, Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Claim(EnrollmentClaimInput{Token: bound.Token, NodeUUID: testNodeUUIDB, Name: "node"}); !errors.Is(err, ErrEnrollmentUUIDMismatch) {
		t.Fatalf("pre-bound UUID mismatch error = %v", err)
	}
	claim, err := service.Claim(EnrollmentClaimInput{Token: bound.Token, NodeUUID: testNodeUUIDA, Name: "node"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RejectRequest(claim.Request.ID, "admin", "asset mismatch", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	status, err := service.RequestStatus(claim.Request.ID, claim.RequestSecret)
	if err != nil || status.State != model.EnrollmentRequestRejected {
		t.Fatalf("rejected status = %+v, %v", status, err)
	}
	if _, _, err := service.CompleteRequest(claim.Request.ID, claim.RequestSecret, "127.0.0.1"); !errors.Is(err, ErrEnrollmentInvalidState) {
		t.Fatalf("rejected completion error = %v", err)
	}

	automatic, err := service.CreateEnrollment(CreateEnrollmentInput{Name: "automatic", AutoApprove: true, Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	autoClaim, err := service.Claim(EnrollmentClaimInput{Token: automatic.Token, NodeUUID: testNodeUUIDB, Name: "auto-node"})
	if err != nil {
		t.Fatal(err)
	}
	if autoClaim.Request.State != model.EnrollmentRequestApproved || autoClaim.Request.ServerID == nil {
		t.Fatalf("auto-approved request = %+v", autoClaim.Request)
	}
}

func TestEnrollmentCompleteRejectsRevokedServer(t *testing.T) {
	service, db, _ := newEnrollmentTestService(t)
	invitation, err := service.CreateEnrollment(CreateEnrollmentInput{Name: "revoked before completion", Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := service.Claim(EnrollmentClaimInput{Token: invitation.Token, NodeUUID: testNodeUUIDA, Name: "revoked-node"})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := service.ApproveRequest(claim.Request.ID, "admin", "verified", "127.0.0.1")
	if err != nil || approved.ServerID == nil {
		t.Fatalf("approve request = %+v, error = %v", approved, err)
	}
	if err := service.RevokeCredentials(*approved.ServerID, "admin", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}

	if _, _, err := service.CompleteRequest(claim.Request.ID, claim.RequestSecret, "127.0.0.1"); !errors.Is(err, ErrEnrollmentInvalidState) {
		t.Fatalf("completion after revocation error = %v", err)
	}

	var request model.NodeEnrollmentRequest
	if err := db.First(&request, "id = ?", claim.Request.ID).Error; err != nil {
		t.Fatal(err)
	}
	if request.State != model.EnrollmentRequestExpired {
		t.Fatalf("revocation did not invalidate approved request: %+v", request)
	}
	var credentialCount, completionAuditCount int64
	if err := db.Model(&model.NodeCredential{}).Where("server_id = ?", *approved.ServerID).Count(&credentialCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.NodeRegistrationAudit{}).
		Where("action = ? AND entity_id = ?", "request.complete", claim.Request.ID).Count(&completionAuditCount).Error; err != nil {
		t.Fatal(err)
	}
	if credentialCount != 0 || completionAuditCount != 0 {
		t.Fatalf("failed completion left partial state: credentials=%d audits=%d", credentialCount, completionAuditCount)
	}

	recovery, err := service.CreateEnrollment(CreateEnrollmentInput{Name: "recover revoked node", RecoveryServerID: *approved.ServerID, Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	recoveryClaim, err := service.Claim(EnrollmentClaimInput{Token: recovery.Token, NodeUUID: testNodeUUIDA, Name: "recovered-node"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApproveRequest(recoveryClaim.Request.ID, "admin", "recovery verified", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.CompleteRequest(claim.Request.ID, claim.RequestSecret, "127.0.0.1"); !errors.Is(err, ErrEnrollmentInvalidState) {
		t.Fatalf("old request revived after recovery approval: %v", err)
	}
	if _, _, err := service.CompleteRequest(recoveryClaim.Request.ID, recoveryClaim.RequestSecret, "127.0.0.1"); err != nil {
		t.Fatalf("recovery request completion error = %v", err)
	}
}

func TestEnrollmentRecoveryReusesServerAndRevokesOldCredential(t *testing.T) {
	service, db, now := newEnrollmentTestService(t)
	nodeUUID := testNodeUUIDA
	server := model.MailServer{
		Name: "existing", NodeUUID: &nodeUUID, APIHost: "10.0.0.8:8081", SMTPHost: "smtp.example", IMAPHost: "imap.example",
		Capacity: 100, Status: "healthy", EnrollmentState: model.EnrollmentApproved,
		ConnectionState: model.ConnectionDisconnected, ReadinessState: model.ReadinessReady,
		AllocationState: model.AllocationActive, TransportMode: model.TransportLegacyHTTP,
	}
	if err := db.Create(&server).Error; err != nil {
		t.Fatal(err)
	}
	oldRaw := "mhn_" + strings.Repeat("1", 64)
	old := model.NodeCredential{ServerID: server.ID, CredentialPrefix: prefix(oldRaw), CredentialHash: HashNodeSecret(oldRaw), State: model.NodeCredentialActive, Version: 1}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	priorRequest := model.NodeEnrollmentRequest{
		ID: "prior-completed-request", EnrollmentTokenID: 999, RequestSecretHash: HashNodeSecret("prior-secret"),
		RequestedNodeUUID: nodeUUID, RequestedName: server.Name, State: model.EnrollmentRequestCompleted, ServerID: &server.ID,
	}
	if err := db.Create(&priorRequest).Error; err != nil {
		t.Fatal(err)
	}

	invitation, err := service.CreateEnrollment(CreateEnrollmentInput{Name: "recover existing", RecoveryServerID: server.ID, Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := service.Claim(EnrollmentClaimInput{Token: invitation.Token, NodeUUID: nodeUUID, Name: "existing-reinstalled"})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := service.ApproveRequest(claim.Request.ID, "admin", "reinstall verified", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if approved.ServerID == nil || *approved.ServerID != server.ID {
		t.Fatalf("recovery server ID = %v, want %d", approved.ServerID, server.ID)
	}
	newRaw, newCredential, err := service.CompleteRequest(claim.Request.ID, claim.RequestSecret, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if newCredential.Version != 2 {
		t.Fatalf("recovery credential version = %d", newCredential.Version)
	}
	var serverCount int64
	if err := db.Model(&model.MailServer{}).Count(&serverCount).Error; err != nil || serverCount != 1 {
		t.Fatalf("server count = %d, error = %v", serverCount, err)
	}
	if _, err := service.AuthenticateCredential(oldRaw, nodeUUID, *now); !errors.Is(err, ErrNodeCredentialInvalid) {
		t.Fatalf("old recovery credential error = %v", err)
	}
	if _, err := service.AuthenticateCredential(newRaw, nodeUUID, *now); err != nil {
		t.Fatalf("new recovery credential error = %v", err)
	}
}

func TestEnrollmentMigrationAdoptsLegacyServerInPlace(t *testing.T) {
	service, db, now := newEnrollmentTestService(t)
	server := model.MailServer{
		Name: "legacy-existing", APIHost: "10.0.0.9:8081", SMTPHost: "smtp.example", IMAPHost: "imap.example",
		PublicHost: "mail.example", MailPublicIPs: []string{"203.0.113.9"}, Capacity: 321, CurrentLoad: 17,
		Status: "healthy", EnrollmentState: model.EnrollmentLegacyApproved, ConnectionState: model.ConnectionUnknown,
		ReadinessState: model.ReadinessReady, AllocationState: model.AllocationActive, TransportMode: model.TransportLegacyHTTP,
		DesiredRevision: 23, AppliedRevision: 22, LastApplyError: "legacy warning", HeartbeatInterval: 45,
	}
	if err := db.Create(&server).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&sqliteEnrollmentMailboxReference{ServerID: server.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&sqliteEnrollmentDomainReference{ServerID: server.ID}).Error; err != nil {
		t.Fatal(err)
	}

	invitation, err := service.CreateEnrollment(CreateEnrollmentInput{
		Name: "migrate legacy", RecoveryServerID: server.ID, MaxUses: 9, Actor: "admin", SourceIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if invitation.Invitation.Purpose != model.EnrollmentPurposeMigration || invitation.Invitation.ExpectedNodeUUID != nil ||
		invitation.Invitation.RecoveryServerID == nil || *invitation.Invitation.RecoveryServerID != server.ID || invitation.Invitation.MaxUses != 1 {
		t.Fatalf("migration invitation = %+v", invitation.Invitation)
	}

	claim, err := service.Claim(EnrollmentClaimInput{
		Token: invitation.Token, NodeUUID: testNodeUUIDA, Name: "legacy-existing", Hostname: "mx-existing",
		OS: "linux", Arch: "amd64", AgentVersion: "p7-migration", MachineFingerprint: "sha256:" + strings.Repeat("c", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	details, err := service.GetRequest(claim.Request.ID)
	if err != nil || details.TargetServer == nil || details.TargetServer.ID != server.ID || details.TargetServer.APIHost != server.APIHost {
		t.Fatalf("migration request target = %+v, error = %v", details, err)
	}
	approved, err := service.ApproveRequest(claim.Request.ID, "admin", "legacy host verified", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if approved.ServerID == nil || *approved.ServerID != server.ID {
		t.Fatalf("migration server ID = %v, want %d", approved.ServerID, server.ID)
	}

	var migrated model.MailServer
	if err := db.First(&migrated, server.ID).Error; err != nil {
		t.Fatal(err)
	}
	if migrated.NodeUUID == nil || *migrated.NodeUUID != testNodeUUIDA || migrated.EnrollmentState != model.EnrollmentApproved {
		t.Fatalf("migrated identity = %+v", migrated)
	}
	if migrated.APIHost != server.APIHost || migrated.SMTPHost != server.SMTPHost || migrated.IMAPHost != server.IMAPHost ||
		migrated.PublicHost != server.PublicHost || migrated.Capacity != server.Capacity || migrated.CurrentLoad != server.CurrentLoad ||
		migrated.Status != server.Status || migrated.ConnectionState != server.ConnectionState || migrated.ReadinessState != server.ReadinessState ||
		migrated.AllocationState != server.AllocationState || migrated.TransportMode != model.TransportLegacyHTTP ||
		migrated.DesiredRevision != server.DesiredRevision || migrated.AppliedRevision != server.AppliedRevision ||
		migrated.LastApplyError != server.LastApplyError || migrated.HeartbeatInterval != server.HeartbeatInterval ||
		len(migrated.MailPublicIPs) != 1 || migrated.MailPublicIPs[0] != server.MailPublicIPs[0] {
		t.Fatalf("migration changed legacy server state: before=%+v after=%+v", server, migrated)
	}
	var serverCount int64
	if err := db.Model(&model.MailServer{}).Count(&serverCount).Error; err != nil || serverCount != 1 {
		t.Fatalf("server count = %d, error = %v", serverCount, err)
	}
	var mailboxRefs, domainRefs int64
	if err := db.Model(&sqliteEnrollmentMailboxReference{}).Where("server_id = ?", server.ID).Count(&mailboxRefs).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&sqliteEnrollmentDomainReference{}).Where("server_id = ?", server.ID).Count(&domainRefs).Error; err != nil {
		t.Fatal(err)
	}
	if mailboxRefs != 1 || domainRefs != 1 {
		t.Fatalf("legacy references changed: mailboxes=%d domains=%d", mailboxRefs, domainRefs)
	}
	var invitationAudit model.NodeRegistrationAudit
	if err := db.Where("action = ? AND entity_id = ?", "invitation.create", invitation.Invitation.ID).First(&invitationAudit).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(invitationAudit.Details, fmt.Sprintf("\"target_server_id\":%d", server.ID)) || strings.Contains(invitationAudit.Details, invitation.Token) {
		t.Fatalf("migration invitation audit = %+v", invitationAudit)
	}

	rawCredential, _, err := service.CompleteRequest(claim.Request.ID, claim.RequestSecret, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := service.AuthenticateCredential(rawCredential, testNodeUUIDA, *now)
	if err != nil || principal.ServerID != server.ID {
		t.Fatalf("migration credential principal = %+v, error = %v", principal, err)
	}
}

func TestEnrollmentMigrationRejectsCompetingInvitationForSameLegacyServer(t *testing.T) {
	service, db, _ := newEnrollmentTestService(t)
	server := model.MailServer{
		Name: "legacy-existing", APIHost: "10.0.0.9:8081", SMTPHost: "smtp.example", IMAPHost: "imap.example",
		Capacity: 100, Status: "healthy", EnrollmentState: model.EnrollmentLegacyApproved,
		ConnectionState: model.ConnectionUnknown, ReadinessState: model.ReadinessReady,
		AllocationState: model.AllocationActive, TransportMode: model.TransportLegacyHTTP,
	}
	if err := db.Create(&server).Error; err != nil {
		t.Fatal(err)
	}
	first, err := service.CreateEnrollment(CreateEnrollmentInput{Name: "first migration", RecoveryServerID: server.ID, Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateEnrollment(CreateEnrollmentInput{Name: "second migration", RecoveryServerID: server.ID, Actor: "admin"}); !errors.Is(err, ErrEnrollmentInvalidState) {
		t.Fatalf("competing migration invitation error = %v", err)
	}
	if _, err := service.Claim(EnrollmentClaimInput{Token: first.Token, NodeUUID: testNodeUUIDA, Name: "first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateEnrollment(CreateEnrollmentInput{Name: "third migration", RecoveryServerID: server.ID, Actor: "admin"}); !errors.Is(err, ErrEnrollmentInvalidState) {
		t.Fatalf("migration invitation while request is pending error = %v", err)
	}
}

func TestEnrollmentMigrationRejectsTargetChangeBeforeApproval(t *testing.T) {
	service, db, _ := newEnrollmentTestService(t)
	server := model.MailServer{
		Name: "legacy-changing", APIHost: "10.0.0.10:8081", SMTPHost: "smtp.example", IMAPHost: "imap.example",
		Capacity: 100, Status: "healthy", EnrollmentState: model.EnrollmentLegacyApproved,
		ConnectionState: model.ConnectionUnknown, ReadinessState: model.ReadinessReady,
		AllocationState: model.AllocationActive, TransportMode: model.TransportLegacyHTTP,
	}
	if err := db.Create(&server).Error; err != nil {
		t.Fatal(err)
	}
	invitation, err := service.CreateEnrollment(CreateEnrollmentInput{Name: "migration", RecoveryServerID: server.ID, Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := service.Claim(EnrollmentClaimInput{Token: invitation.Token, NodeUUID: testNodeUUIDA, Name: "legacy-changing"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.MailServer{}).Where("id = ?", server.ID).Update("transport_mode", model.TransportDual).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApproveRequest(claim.Request.ID, "admin", "must fail", "127.0.0.1"); !errors.Is(err, ErrEnrollmentInvalidState) {
		t.Fatalf("approval after target change error = %v", err)
	}
	var request model.NodeEnrollmentRequest
	if err := db.First(&request, "id = ?", claim.Request.ID).Error; err != nil {
		t.Fatal(err)
	}
	if request.State != model.EnrollmentRequestPending || request.ServerID != nil {
		t.Fatalf("failed approval changed request = %+v", request)
	}
	var credentialCount, approveAuditCount int64
	if err := db.Model(&model.NodeCredential{}).Count(&credentialCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.NodeRegistrationAudit{}).Where("action = ? AND entity_id = ?", "request.approve", request.ID).Count(&approveAuditCount).Error; err != nil {
		t.Fatal(err)
	}
	if credentialCount != 0 || approveAuditCount != 0 {
		t.Fatalf("failed approval left partial state: credentials=%d audits=%d", credentialCount, approveAuditCount)
	}
}
