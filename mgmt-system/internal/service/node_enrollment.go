package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/store"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrEnrollmentNotFound         = errors.New("node enrollment not found")
	ErrEnrollmentTokenInvalid     = errors.New("invalid enrollment token")
	ErrEnrollmentTokenExpired     = errors.New("enrollment token expired")
	ErrEnrollmentTokenUnavailable = errors.New("enrollment token is no longer available")
	ErrEnrollmentUUIDMismatch     = errors.New("node UUID does not match the invitation")
	ErrEnrollmentDuplicateUUID    = errors.New("node UUID is already enrolled or pending")
	ErrEnrollmentInvalidState     = errors.New("invalid enrollment state transition")
	ErrEnrollmentRequestSecret    = errors.New("invalid enrollment request secret")
	ErrNodeCredentialInvalid      = errors.New("invalid node credential")
)

const (
	defaultEnrollmentTTL = 30 * time.Minute
	maxEnrollmentTTL     = 24 * time.Hour
)

type CreateEnrollmentInput struct {
	Name             string
	Environment      string
	Region           string
	Labels           map[string]string
	ExpectedNodeUUID string
	RecoveryServerID uint64
	ExpiresIn        time.Duration
	MaxUses          int
	AutoApprove      bool
	Actor            string
	SourceIP         string
}

type EnrollmentTokenResult struct {
	Invitation model.NodeEnrollmentToken `json:"invitation"`
	Token      string                    `json:"token"`
}

type EnrollmentClaimInput struct {
	Token              string
	NodeUUID           string
	Name               string
	Hostname           string
	OS                 string
	Arch               string
	AgentVersion       string
	MachineFingerprint string
	SourceIP           string
}

type EnrollmentClaimResult struct {
	Request       model.NodeEnrollmentRequest `json:"request"`
	RequestSecret string                      `json:"request_secret"`
}

type EnrollmentRequestDetails struct {
	Request      model.NodeEnrollmentRequest   `json:"request"`
	Invitation   model.NodeEnrollmentToken     `json:"invitation"`
	TargetServer *model.MailServer             `json:"target_server,omitempty"`
	Credential   *model.NodeCredential         `json:"credential,omitempty"`
	RecentAudits []model.NodeRegistrationAudit `json:"recent_audits"`
}

type NodePrincipal struct {
	ServerID      uint64
	NodeUUID      string
	CredentialID  uint64
	CredentialVer uint64
}

type nodeEnrollmentStore interface {
	DB() *gorm.DB
	GetServer(id uint64) (*model.MailServer, error)
}

type NodeEnrollmentService struct {
	store nodeEnrollmentStore
	now   func() time.Time
}

func NewNodeEnrollmentService(st *store.Store) *NodeEnrollmentService {
	return &NodeEnrollmentService{store: st, now: time.Now}
}

func (service *NodeEnrollmentService) CreateEnrollment(input CreateEnrollmentInput) (*EnrollmentTokenResult, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Actor = strings.TrimSpace(input.Actor)
	if input.Name == "" || len(input.Name) > 128 || input.Actor == "" {
		return nil, fmt.Errorf("name and actor are required")
	}
	if input.ExpiresIn == 0 {
		input.ExpiresIn = defaultEnrollmentTTL
	}
	if input.ExpiresIn < time.Minute || input.ExpiresIn > maxEnrollmentTTL {
		return nil, fmt.Errorf("expiration must be between 1 minute and 24 hours")
	}
	if input.MaxUses == 0 {
		input.MaxUses = 1
	}
	if input.MaxUses < 1 || input.MaxUses > 100 {
		return nil, fmt.Errorf("max uses must be between 1 and 100")
	}

	purpose := model.EnrollmentPurposeNew
	var expectedUUID *string
	var recoveryServerID *uint64
	if input.RecoveryServerID != 0 {
		server, err := service.store.GetServer(input.RecoveryServerID)
		if err != nil {
			return nil, fmt.Errorf("existing server not found: %w", ErrEnrollmentNotFound)
		}
		if server.NodeUUID == nil {
			if server.EnrollmentState != model.EnrollmentLegacyApproved || server.TransportMode != model.TransportLegacyHTTP {
				return nil, fmt.Errorf("only legacy HTTP servers can be migrated in place: %w", ErrEnrollmentInvalidState)
			}
			purpose = model.EnrollmentPurposeMigration
		} else {
			if strings.TrimSpace(*server.NodeUUID) == "" {
				return nil, fmt.Errorf("existing server has an invalid empty node UUID: %w", ErrEnrollmentInvalidState)
			}
			purpose = model.EnrollmentPurposeRecovery
			expected := *server.NodeUUID
			expectedUUID = &expected
		}
		recoveryID := server.ID
		recoveryServerID = &recoveryID
		input.MaxUses = 1
		if input.AutoApprove {
			return nil, fmt.Errorf("migration and recovery invitations cannot auto approve")
		}
	} else if strings.TrimSpace(input.ExpectedNodeUUID) != "" {
		normalized, err := normalizeNodeUUID(input.ExpectedNodeUUID)
		if err != nil {
			return nil, err
		}
		expectedUUID = &normalized
	}

	token, err := randomSecret("mhe_", 32)
	if err != nil {
		return nil, err
	}
	now := service.now().UTC()
	record := model.NodeEnrollmentToken{
		TokenPrefix: prefix(token), TokenHash: HashNodeSecret(token), ExpectedNodeUUID: expectedUUID,
		Purpose: purpose, RecoveryServerID: recoveryServerID, Name: input.Name,
		Environment: bounded(input.Environment, 64), Region: bounded(input.Region, 64), Labels: sanitizeLabels(input.Labels),
		State: model.EnrollmentTokenActive, ExpiresAt: now.Add(input.ExpiresIn), MaxUses: input.MaxUses,
		AutoApprove: input.AutoApprove, CreatedBy: input.Actor,
	}
	err = service.store.DB().Transaction(func(tx *gorm.DB) error {
		if recoveryServerID != nil {
			var current model.MailServer
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, *recoveryServerID).Error; err != nil {
				return ErrEnrollmentNotFound
			}
			switch purpose {
			case model.EnrollmentPurposeMigration:
				if current.NodeUUID != nil || current.EnrollmentState != model.EnrollmentLegacyApproved || current.TransportMode != model.TransportLegacyHTTP {
					return ErrEnrollmentInvalidState
				}
				var competing int64
				if err := tx.Model(&model.NodeEnrollmentToken{}).
					Where("purpose = ? AND recovery_server_id = ? AND state = ? AND expires_at > ?",
						model.EnrollmentPurposeMigration, current.ID, model.EnrollmentTokenActive, now).
					Count(&competing).Error; err != nil {
					return err
				}
				if competing == 0 {
					if err := tx.Model(&model.NodeEnrollmentRequest{}).
						Joins("JOIN node_enrollment_tokens ON node_enrollment_tokens.id = node_enrollment_requests.enrollment_token_id").
						Where("node_enrollment_tokens.purpose = ? AND node_enrollment_tokens.recovery_server_id = ? AND node_enrollment_requests.state IN ?",
							model.EnrollmentPurposeMigration, current.ID, []string{model.EnrollmentRequestPending, model.EnrollmentRequestApproved}).
						Count(&competing).Error; err != nil {
						return err
					}
				}
				if competing != 0 {
					return ErrEnrollmentInvalidState
				}
			case model.EnrollmentPurposeRecovery:
				if current.NodeUUID == nil || expectedUUID == nil || *current.NodeUUID != *expectedUUID {
					return ErrEnrollmentUUIDMismatch
				}
			default:
				return ErrEnrollmentInvalidState
			}
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		return createNodeAudit(tx, "invitation.create", "invitation", fmt.Sprint(record.ID), input.Actor, input.SourceIP, map[string]any{
			"purpose": purpose, "token_prefix": record.TokenPrefix, "expires_at": record.ExpiresAt,
			"max_uses": record.MaxUses, "auto_approve": record.AutoApprove, "expected_node_uuid": expectedUUID,
			"target_server_id": recoveryServerID,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("create enrollment invitation: %w", err)
	}
	return &EnrollmentTokenResult{Invitation: record, Token: token}, nil
}

func (service *NodeEnrollmentService) ListEnrollments() ([]model.NodeEnrollmentToken, error) {
	now := service.now().UTC()
	if err := service.store.DB().Model(&model.NodeEnrollmentToken{}).
		Where("state = ? AND expires_at <= ?", model.EnrollmentTokenActive, now).
		Update("state", model.EnrollmentTokenExpired).Error; err != nil {
		return nil, err
	}
	var records []model.NodeEnrollmentToken
	err := service.store.DB().Order("created_at DESC").Find(&records).Error
	return records, err
}

func (service *NodeEnrollmentService) RevokeEnrollment(id uint64, actor, sourceIP string) error {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return fmt.Errorf("actor is required")
	}
	now := service.now().UTC()
	return service.store.DB().Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.NodeEnrollmentToken{}).
			Where("id = ? AND state IN ?", id, []string{model.EnrollmentTokenActive, model.EnrollmentTokenUsed}).
			Updates(map[string]any{"state": model.EnrollmentTokenRevoked, "revoked_at": &now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrEnrollmentInvalidState
		}
		return createNodeAudit(tx, "invitation.revoke", "invitation", fmt.Sprint(id), actor, sourceIP, map[string]any{})
	})
}

func (service *NodeEnrollmentService) DeleteEnrollment(id uint64, actor, sourceIP string) error {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return fmt.Errorf("actor is required")
	}
	return service.store.DB().Transaction(func(tx *gorm.DB) error {
		var invitation model.NodeEnrollmentToken
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&invitation, id).Error; err != nil {
			return mapNotFound(err)
		}
		if invitation.State != model.EnrollmentTokenRevoked {
			return ErrEnrollmentInvalidState
		}
		if err := createNodeAudit(tx, "invitation.delete", "invitation", fmt.Sprint(id), actor, sourceIP, map[string]any{
			"token_prefix": invitation.TokenPrefix,
		}); err != nil {
			return err
		}
		return tx.Delete(&invitation).Error
	})
}

func (service *NodeEnrollmentService) Claim(input EnrollmentClaimInput) (*EnrollmentClaimResult, error) {
	nodeUUID, err := normalizeNodeUUID(input.NodeUUID)
	if err != nil {
		return nil, err
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 128 {
		return nil, fmt.Errorf("node name is required and must not exceed 128 characters")
	}
	if !validMachineFingerprint(input.MachineFingerprint) {
		return nil, fmt.Errorf("invalid machine fingerprint")
	}
	requestID, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	requestSecret, err := randomSecret("mhr_", 32)
	if err != nil {
		return nil, err
	}
	now := service.now().UTC()
	request := model.NodeEnrollmentRequest{
		ID: requestID, RequestSecretHash: HashNodeSecret(requestSecret), RequestedNodeUUID: nodeUUID,
		RequestedName: input.Name, Hostname: bounded(input.Hostname, 255), OS: bounded(input.OS, 64),
		Arch: bounded(input.Arch, 64), AgentVersion: bounded(input.AgentVersion, 64),
		MachineFingerprint: strings.TrimSpace(input.MachineFingerprint), SourceIP: bounded(input.SourceIP, 64),
		State: model.EnrollmentRequestPending,
	}
	var invitation model.NodeEnrollmentToken
	err = service.store.DB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("token_hash = ?", HashNodeSecret(strings.TrimSpace(input.Token))).First(&invitation).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEnrollmentTokenInvalid
			}
			return err
		}
		if invitation.State != model.EnrollmentTokenActive || invitation.UsedCount >= invitation.MaxUses {
			return ErrEnrollmentTokenUnavailable
		}
		if !invitation.ExpiresAt.After(now) {
			if err := tx.Model(&invitation).Update("state", model.EnrollmentTokenExpired).Error; err != nil {
				return err
			}
			return ErrEnrollmentTokenExpired
		}
		if invitation.ExpectedNodeUUID != nil && *invitation.ExpectedNodeUUID != nodeUUID {
			return ErrEnrollmentUUIDMismatch
		}
		switch invitation.Purpose {
		case model.EnrollmentPurposeRecovery:
			if invitation.RecoveryServerID == nil || invitation.ExpectedNodeUUID == nil {
				return ErrEnrollmentInvalidState
			}
			var server model.MailServer
			if err := tx.First(&server, *invitation.RecoveryServerID).Error; err != nil || server.NodeUUID == nil || *server.NodeUUID != nodeUUID {
				return ErrEnrollmentUUIDMismatch
			}
		case model.EnrollmentPurposeMigration:
			if invitation.RecoveryServerID == nil || invitation.ExpectedNodeUUID != nil {
				return ErrEnrollmentInvalidState
			}
			var server model.MailServer
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&server, *invitation.RecoveryServerID).Error; err != nil {
				return ErrEnrollmentNotFound
			}
			if server.NodeUUID != nil || server.EnrollmentState != model.EnrollmentLegacyApproved || server.TransportMode != model.TransportLegacyHTTP {
				return ErrEnrollmentInvalidState
			}
			var targetCount int64
			if err := tx.Model(&model.NodeEnrollmentRequest{}).
				Joins("JOIN node_enrollment_tokens ON node_enrollment_tokens.id = node_enrollment_requests.enrollment_token_id").
				Where("node_enrollment_tokens.purpose = ? AND node_enrollment_tokens.recovery_server_id = ? AND node_enrollment_requests.state IN ?",
					model.EnrollmentPurposeMigration, *invitation.RecoveryServerID, []string{model.EnrollmentRequestPending, model.EnrollmentRequestApproved}).
				Count(&targetCount).Error; err != nil {
				return err
			}
			if targetCount != 0 {
				return ErrEnrollmentInvalidState
			}
			fallthrough
		case model.EnrollmentPurposeNew:
			var count int64
			if err := tx.Model(&model.MailServer{}).Where("node_uuid = ?", nodeUUID).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return ErrEnrollmentDuplicateUUID
			}
		default:
			return ErrEnrollmentInvalidState
		}
		var pendingCount int64
		if err := tx.Model(&model.NodeEnrollmentRequest{}).
			Where("requested_node_uuid = ? AND state IN ?", nodeUUID, []string{model.EnrollmentRequestPending, model.EnrollmentRequestApproved}).
			Count(&pendingCount).Error; err != nil {
			return err
		}
		if pendingCount != 0 {
			return ErrEnrollmentDuplicateUUID
		}

		request.EnrollmentTokenID = invitation.ID
		result := tx.Model(&model.NodeEnrollmentToken{}).
			Where("id = ? AND state = ? AND used_count < ? AND expires_at > ?", invitation.ID, model.EnrollmentTokenActive, invitation.MaxUses, now).
			Updates(map[string]any{
				"used_count": gorm.Expr("used_count + 1"),
				"state":      gorm.Expr("CASE WHEN used_count + 1 >= max_uses THEN ? ELSE state END", model.EnrollmentTokenUsed),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrEnrollmentTokenUnavailable
		}
		if err := tx.Create(&request).Error; err != nil {
			return err
		}
		return createNodeAudit(tx, "request.claim", "request", request.ID, "node:"+nodeUUID, input.SourceIP, map[string]any{
			"invitation_id": invitation.ID, "token_prefix": invitation.TokenPrefix, "node_uuid": nodeUUID,
		})
	})
	if err != nil {
		if errors.Is(err, ErrEnrollmentTokenExpired) && invitation.ID != 0 {
			_ = service.store.DB().Model(&model.NodeEnrollmentToken{}).
				Where("id = ? AND state = ?", invitation.ID, model.EnrollmentTokenActive).
				Update("state", model.EnrollmentTokenExpired).Error
		}
		return nil, err
	}
	if invitation.AutoApprove {
		if approved, approveErr := service.ApproveRequest(request.ID, "auto:"+invitation.TokenPrefix, "auto-approved by invitation", input.SourceIP); approveErr == nil {
			request = *approved
		}
	}
	return &EnrollmentClaimResult{Request: request, RequestSecret: requestSecret}, nil
}

func (service *NodeEnrollmentService) ListRequests(state string) ([]EnrollmentRequestDetails, error) {
	query := service.store.DB().Order("node_enrollment_requests.created_at DESC")
	if strings.TrimSpace(state) != "" {
		query = query.Where("node_enrollment_requests.state = ?", strings.TrimSpace(state))
	}
	var requests []model.NodeEnrollmentRequest
	if err := query.Find(&requests).Error; err != nil {
		return nil, err
	}
	result := make([]EnrollmentRequestDetails, 0, len(requests))
	for _, request := range requests {
		var invitation model.NodeEnrollmentToken
		if err := service.store.DB().Unscoped().First(&invitation, request.EnrollmentTokenID).Error; err != nil {
			return nil, err
		}
		details := EnrollmentRequestDetails{Request: request, Invitation: invitation, RecentAudits: []model.NodeRegistrationAudit{}}
		if invitation.RecoveryServerID != nil {
			server, err := service.store.GetServer(*invitation.RecoveryServerID)
			if err != nil {
				return nil, err
			}
			details.TargetServer = server
		}
		result = append(result, details)
	}
	return result, nil
}

func (service *NodeEnrollmentService) GetRequest(id string) (*EnrollmentRequestDetails, error) {
	var request model.NodeEnrollmentRequest
	if err := service.store.DB().First(&request, "id = ?", strings.TrimSpace(id)).Error; err != nil {
		return nil, mapNotFound(err)
	}
	var invitation model.NodeEnrollmentToken
	if err := service.store.DB().Unscoped().First(&invitation, request.EnrollmentTokenID).Error; err != nil {
		return nil, err
	}
	details := &EnrollmentRequestDetails{Request: request, Invitation: invitation, RecentAudits: []model.NodeRegistrationAudit{}}
	if invitation.RecoveryServerID != nil {
		server, err := service.store.GetServer(*invitation.RecoveryServerID)
		if err != nil {
			return nil, err
		}
		details.TargetServer = server
	}
	if request.ServerID != nil {
		var credential model.NodeCredential
		if err := service.store.DB().Where("server_id = ?", *request.ServerID).Order("version DESC").First(&credential).Error; err == nil {
			details.Credential = &credential
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	if err := service.store.DB().Where("entity_type = ? AND entity_id = ?", "request", request.ID).
		Order("created_at DESC").Limit(20).Find(&details.RecentAudits).Error; err != nil {
		return nil, err
	}
	return details, nil
}

func (service *NodeEnrollmentService) RequestStatus(id, requestSecret string) (*model.NodeEnrollmentRequest, error) {
	var request model.NodeEnrollmentRequest
	err := service.store.DB().Where("id = ? AND request_secret_hash = ?", strings.TrimSpace(id), HashNodeSecret(strings.TrimSpace(requestSecret))).First(&request).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrEnrollmentRequestSecret
	}
	return &request, err
}

func (service *NodeEnrollmentService) ApproveRequest(id, actor, note, sourceIP string) (*model.NodeEnrollmentRequest, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" || len(note) > 1024 {
		return nil, fmt.Errorf("actor is required and note must not exceed 1024 characters")
	}
	now := service.now().UTC()
	var approved model.NodeEnrollmentRequest
	err := service.store.DB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&approved, "id = ?", strings.TrimSpace(id)).Error; err != nil {
			return mapNotFound(err)
		}
		if approved.State != model.EnrollmentRequestPending {
			return ErrEnrollmentInvalidState
		}
		var invitation model.NodeEnrollmentToken
		if err := tx.Unscoped().First(&invitation, approved.EnrollmentTokenID).Error; err != nil {
			return err
		}
		var server model.MailServer
		switch invitation.Purpose {
		case model.EnrollmentPurposeRecovery:
			if invitation.RecoveryServerID == nil {
				return ErrEnrollmentInvalidState
			}
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&server, *invitation.RecoveryServerID).Error; err != nil {
				return err
			}
			if server.NodeUUID == nil || *server.NodeUUID != approved.RequestedNodeUUID {
				return ErrEnrollmentUUIDMismatch
			}
			if err := tx.Model(&server).Updates(map[string]any{
				"enrollment_state": model.EnrollmentApproved, "agent_version": approved.AgentVersion,
			}).Error; err != nil {
				return err
			}
		case model.EnrollmentPurposeMigration:
			if invitation.RecoveryServerID == nil || invitation.ExpectedNodeUUID != nil {
				return ErrEnrollmentInvalidState
			}
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&server, *invitation.RecoveryServerID).Error; err != nil {
				return err
			}
			if server.NodeUUID != nil || server.EnrollmentState != model.EnrollmentLegacyApproved || server.TransportMode != model.TransportLegacyHTTP {
				return ErrEnrollmentInvalidState
			}
			nodeUUID := approved.RequestedNodeUUID
			result := tx.Model(&model.MailServer{}).
				Where("id = ? AND node_uuid IS NULL AND enrollment_state = ? AND transport_mode = ?", server.ID, model.EnrollmentLegacyApproved, model.TransportLegacyHTTP).
				Updates(map[string]any{
					"node_uuid": &nodeUUID, "enrollment_state": model.EnrollmentApproved, "agent_version": approved.AgentVersion,
				})
			if result.Error != nil {
				if isDuplicateError(result.Error) {
					return ErrEnrollmentDuplicateUUID
				}
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrEnrollmentInvalidState
			}
			server.NodeUUID = &nodeUUID
			server.EnrollmentState = model.EnrollmentApproved
			server.AgentVersion = approved.AgentVersion
		case model.EnrollmentPurposeNew:
			nodeUUID := approved.RequestedNodeUUID
			server = model.MailServer{
				Name: approved.RequestedName, NodeUUID: &nodeUUID, APIHost: "", SMTPHost: "", IMAPHost: "",
				Capacity: 5000, Status: "down", EnrollmentState: model.EnrollmentApproved,
				ConnectionState: model.ConnectionDisconnected, ReadinessState: model.ReadinessUnknown,
				AllocationState: model.AllocationDisabled, TransportMode: model.TransportLegacyHTTP,
				AgentVersion: approved.AgentVersion,
			}
			if err := tx.Create(&server).Error; err != nil {
				if isDuplicateError(err) {
					return ErrEnrollmentDuplicateUUID
				}
				return err
			}
		default:
			return ErrEnrollmentInvalidState
		}
		result := tx.Model(&model.NodeEnrollmentRequest{}).Where("id = ? AND state = ?", approved.ID, model.EnrollmentRequestPending).
			Updates(map[string]any{"state": model.EnrollmentRequestApproved, "reviewed_by": actor, "reviewed_at": &now, "review_note": strings.TrimSpace(note), "server_id": server.ID})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrEnrollmentInvalidState
		}
		approved.State, approved.ReviewedBy, approved.ReviewedAt, approved.ReviewNote, approved.ServerID = model.EnrollmentRequestApproved, actor, &now, strings.TrimSpace(note), &server.ID
		return createNodeAudit(tx, "request.approve", "request", approved.ID, actor, sourceIP, map[string]any{
			"server_id": server.ID, "node_uuid": approved.RequestedNodeUUID, "purpose": invitation.Purpose,
		})
	})
	if err != nil {
		return nil, err
	}
	return &approved, nil
}

func (service *NodeEnrollmentService) RejectRequest(id, actor, note, sourceIP string) error {
	actor = strings.TrimSpace(actor)
	if actor == "" || len(note) > 1024 {
		return fmt.Errorf("actor is required and note must not exceed 1024 characters")
	}
	now := service.now().UTC()
	return service.store.DB().Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.NodeEnrollmentRequest{}).Where("id = ? AND state = ?", strings.TrimSpace(id), model.EnrollmentRequestPending).
			Updates(map[string]any{"state": model.EnrollmentRequestRejected, "reviewed_by": actor, "reviewed_at": &now, "review_note": strings.TrimSpace(note)})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrEnrollmentInvalidState
		}
		return createNodeAudit(tx, "request.reject", "request", strings.TrimSpace(id), actor, sourceIP, map[string]any{"note": strings.TrimSpace(note)})
	})
}

func (service *NodeEnrollmentService) CompleteRequest(id, requestSecret, sourceIP string) (string, *model.NodeCredential, error) {
	credential, err := randomSecret("mhn_", 32)
	if err != nil {
		return "", nil, err
	}
	now := service.now().UTC()
	record := model.NodeCredential{CredentialPrefix: prefix(credential), CredentialHash: HashNodeSecret(credential), State: model.NodeCredentialActive}
	err = service.store.DB().Transaction(func(tx *gorm.DB) error {
		requestID := strings.TrimSpace(id)
		requestSecretHash := HashNodeSecret(strings.TrimSpace(requestSecret))
		var candidate model.NodeEnrollmentRequest
		if err := tx.Where("id = ? AND request_secret_hash = ?", requestID, requestSecretHash).First(&candidate).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEnrollmentRequestSecret
			}
			return err
		}
		if candidate.State != model.EnrollmentRequestApproved || candidate.ServerID == nil {
			return ErrEnrollmentInvalidState
		}
		var server model.MailServer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&server, *candidate.ServerID).Error; err != nil {
			return mapNotFound(err)
		}
		var request model.NodeEnrollmentRequest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND request_secret_hash = ?", requestID, requestSecretHash).First(&request).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEnrollmentRequestSecret
			}
			return err
		}
		if request.State != model.EnrollmentRequestApproved || request.ServerID == nil || *request.ServerID != server.ID {
			return ErrEnrollmentInvalidState
		}
		if server.NodeUUID == nil || *server.NodeUUID != request.RequestedNodeUUID || server.EnrollmentState != model.EnrollmentApproved {
			return ErrEnrollmentInvalidState
		}
		record.ServerID = server.ID
		var version uint64
		if err := tx.Model(&model.NodeCredential{}).Where("server_id = ?", record.ServerID).
			Select("COALESCE(MAX(version), 0)").Scan(&version).Error; err != nil {
			return err
		}
		record.Version = version + 1
		if err := tx.Model(&model.NodeCredential{}).Where("server_id = ? AND state IN ?", record.ServerID, []string{model.NodeCredentialActive, model.NodeCredentialRotating}).
			Updates(map[string]any{"state": model.NodeCredentialRevoked, "revoked_at": &now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		result := tx.Model(&model.NodeEnrollmentRequest{}).Where("id = ? AND state = ?", request.ID, model.EnrollmentRequestApproved).
			Update("state", model.EnrollmentRequestCompleted)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrEnrollmentInvalidState
		}
		return createNodeAudit(tx, "request.complete", "request", request.ID, "node:"+request.RequestedNodeUUID, sourceIP, map[string]any{
			"server_id": record.ServerID, "credential_prefix": record.CredentialPrefix, "credential_version": record.Version,
		})
	})
	if err != nil {
		return "", nil, err
	}
	return credential, &record, nil
}

func (service *NodeEnrollmentService) RotateCredential(serverID uint64, actor, sourceIP string) (string, *model.NodeCredential, error) {
	credential, err := randomSecret("mhn_", 32)
	if err != nil {
		return "", nil, err
	}
	now := service.now().UTC()
	record := model.NodeCredential{ServerID: serverID, CredentialPrefix: prefix(credential), CredentialHash: HashNodeSecret(credential), State: model.NodeCredentialActive}
	err = service.store.DB().Transaction(func(tx *gorm.DB) error {
		var server model.MailServer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&server, serverID).Error; err != nil {
			return mapNotFound(err)
		}
		if server.NodeUUID == nil || server.EnrollmentState != model.EnrollmentApproved {
			return ErrEnrollmentInvalidState
		}
		var version uint64
		if err := tx.Model(&model.NodeCredential{}).Where("server_id = ?", serverID).Select("COALESCE(MAX(version), 0)").Scan(&version).Error; err != nil {
			return err
		}
		record.Version = version + 1
		if err := tx.Model(&model.NodeCredential{}).Where("server_id = ? AND state = ?", serverID, model.NodeCredentialRotating).
			Updates(map[string]any{"state": model.NodeCredentialRevoked, "revoked_at": &now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.NodeCredential{}).Where("server_id = ? AND state = ?", serverID, model.NodeCredentialActive).
			Update("state", model.NodeCredentialRotating).Error; err != nil {
			return err
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		return createNodeAudit(tx, "credential.rotate", "server", fmt.Sprint(serverID), actor, sourceIP, map[string]any{
			"credential_prefix": record.CredentialPrefix, "credential_version": record.Version, "rotated_at": now,
		})
	})
	if err != nil {
		return "", nil, err
	}
	return credential, &record, nil
}

func (service *NodeEnrollmentService) RevokeCredentials(serverID uint64, actor, sourceIP string) error {
	now := service.now().UTC()
	return service.store.DB().Transaction(func(tx *gorm.DB) error {
		var server model.MailServer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&server, serverID).Error; err != nil {
			return mapNotFound(err)
		}
		invalidated := tx.Model(&model.NodeEnrollmentRequest{}).
			Where("server_id = ? AND state = ?", serverID, model.EnrollmentRequestApproved).
			Update("state", model.EnrollmentRequestExpired)
		if invalidated.Error != nil {
			return invalidated.Error
		}
		if err := tx.Model(&model.NodeCredential{}).Where("server_id = ? AND state IN ?", serverID, []string{model.NodeCredentialActive, model.NodeCredentialRotating}).
			Updates(map[string]any{"state": model.NodeCredentialRevoked, "revoked_at": &now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&server).Updates(map[string]any{
			"enrollment_state": model.EnrollmentRevoked, "connection_state": model.ConnectionDisconnected,
			"allocation_state": model.AllocationDisabled, "lease_expires_at": nil,
		}).Error; err != nil {
			return err
		}
		return createNodeAudit(tx, "credential.revoke", "server", fmt.Sprint(serverID), actor, sourceIP, map[string]any{
			"invalidated_requests": invalidated.RowsAffected,
		})
	})
}

func (service *NodeEnrollmentService) ListCredentials(serverID uint64) ([]model.NodeCredential, error) {
	var credentials []model.NodeCredential
	err := service.store.DB().Where("server_id = ?", serverID).Order("version DESC").Find(&credentials).Error
	return credentials, err
}

func (service *NodeEnrollmentService) AuthenticateCredential(rawCredential, nodeUUID string, usedAt time.Time) (*NodePrincipal, error) {
	normalizedUUID, err := normalizeNodeUUID(nodeUUID)
	if err != nil {
		return nil, ErrNodeCredentialInvalid
	}
	var credential model.NodeCredential
	err = service.store.DB().Where("credential_hash = ? AND state IN ? AND (expires_at IS NULL OR expires_at > ?)",
		HashNodeSecret(strings.TrimSpace(rawCredential)), []string{model.NodeCredentialActive, model.NodeCredentialRotating}, usedAt.UTC()).First(&credential).Error
	if err != nil {
		return nil, ErrNodeCredentialInvalid
	}
	var server model.MailServer
	if err := service.store.DB().First(&server, credential.ServerID).Error; err != nil || server.NodeUUID == nil ||
		*server.NodeUUID != normalizedUUID || server.EnrollmentState != model.EnrollmentApproved {
		return nil, ErrNodeCredentialInvalid
	}
	if err := service.store.DB().Model(&model.NodeCredential{}).Where("id = ?", credential.ID).Update("last_used_at", usedAt.UTC()).Error; err != nil {
		return nil, err
	}
	return &NodePrincipal{ServerID: server.ID, NodeUUID: normalizedUUID, CredentialID: credential.ID, CredentialVer: credential.Version}, nil
}

func HashNodeSecret(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func normalizeNodeUUID(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.Version() != 4 || parsed.String() != value {
		return "", fmt.Errorf("invalid UUIDv4")
	}
	return value, nil
}

func validMachineFingerprint(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func randomSecret(prefix string, bytes int) (string, error) {
	raw, err := randomHex(bytes)
	if err != nil {
		return "", err
	}
	return prefix + raw, nil
}

func randomHex(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate secure random value: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func prefix(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

func bounded(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) > max {
		return value[:max]
	}
	return value
}

func sanitizeLabels(labels map[string]string) map[string]string {
	clean := make(map[string]string, len(labels))
	for key, value := range labels {
		key, value = bounded(key, 64), bounded(value, 128)
		if key != "" && len(clean) < 32 {
			clean[key] = value
		}
	}
	return clean
}

func createNodeAudit(tx *gorm.DB, action, entityType, entityID, actor, sourceIP string, details map[string]any) error {
	payload, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return tx.Create(&model.NodeRegistrationAudit{
		Action: action, EntityType: entityType, EntityID: entityID, Actor: actor,
		SourceIP: bounded(sourceIP, 64), Details: string(payload),
	}).Error
}

func mapNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrEnrollmentNotFound
	}
	return err
}

func isDuplicateError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate") || strings.Contains(message, "unique constraint")
}
