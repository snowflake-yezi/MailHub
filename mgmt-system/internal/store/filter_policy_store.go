package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ticket/email-mgmt-system/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type FilterDecisionRecord struct {
	model.FilterDecision
	Mailbox string `gorm:"column:mailbox" json:"mailbox"`
}

const maxFilterJSONPayloadBytes = 4 << 20

var (
	ErrInvalidFilterPolicyRevision = errors.New("invalid filter policy revision")
	ErrInvalidFilterDecision       = errors.New("invalid filter decision")
	ErrFilterDecisionConflict      = errors.New("filter decision key already contains different data")
	ErrInvalidFilterNodeState      = errors.New("invalid filter node state")
	ErrInvalidFilterAudit          = errors.New("invalid filter audit")
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
