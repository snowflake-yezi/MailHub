package service

import (
	"errors"
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
