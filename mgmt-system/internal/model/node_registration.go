package model

import (
	"time"

	"gorm.io/gorm"
)

const (
	EnrollmentPending        = "pending"
	EnrollmentApproved       = "approved"
	EnrollmentRevoked        = "revoked"
	EnrollmentLegacyApproved = "legacy_approved"

	ConnectionConnected    = "connected"
	ConnectionDisconnected = "disconnected"
	ConnectionUnknown      = "unknown"

	ReadinessReady    = "ready"
	ReadinessDegraded = "degraded"
	ReadinessFailed   = "failed"
	ReadinessUnknown  = "unknown"

	AllocationActive   = "active"
	AllocationDraining = "draining"
	AllocationDisabled = "disabled"

	TransportLegacyHTTP    = "legacy_http"
	TransportDual          = "dual"
	TransportControlStream = "control_stream"

	EnrollmentTokenActive  = "active"
	EnrollmentTokenUsed    = "used"
	EnrollmentTokenExpired = "expired"
	EnrollmentTokenRevoked = "revoked"

	EnrollmentPurposeNew       = "new"
	EnrollmentPurposeMigration = "migration"
	EnrollmentPurposeRecovery  = "recovery"

	EnrollmentRequestPending   = "pending"
	EnrollmentRequestApproved  = "approved"
	EnrollmentRequestRejected  = "rejected"
	EnrollmentRequestCompleted = "completed"
	EnrollmentRequestExpired   = "expired"

	NodeCredentialActive   = "active"
	NodeCredentialRotating = "rotating"
	NodeCredentialRevoked  = "revoked"
	NodeCredentialExpired  = "expired"

	NodeCommandQueued               = "queued"
	NodeCommandDelivered            = "delivered"
	NodeCommandReceived             = "received"
	NodeCommandRunning              = "running"
	NodeCommandSucceeded            = "succeeded"
	NodeCommandSucceededWithWarning = "succeeded_with_warning"
	NodeCommandFailed               = "failed"
	NodeCommandRejected             = "rejected"
	NodeCommandExpired              = "expired"
)

func IsTerminalNodeCommandState(state string) bool {
	switch state {
	case NodeCommandSucceeded, NodeCommandSucceededWithWarning, NodeCommandFailed, NodeCommandRejected, NodeCommandExpired:
		return true
	default:
		return false
	}
}

// ApplyLegacyNodeDefaults maps the former combined status into the new state
// dimensions without requiring a UUID or changing legacy transport behavior.
func (server *MailServer) ApplyLegacyNodeDefaults() {
	if server.EnrollmentState == "" {
		server.EnrollmentState = EnrollmentLegacyApproved
	}
	if server.ConnectionState == "" {
		server.ConnectionState = ConnectionUnknown
	}
	if server.ReadinessState == "" {
		server.ReadinessState = LegacyReadinessState(server.Status)
	}
	if server.AllocationState == "" {
		server.AllocationState = AllocationActive
		if server.Status == "draining" {
			server.AllocationState = AllocationDraining
		}
	}
	if server.TransportMode == "" {
		server.TransportMode = TransportLegacyHTTP
	}
}

func LegacyReadinessState(status string) string {
	switch status {
	case "healthy", "draining":
		return ReadinessReady
	case "degraded":
		return ReadinessDegraded
	case "down":
		return ReadinessFailed
	default:
		return ReadinessUnknown
	}
}

// ApplyLegacyAdminStatus keeps the compatibility status and the new state
// dimensions coherent while legacy_http remains the active transport.
func (server *MailServer) ApplyLegacyAdminStatus(status string) {
	server.Status = status
	if server.TransportMode != "" && server.TransportMode != TransportLegacyHTTP {
		return
	}
	server.ReadinessState = LegacyReadinessState(status)
	if status == "draining" {
		server.AllocationState = AllocationDraining
	} else if server.AllocationState == "" || server.AllocationState == AllocationDraining {
		server.AllocationState = AllocationActive
	}
}

// IsAllocatableState evaluates node-level allocation state. Domain readiness is
// checked separately because it belongs to a server/domain binding.
func (server MailServer) IsAllocatableState(now time.Time) bool {
	if server.EnrollmentState != EnrollmentApproved && server.EnrollmentState != EnrollmentLegacyApproved {
		return false
	}
	if server.ReadinessState != ReadinessReady || server.AllocationState != AllocationActive {
		return false
	}
	if server.CurrentLoad >= server.Capacity {
		return false
	}

	switch server.TransportMode {
	case TransportLegacyHTTP:
		return server.Status == "healthy"
	case TransportControlStream:
		return server.ConnectionState == ConnectionConnected && server.LeaseExpiresAt != nil && server.LeaseExpiresAt.After(now)
	case TransportDual:
		// Until NR-P3 introduces an explicit primary transport, require both the
		// legacy probe and the outbound session to be healthy.
		return server.Status == "healthy" && server.ConnectionState == ConnectionConnected &&
			server.LeaseExpiresAt != nil && server.LeaseExpiresAt.After(now)
	default:
		return false
	}
}

// NodeEnrollmentToken stores only a token digest and display-safe prefix.
type NodeEnrollmentToken struct {
	ID               uint64            `gorm:"primaryKey;autoIncrement" json:"id"`
	TokenPrefix      string            `gorm:"size:32;not null;index" json:"token_prefix"`
	TokenHash        string            `gorm:"type:char(64);not null;uniqueIndex" json:"-"`
	ExpectedNodeUUID *string           `gorm:"type:char(36);index" json:"expected_node_uuid,omitempty"`
	Purpose          string            `gorm:"size:24;not null;default:new;index" json:"purpose"`
	RecoveryServerID *uint64           `gorm:"index" json:"recovery_server_id,omitempty"`
	Name             string            `gorm:"size:128;not null" json:"name"`
	Environment      string            `gorm:"size:64;index" json:"environment,omitempty"`
	Region           string            `gorm:"size:64;index" json:"region,omitempty"`
	Labels           map[string]string `gorm:"serializer:json;type:text" json:"labels"`
	State            string            `gorm:"size:24;not null;default:active;index" json:"state"`
	ExpiresAt        time.Time         `gorm:"not null;index" json:"expires_at"`
	MaxUses          int               `gorm:"not null;default:1" json:"max_uses"`
	UsedCount        int               `gorm:"not null;default:0" json:"used_count"`
	AutoApprove      bool              `gorm:"not null;default:false" json:"auto_approve"`
	CreatedBy        string            `gorm:"size:128;not null" json:"created_by"`
	CreatedAt        time.Time         `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt        time.Time         `gorm:"autoUpdateTime" json:"updated_at"`
	RevokedAt        *time.Time        `json:"revoked_at,omitempty"`
	DeletedAt        gorm.DeletedAt    `gorm:"index" json:"-"`
}

// NodeRegistrationAudit records administrator and automatic enrollment state
// changes without ever retaining enrollment, request, or runtime secrets.
type NodeRegistrationAudit struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Action     string    `gorm:"size:64;not null;index" json:"action"`
	EntityType string    `gorm:"size:32;not null;index" json:"entity_type"`
	EntityID   string    `gorm:"size:64;not null;index" json:"entity_id"`
	Actor      string    `gorm:"size:128;not null;index" json:"actor"`
	SourceIP   string    `gorm:"size:64" json:"source_ip,omitempty"`
	Details    string    `gorm:"type:text;not null" json:"details"`
	CreatedAt  time.Time `gorm:"autoCreateTime;index" json:"created_at"`
}

// NodeEnrollmentRequest is a pending or reviewed node identity claim.
type NodeEnrollmentRequest struct {
	ID                 string     `gorm:"size:32;primaryKey" json:"id"`
	EnrollmentTokenID  uint64     `gorm:"not null;index" json:"enrollment_token_id"`
	RequestSecretHash  string     `gorm:"type:char(64);not null;uniqueIndex" json:"-"`
	RequestedNodeUUID  string     `gorm:"type:char(36);not null;index" json:"requested_node_uuid"`
	RequestedName      string     `gorm:"size:128;not null" json:"requested_name"`
	Hostname           string     `gorm:"size:255" json:"hostname,omitempty"`
	OS                 string     `gorm:"size:64" json:"os,omitempty"`
	Arch               string     `gorm:"size:64" json:"arch,omitempty"`
	AgentVersion       string     `gorm:"size:64" json:"agent_version,omitempty"`
	MachineFingerprint string     `gorm:"size:71;index" json:"machine_fingerprint,omitempty"`
	SourceIP           string     `gorm:"size:64" json:"source_ip,omitempty"`
	State              string     `gorm:"size:24;not null;default:pending;index" json:"state"`
	ReviewedBy         string     `gorm:"size:128" json:"reviewed_by,omitempty"`
	ReviewedAt         *time.Time `json:"reviewed_at,omitempty"`
	ReviewNote         string     `gorm:"size:1024" json:"review_note,omitempty"`
	ServerID           *uint64    `gorm:"index" json:"server_id,omitempty"`
	CreatedAt          time.Time  `gorm:"autoCreateTime;index" json:"created_at"`
	UpdatedAt          time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

// NodeCredential is one revocable version of a per-node runtime token.
type NodeCredential struct {
	ID               uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	ServerID         uint64         `gorm:"not null;index;uniqueIndex:uk_node_credential_version" json:"server_id"`
	CredentialPrefix string         `gorm:"size:32;not null;index" json:"credential_prefix"`
	CredentialHash   string         `gorm:"type:char(64);not null;uniqueIndex" json:"-"`
	State            string         `gorm:"size:24;not null;default:active;index" json:"state"`
	Version          uint64         `gorm:"not null;uniqueIndex:uk_node_credential_version" json:"version"`
	ExpiresAt        *time.Time     `gorm:"index" json:"expires_at,omitempty"`
	LastUsedAt       *time.Time     `json:"last_used_at,omitempty"`
	CreatedAt        time.Time      `gorm:"autoCreateTime" json:"created_at"`
	RevokedAt        *time.Time     `json:"revoked_at,omitempty"`
	DeletedAt        gorm.DeletedAt `gorm:"index" json:"-"`
}

// NodeCommand is the durable source of truth for an at-least-once command.
type NodeCommand struct {
	CommandID      string     `gorm:"type:char(36);primaryKey" json:"command_id"`
	ServerID       uint64     `gorm:"not null;index;uniqueIndex:uk_node_command_sequence;uniqueIndex:uk_node_command_idempotency" json:"server_id"`
	Sequence       uint64     `gorm:"not null;uniqueIndex:uk_node_command_sequence" json:"sequence"`
	CommandType    string     `gorm:"size:64;not null;index" json:"command_type"`
	SchemaVersion  uint32     `gorm:"not null" json:"schema_version"`
	IdempotencyKey string     `gorm:"size:191;not null;uniqueIndex:uk_node_command_idempotency" json:"idempotency_key"`
	PayloadJSON    string     `gorm:"type:longtext;not null" json:"-"`
	State          string     `gorm:"size:32;not null;default:queued;index" json:"state"`
	AttemptCount   int        `gorm:"not null;default:0" json:"attempt_count"`
	DeadlineAt     time.Time  `gorm:"not null;index" json:"deadline_at"`
	ReceivedAt     *time.Time `json:"received_at,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	ResultCode     string     `gorm:"size:128" json:"result_code,omitempty"`
	ResultJSON     string     `gorm:"type:longtext" json:"-"`
	ErrorMessage   string     `gorm:"type:text" json:"error_message,omitempty"`
	TraceID        string     `gorm:"size:64;index" json:"trace_id,omitempty"`
	RequestedBy    string     `gorm:"size:128" json:"requested_by,omitempty"`
	CreatedAt      time.Time  `gorm:"autoCreateTime;index" json:"created_at"`
	UpdatedAt      time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

func (NodeEnrollmentToken) TableName() string   { return "node_enrollment_tokens" }
func (NodeEnrollmentRequest) TableName() string { return "node_enrollment_requests" }
func (NodeCredential) TableName() string        { return "node_credentials" }
func (NodeCommand) TableName() string           { return "node_commands" }
func (NodeRegistrationAudit) TableName() string { return "node_registration_audits" }
