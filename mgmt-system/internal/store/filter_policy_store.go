package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ticket/email-mgmt-system/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type FilterDecisionRecord struct {
	model.FilterDecision
	Mailbox string `gorm:"column:mailbox" json:"mailbox"`
}

type FilterQuarantineRecord struct {
	model.FilterQuarantine
	DecisionKey   string    `gorm:"column:decision_key" json:"decision_key"`
	MessageKey    string    `gorm:"column:message_key" json:"message_key"`
	MessageID     string    `gorm:"column:message_id" json:"message_id"`
	Mailbox       string    `gorm:"column:mailbox" json:"mailbox"`
	NodeID        uint64    `gorm:"column:node_id" json:"node_id"`
	AdScoreMilli  int64     `gorm:"column:ad_score_milli" json:"ad_score_milli"`
	EvaluatedAt   time.Time `gorm:"column:evaluated_at" json:"evaluated_at"`
	ServerAPIHost string    `gorm:"column:server_api_host" json:"-"`
	TransportMode string    `gorm:"column:transport_mode" json:"-"`
}

const maxFilterJSONPayloadBytes = 4 << 20

var (
	ErrInvalidFilterPolicyRevision = errors.New("invalid filter policy revision")
	ErrInvalidFilterDecision       = errors.New("invalid filter decision")
	ErrFilterDecisionConflict      = errors.New("filter decision key already contains different data")
	ErrInvalidFilterNodeState      = errors.New("invalid filter node state")
	ErrInvalidFilterAudit          = errors.New("invalid filter audit")
	ErrInvalidFilterQuarantine     = errors.New("invalid filter quarantine")
	ErrFilterQuarantineConflict    = errors.New("filter quarantine state conflict")
)

func (s *Store) CreateManualFilterRevision(revision *model.ManualFilterRevision) error {
	if revision == nil || revision.Revision == 0 || revision.SchemaVersion != model.FilterPolicySchemaVersionV1 ||
		revision.Status != "draft" || revision.Checksum != "" || strings.TrimSpace(revision.CreatedBy) == "" {
		return ErrInvalidFilterPolicyRevision
	}
	return s.db.Create(revision).Error
}

func (s *Store) CreateAdFilterRevision(revision *model.AdFilterRevision) error {
	if revision == nil || revision.Revision == 0 || revision.SchemaVersion != model.FilterPolicySchemaVersionV1 ||
		revision.Status != "draft" || revision.Checksum != "" || strings.TrimSpace(revision.CreatedBy) == "" {
		return ErrInvalidFilterPolicyRevision
	}
	return s.db.Create(revision).Error
}

func (s *Store) GetManualFilterRevision(revision uint64) (*model.ManualFilterRevision, error) {
	var result model.ManualFilterRevision
	err := s.db.
		Preload("Rules", func(db *gorm.DB) *gorm.DB { return db.Order("priority ASC, logical_id ASC") }).
		Preload("Rules.Conditions", func(db *gorm.DB) *gorm.DB { return db.Order("position ASC") }).
		Where("revision = ?", revision).
		First(&result).Error
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *Store) GetAdFilterRevision(revision uint64) (*model.AdFilterRevision, error) {
	var result model.AdFilterRevision
	err := s.db.
		Preload("Detectors", func(db *gorm.DB) *gorm.DB { return db.Order("symbol ASC, logical_id ASC") }).
		Preload("Detectors.Conditions", func(db *gorm.DB) *gorm.DB { return db.Order("position ASC") }).
		Preload("Composites", func(db *gorm.DB) *gorm.DB { return db.Order("symbol ASC, logical_id ASC") }).
		Preload("Composites.Terms", func(db *gorm.DB) *gorm.DB { return db.Order("group_kind ASC, position ASC") }).
		Preload("Weights", func(db *gorm.DB) *gorm.DB { return db.Order("symbol ASC") }).
		Where("revision = ?", revision).
		First(&result).Error
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *Store) GetFilterActiveState(policyKind string) (*model.FilterActiveState, error) {
	if !validFilterPolicyKind(policyKind) {
		return nil, ErrInvalidFilterPolicyRevision
	}
	var state model.FilterActiveState
	if err := s.db.Where("policy_kind = ?", policyKind).First(&state).Error; err != nil {
		return nil, err
	}
	return &state, nil
}

// SaveFilterDecision is idempotent by decision_key. A retry with identical
// contents succeeds; reusing the key for a different decision is rejected.
func (s *Store) SaveFilterDecision(decision *model.FilterDecision) error {
	if err := validateFilterDecision(decision); err != nil {
		return err
	}
	if err := s.db.Create(decision).Error; err == nil {
		return nil
	} else {
		var existing model.FilterDecision
		if findErr := s.db.Where("decision_key = ?", decision.DecisionKey).First(&existing).Error; findErr != nil {
			return err
		}
		if !sameFilterDecision(existing, *decision) {
			return ErrFilterDecisionConflict
		}
		decision.ID = existing.ID
		decision.CreatedAt = existing.CreatedAt
		return nil
	}
}

// SaveFilterDecisionAndQuarantine commits the ready outbox event as one fact.
// Retries with the same decision/quarantine pair are idempotent.
func (s *Store) SaveFilterDecisionAndQuarantine(decision *model.FilterDecision, quarantine *model.FilterQuarantine) error {
	if quarantine == nil {
		return s.SaveFilterDecision(decision)
	}
	if err := validateFilterDecision(decision); err != nil {
		return err
	}
	if strings.TrimSpace(quarantine.QuarantineKey) == "" || quarantine.ExpiresAt.IsZero() || quarantine.Status != "quarantined" {
		return ErrInvalidFilterQuarantine
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := saveFilterDecisionTx(tx, decision); err != nil {
			return err
		}
		quarantine.DecisionID = decision.ID
		if err := tx.Create(quarantine).Error; err == nil {
			return nil
		}
		var existing model.FilterQuarantine
		if err := tx.Where("quarantine_key = ?", quarantine.QuarantineKey).First(&existing).Error; err != nil {
			return err
		}
		if existing.DecisionID != decision.ID || existing.OriginalMaildirKey != quarantine.OriginalMaildirKey || !existing.ExpiresAt.Equal(quarantine.ExpiresAt) {
			return ErrFilterQuarantineConflict
		}
		*quarantine = existing
		return nil
	})
}

func saveFilterDecisionTx(tx *gorm.DB, decision *model.FilterDecision) error {
	if err := tx.Create(decision).Error; err == nil {
		return nil
	}
	var existing model.FilterDecision
	if err := tx.Where("decision_key = ?", decision.DecisionKey).First(&existing).Error; err != nil {
		return err
	}
	if !sameFilterDecision(existing, *decision) {
		return ErrFilterDecisionConflict
	}
	decision.ID = existing.ID
	decision.CreatedAt = existing.CreatedAt
	return nil
}

func (s *Store) ListFilterQuarantines(page, size int, status string) ([]FilterQuarantineRecord, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	query := s.db.Table("filter_quarantines")
	if status != "" {
		if !validQuarantineStatus(status) {
			return nil, 0, ErrInvalidFilterQuarantine
		}
		query = query.Where("filter_quarantines.status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var records []FilterQuarantineRecord
	err := query.Select("filter_quarantines.*, filter_decisions.decision_key, filter_decisions.message_key, filter_decisions.message_id, filter_decisions.node_id, filter_decisions.ad_score_milli, filter_decisions.evaluated_at, mailbox_accounts.email_address AS mailbox, mail_servers.api_host AS server_api_host, mail_servers.transport_mode AS transport_mode").
		Joins("JOIN filter_decisions ON filter_decisions.id = filter_quarantines.decision_id").
		Joins("JOIN mailbox_accounts ON mailbox_accounts.id = filter_decisions.mailbox_account_id").
		Joins("JOIN mail_servers ON mail_servers.id = filter_decisions.node_id").
		Order("filter_quarantines.created_at DESC, filter_quarantines.id DESC").
		Offset((page - 1) * size).Limit(size).Scan(&records).Error
	return records, total, err
}

func (s *Store) GetFilterQuarantine(key string) (*FilterQuarantineRecord, error) {
	var record FilterQuarantineRecord
	err := s.db.Table("filter_quarantines").
		Select("filter_quarantines.*, filter_decisions.decision_key, filter_decisions.message_key, filter_decisions.message_id, filter_decisions.node_id, filter_decisions.ad_score_milli, filter_decisions.evaluated_at, mailbox_accounts.email_address AS mailbox, mail_servers.api_host AS server_api_host, mail_servers.transport_mode AS transport_mode").
		Joins("JOIN filter_decisions ON filter_decisions.id = filter_quarantines.decision_id").
		Joins("JOIN mailbox_accounts ON mailbox_accounts.id = filter_decisions.mailbox_account_id").
		Joins("JOIN mail_servers ON mail_servers.id = filter_decisions.node_id").
		Where("filter_quarantines.quarantine_key = ?", key).First(&record).Error
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *Store) BeginFilterQuarantineRelease(key, operationID, actor string) (*FilterQuarantineRecord, error) {
	if strings.TrimSpace(operationID) == "" || strings.TrimSpace(actor) == "" {
		return nil, ErrInvalidFilterQuarantine
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var value model.FilterQuarantine
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("quarantine_key = ?", key).First(&value).Error; err != nil {
			return err
		}
		switch value.Status {
		case "quarantined":
			return tx.Model(&value).Updates(map[string]any{
				"status": "releasing", "release_operation_id": operationID, "reviewed_by": actor,
				"last_error": "", "updated_at": time.Now(),
			}).Error
		case "release_failed":
			if value.ReleaseOperationID == "" {
				value.ReleaseOperationID = operationID
			}
			return tx.Model(&value).Updates(map[string]any{
				"status": "releasing", "release_operation_id": value.ReleaseOperationID,
				"reviewed_by": actor, "last_error": "", "updated_at": time.Now(),
			}).Error
		case "releasing", "released":
			return nil
		default:
			return ErrFilterQuarantineConflict
		}
	})
	if err != nil {
		return nil, err
	}
	return s.GetFilterQuarantine(key)
}

func (s *Store) CompleteFilterQuarantineRelease(key, operationID, status, receiptText, lastError, feedbackLabel, actor string) error {
	if status != "released" && status != "release_failed" {
		return ErrInvalidFilterQuarantine
	}
	now := time.Now()
	updates := map[string]any{
		"status": status, "release_receipt_text": receiptText, "last_error": lastError,
		"reviewed_by": actor, "reviewed_at": &now, "updated_at": now,
	}
	if status == "released" {
		if feedbackLabel != "false_positive" {
			feedbackLabel = "uncertain"
		}
		updates["feedback_label"] = feedbackLabel
	}
	result := s.db.Model(&model.FilterQuarantine{}).
		Where("quarantine_key = ? AND release_operation_id = ? AND status IN ?", key, operationID, []string{"releasing", status}).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrFilterQuarantineConflict
	}
	return nil
}

func (s *Store) ReviewFilterQuarantine(key, status, label, note, actor string) error {
	if status != "confirmed_ad" || label != "confirmed_ad" || strings.TrimSpace(actor) == "" {
		return ErrInvalidFilterQuarantine
	}
	now := time.Now()
	result := s.db.Model(&model.FilterQuarantine{}).
		Where("quarantine_key = ? AND status IN ?", key, []string{"quarantined", "confirmed_ad"}).
		Updates(map[string]any{"status": status, "feedback_label": label, "review_note": note, "reviewed_by": actor, "reviewed_at": &now, "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrFilterQuarantineConflict
	}
	return nil
}

func (s *Store) MarkFilterQuarantinesExpired(nodeID uint64, keys []string) error {
	if nodeID == 0 || len(keys) == 0 {
		return nil
	}
	return s.db.Model(&model.FilterQuarantine{}).
		Where("quarantine_key IN ? AND status IN ? AND decision_id IN (SELECT id FROM filter_decisions WHERE node_id = ?)", keys, []string{"quarantined", "releasing", "release_failed", "confirmed_ad"}, nodeID).
		Updates(map[string]any{"status": "expired", "updated_at": time.Now()}).Error
}

func (s *Store) ListFilterDecisions(page, size int, action string) ([]FilterDecisionRecord, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	query := s.db.Table("filter_decisions")
	if action != "" {
		if !validFilterAction(action) {
			return nil, 0, ErrInvalidFilterDecision
		}
		query = query.Where("filter_decisions.final_action = ?", action)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var result []FilterDecisionRecord
	err := query.
		Select("filter_decisions.*, mailbox_accounts.email_address AS mailbox").
		Joins("LEFT JOIN mailbox_accounts ON mailbox_accounts.id = filter_decisions.mailbox_account_id").
		Order("filter_decisions.evaluated_at DESC, filter_decisions.id DESC").
		Offset((page - 1) * size).Limit(size).Scan(&result).Error
	return result, total, err
}

func (s *Store) GetFilterDecision(decisionKey string) (*FilterDecisionRecord, error) {
	var result FilterDecisionRecord
	err := s.db.Table("filter_decisions").
		Select("filter_decisions.*, mailbox_accounts.email_address AS mailbox").
		Joins("LEFT JOIN mailbox_accounts ON mailbox_accounts.id = filter_decisions.mailbox_account_id").
		Where("filter_decisions.decision_key = ?", decisionKey).First(&result).Error
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *Store) UpsertFilterNodeState(state *model.FilterNodeState) error {
	if state == nil || state.NodeID == 0 || !validFilterPolicyKind(state.PolicyKind) ||
		(state.AppliedRevision > 0 && len(state.Checksum) != 64) {
		return ErrInvalidFilterNodeState
	}
	return s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "node_id"}, {Name: "policy_kind"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"desired_revision", "applied_revision", "checksum", "boot_id", "last_error", "applied_at", "updated_at",
		}),
	}).Create(state).Error
}

func (s *Store) CreateFilterAudit(audit *model.FilterAudit) error {
	if audit == nil || strings.TrimSpace(audit.PolicyKind) == "" || strings.TrimSpace(audit.Action) == "" ||
		strings.TrimSpace(audit.EntityType) == "" || strings.TrimSpace(audit.EntityID) == "" ||
		strings.TrimSpace(audit.Actor) == "" {
		return ErrInvalidFilterAudit
	}
	if err := validateJSONObject(audit.ChangesText); err != nil {
		return fmt.Errorf("%w: changes_text: %v", ErrInvalidFilterAudit, err)
	}
	return s.db.Create(audit).Error
}

func validateFilterDecision(decision *model.FilterDecision) error {
	if decision == nil || decision.SchemaVersion != model.FilterPolicySchemaVersionV1 ||
		strings.TrimSpace(decision.DecisionKey) == "" || strings.TrimSpace(decision.MessageKey) == "" ||
		decision.MailboxAccountID == 0 || decision.NodeID == 0 || decision.EvaluatedAt.IsZero() ||
		!validFilterAction(decision.ManualAction) || !validFilterAction(decision.AdAction) || !validFilterAction(decision.FinalAction) {
		return ErrInvalidFilterDecision
	}
	for name, payload := range map[string]string{
		"reasons_text":        decision.ReasonsText,
		"ad_symbols_text":     decision.AdSymbolsText,
		"shadow_results_text": decision.ShadowResultsText,
		"parse_warnings_text": decision.ParseWarningsText,
	} {
		if err := validateJSONArray(payload); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrInvalidFilterDecision, name, err)
		}
	}
	return nil
}

func sameFilterDecision(left, right model.FilterDecision) bool {
	return left.SchemaVersion == right.SchemaVersion &&
		left.DecisionKey == right.DecisionKey &&
		left.MessageKey == right.MessageKey &&
		left.MessageID == right.MessageID &&
		left.MailboxAccountID == right.MailboxAccountID &&
		left.NodeID == right.NodeID &&
		left.ManualRevision == right.ManualRevision &&
		left.AdRevision == right.AdRevision &&
		left.ManualAction == right.ManualAction &&
		left.AdAction == right.AdAction &&
		left.FinalAction == right.FinalAction &&
		left.AdScoreMilli == right.AdScoreMilli &&
		left.ReasonsText == right.ReasonsText &&
		left.AdSymbolsText == right.AdSymbolsText &&
		left.ShadowResultsText == right.ShadowResultsText &&
		left.ParseWarningsText == right.ParseWarningsText &&
		left.EvaluatedAt.Equal(right.EvaluatedAt)
}

func validateJSONArray(payload string) error {
	var value []json.RawMessage
	if err := decodeFilterJSON(payload, &value); err != nil {
		return err
	}
	if value == nil {
		return errors.New("must be a JSON array")
	}
	return nil
}

func validateJSONObject(payload string) error {
	var value map[string]json.RawMessage
	if err := decodeFilterJSON(payload, &value); err != nil {
		return err
	}
	if value == nil {
		return errors.New("must be a JSON object")
	}
	return nil
}

func decodeFilterJSON(payload string, target any) error {
	if payload == "" {
		return errors.New("must not be empty")
	}
	if len(payload) > maxFilterJSONPayloadBytes {
		return fmt.Errorf("exceeds %d bytes", maxFilterJSONPayloadBytes)
	}
	decoder := json.NewDecoder(bytes.NewBufferString(payload))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func validFilterPolicyKind(value string) bool {
	return value == "manual" || value == "ad"
}

func validFilterAction(value string) bool {
	return value == "allow" || value == "tag" || value == "quarantine"
}

func validQuarantineStatus(value string) bool {
	switch value {
	case "quarantined", "releasing", "released", "release_failed", "confirmed_ad", "expired":
		return true
	default:
		return false
	}
}
