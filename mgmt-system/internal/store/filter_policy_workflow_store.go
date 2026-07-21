package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/ticket/email-mgmt-system/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrFilterPolicyImmutable = errors.New("published filter policy revisions are immutable")
	ErrFilterPolicyConflict  = errors.New("filter policy revision changed concurrently")
)

func (s *Store) ListManualFilterRevisions() ([]model.ManualFilterRevision, error) {
	var revisions []model.ManualFilterRevision
	err := s.db.Order("revision DESC").Find(&revisions).Error
	return revisions, err
}

func (s *Store) ListAdFilterRevisions() ([]model.AdFilterRevision, error) {
	var revisions []model.AdFilterRevision
	err := s.db.Order("revision DESC").Find(&revisions).Error
	return revisions, err
}

func (s *Store) ListFilterActiveStates() ([]model.FilterActiveState, error) {
	var states []model.FilterActiveState
	err := s.db.Order("policy_kind ASC").Find(&states).Error
	return states, err
}

func (s *Store) ListFilterNodeStates(policyKind string) ([]model.FilterNodeState, error) {
	if policyKind != "" && !validFilterPolicyKind(policyKind) {
		return nil, ErrInvalidFilterNodeState
	}
	query := s.db.Order("node_id ASC, policy_kind ASC")
	if policyKind != "" {
		query = query.Where("policy_kind = ?", policyKind)
	}
	var states []model.FilterNodeState
	return states, query.Find(&states).Error
}

func (s *Store) CreateManualFilterDraft(draft *model.ManualFilterRevision, actor, requestID string) error {
	if draft == nil || strings.TrimSpace(actor) == "" {
		return ErrInvalidFilterPolicyRevision
	}
	for attempt := 0; attempt < 3; attempt++ {
		candidate := cloneManualRevisionGraph(draft)
		err := s.db.Transaction(func(tx *gorm.DB) error {
			var latest uint64
			if err := tx.Model(&model.ManualFilterRevision{}).Select("COALESCE(MAX(revision), 0)").Scan(&latest).Error; err != nil {
				return err
			}
			candidate.Revision = latest + 1
			candidate.Status = "draft"
			candidate.SchemaVersion = model.FilterPolicySchemaVersionV1
			candidate.Checksum = ""
			candidate.CreatedBy = actor
			candidate.PublishedBy = ""
			candidate.PublishedAt = nil
			if err := tx.Create(candidate).Error; err != nil {
				return err
			}
			return createFilterAuditTx(tx, "manual", "create_draft", "revision", fmt.Sprint(candidate.Revision), candidate.Revision, actor, requestID, map[string]any{
				"base_revision": candidate.BaseRevision,
				"rule_count":    len(candidate.Rules),
			})
		})
		if err == nil {
			*draft = *candidate
			return nil
		}
		if !isDuplicateKeyError(err) {
			return err
		}
	}
	return ErrFilterPolicyConflict
}

func (s *Store) CreateAdFilterDraft(draft *model.AdFilterRevision, actor, requestID string) error {
	if draft == nil || strings.TrimSpace(actor) == "" {
		return ErrInvalidFilterPolicyRevision
	}
	for attempt := 0; attempt < 3; attempt++ {
		candidate := cloneAdRevisionGraph(draft)
		err := s.db.Transaction(func(tx *gorm.DB) error {
			var latest uint64
			if err := tx.Model(&model.AdFilterRevision{}).Select("COALESCE(MAX(revision), 0)").Scan(&latest).Error; err != nil {
				return err
			}
			candidate.Revision = latest + 1
			candidate.Status = "draft"
			candidate.SchemaVersion = model.FilterPolicySchemaVersionV1
			candidate.Checksum = ""
			candidate.CreatedBy = actor
			candidate.PublishedBy = ""
			candidate.PublishedAt = nil
			if err := tx.Create(candidate).Error; err != nil {
				return err
			}
			return createFilterAuditTx(tx, "ad", "create_draft", "revision", fmt.Sprint(candidate.Revision), candidate.Revision, actor, requestID, map[string]any{
				"base_revision":   candidate.BaseRevision,
				"detector_count":  len(candidate.Detectors),
				"composite_count": len(candidate.Composites),
				"weight_count":    len(candidate.Weights),
			})
		})
		if err == nil {
			*draft = *candidate
			return nil
		}
		if !isDuplicateKeyError(err) {
			return err
		}
	}
	return ErrFilterPolicyConflict
}

func (s *Store) ReplaceManualFilterDraft(revision uint64, rules []model.ManualFilterRule, actor, requestID, action string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var current model.ManualFilterRevision
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("revision = ?", revision).First(&current).Error; err != nil {
			return err
		}
		if current.Status != "draft" {
			return ErrFilterPolicyImmutable
		}
		if err := deleteManualGraphTx(tx, current.ID); err != nil {
			return err
		}
		for i := range rules {
			rule := cloneManualRule(rules[i])
			rule.RevisionID = current.ID
			if err := tx.Create(&rule).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&current).UpdateColumn("updated_at", time.Now().UTC()).Error; err != nil {
			return err
		}
		return createFilterAuditTx(tx, "manual", action, "revision", fmt.Sprint(revision), revision, actor, requestID, map[string]any{"rule_count": len(rules)})
	})
}

func (s *Store) ReplaceAdFilterDraft(revision uint64, tagThresholdMilli, quarantineThresholdMilli int64, detectors []model.AdFilterDetector, composites []model.AdFilterComposite, weights []model.AdFilterSymbolWeight, actor, requestID, action string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var current model.AdFilterRevision
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("revision = ?", revision).First(&current).Error; err != nil {
			return err
		}
		if current.Status != "draft" {
			return ErrFilterPolicyImmutable
		}
		if err := deleteAdGraphTx(tx, current.ID); err != nil {
			return err
		}
		for i := range detectors {
			detector := cloneAdDetector(detectors[i])
			detector.RevisionID = current.ID
			if err := tx.Create(&detector).Error; err != nil {
				return err
			}
		}
		for i := range composites {
			composite := cloneAdComposite(composites[i])
			composite.RevisionID = current.ID
			if err := tx.Create(&composite).Error; err != nil {
				return err
			}
		}
		for i := range weights {
			weight := weights[i]
			weight.ID = 0
			weight.RevisionID = current.ID
			if err := tx.Create(&weight).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&current).Updates(map[string]any{
			"tag_threshold_milli": tagThresholdMilli, "quarantine_threshold_milli": quarantineThresholdMilli,
			"updated_at": time.Now().UTC(),
		}).Error; err != nil {
			return err
		}
		return createFilterAuditTx(tx, "ad", action, "revision", fmt.Sprint(revision), revision, actor, requestID, map[string]any{
			"tag_threshold_milli": tagThresholdMilli, "quarantine_threshold_milli": quarantineThresholdMilli,
			"detector_count": len(detectors), "composite_count": len(composites), "weight_count": len(weights),
		})
	})
}

func (s *Store) PublishManualFilterRevision(revision uint64, actor, requestID string, validate func(*model.ManualFilterRevision) (string, error)) (*model.ManualFilterRevision, error) {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		current, err := getManualFilterRevisionDB(tx, revision, true)
		if err != nil {
			return err
		}
		if current.Status != "draft" {
			var active model.FilterActiveState
			if current.Status == "published" && tx.Where("policy_kind = ? AND active_revision = ? AND checksum = ?", "manual", revision, current.Checksum).First(&active).Error == nil {
				return nil
			}
			return ErrFilterPolicyImmutable
		}
		checksum, err := validate(current)
		if err != nil {
			return err
		}
		return publishFilterRevisionTx(tx, "manual", revision, current.ID, checksum, actor, requestID)
	})
	if err != nil {
		return nil, err
	}
	return s.GetManualFilterRevision(revision)
}

func (s *Store) PublishAdFilterRevision(revision uint64, actor, requestID string, validate func(*model.AdFilterRevision) (string, error)) (*model.AdFilterRevision, error) {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		current, err := getAdFilterRevisionDB(tx, revision, true)
		if err != nil {
			return err
		}
		if current.Status != "draft" {
			var active model.FilterActiveState
			if current.Status == "published" && tx.Where("policy_kind = ? AND active_revision = ? AND checksum = ?", "ad", revision, current.Checksum).First(&active).Error == nil {
				return nil
			}
			return ErrFilterPolicyImmutable
		}
		checksum, err := validate(current)
		if err != nil {
			return err
		}
		return publishFilterRevisionTx(tx, "ad", revision, current.ID, checksum, actor, requestID)
	})
	if err != nil {
		return nil, err
	}
	return s.GetAdFilterRevision(revision)
}

func publishFilterRevisionTx(tx *gorm.DB, policyKind string, revision, revisionID uint64, checksum, actor, requestID string) error {
	if len(checksum) != 64 || actor == "" {
		return ErrInvalidFilterPolicyRevision
	}
	now := time.Now().UTC()
	var previous model.FilterActiveState
	previousErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("policy_kind = ?", policyKind).First(&previous).Error
	if previousErr != nil && !errors.Is(previousErr, gorm.ErrRecordNotFound) {
		return previousErr
	}
	revisionModel := any(&model.ManualFilterRevision{})
	if policyKind == "ad" {
		revisionModel = &model.AdFilterRevision{}
	}
	result := tx.Model(revisionModel).Where("id = ? AND status = ?", revisionID, "draft").Updates(map[string]any{
		"status": "published", "checksum": checksum, "published_by": actor, "published_at": now, "updated_at": now,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrFilterPolicyConflict
	}
	if previousErr == nil && previous.ActiveRevision != revision {
		if err := tx.Model(revisionModel).Where("revision = ? AND status = ?", previous.ActiveRevision, "published").Update("status", "retired").Error; err != nil {
			return err
		}
	}
	active := model.FilterActiveState{PolicyKind: policyKind, ActiveRevision: revision, Checksum: checksum, ChangedAt: now, ChangedBy: actor}
	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "policy_kind"}},
		DoUpdates: clause.AssignmentColumns([]string{"active_revision", "checksum", "changed_at", "changed_by"}),
	}).Create(&active).Error; err != nil {
		return err
	}
	var serverIDs []uint64
	if err := tx.Model(&model.MailServer{}).Order("id ASC").Pluck("id", &serverIDs).Error; err != nil {
		return err
	}
	for _, nodeID := range serverIDs {
		state := model.FilterNodeState{NodeID: nodeID, PolicyKind: policyKind, DesiredRevision: revision}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "node_id"}, {Name: "policy_kind"}},
			DoUpdates: clause.Assignments(map[string]any{"desired_revision": revision, "updated_at": now}),
		}).Create(&state).Error; err != nil {
			return err
		}
	}
	return createFilterAuditTx(tx, policyKind, "publish", "revision", fmt.Sprint(revision), revision, actor, requestID, map[string]any{
		"checksum": checksum, "previous_active_revision": previous.ActiveRevision,
	})
}

func getManualFilterRevisionDB(db *gorm.DB, revision uint64, lock bool) (*model.ManualFilterRevision, error) {
	var result model.ManualFilterRevision
	query := db
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.Preload("Rules", func(db *gorm.DB) *gorm.DB { return db.Order("priority ASC, logical_id ASC") }).
		Preload("Rules.Conditions", func(db *gorm.DB) *gorm.DB { return db.Order("position ASC") }).
		Where("revision = ?", revision).First(&result).Error
	return &result, err
}

func getAdFilterRevisionDB(db *gorm.DB, revision uint64, lock bool) (*model.AdFilterRevision, error) {
	var result model.AdFilterRevision
	query := db
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.Preload("Detectors", func(db *gorm.DB) *gorm.DB { return db.Order("symbol ASC, logical_id ASC") }).
		Preload("Detectors.Conditions", func(db *gorm.DB) *gorm.DB { return db.Order("position ASC") }).
		Preload("Composites", func(db *gorm.DB) *gorm.DB { return db.Order("symbol ASC, logical_id ASC") }).
		Preload("Composites.Terms", func(db *gorm.DB) *gorm.DB { return db.Order("group_kind ASC, position ASC") }).
		Preload("Weights", func(db *gorm.DB) *gorm.DB { return db.Order("symbol ASC") }).
		Where("revision = ?", revision).First(&result).Error
	return &result, err
}

func deleteManualGraphTx(tx *gorm.DB, revisionID uint64) error {
	var ruleIDs []uint64
	if err := tx.Model(&model.ManualFilterRule{}).Where("revision_id = ?", revisionID).Pluck("id", &ruleIDs).Error; err != nil {
		return err
	}
	if len(ruleIDs) > 0 {
		if err := tx.Where("rule_id IN ?", ruleIDs).Delete(&model.ManualFilterCondition{}).Error; err != nil {
			return err
		}
	}
	return tx.Where("revision_id = ?", revisionID).Delete(&model.ManualFilterRule{}).Error
}

func deleteAdGraphTx(tx *gorm.DB, revisionID uint64) error {
	var detectorIDs, compositeIDs []uint64
	if err := tx.Model(&model.AdFilterDetector{}).Where("revision_id = ?", revisionID).Pluck("id", &detectorIDs).Error; err != nil {
		return err
	}
	if err := tx.Model(&model.AdFilterComposite{}).Where("revision_id = ?", revisionID).Pluck("id", &compositeIDs).Error; err != nil {
		return err
	}
	if len(detectorIDs) > 0 {
		if err := tx.Where("detector_id IN ?", detectorIDs).Delete(&model.AdFilterCondition{}).Error; err != nil {
			return err
		}
	}
	if len(compositeIDs) > 0 {
		if err := tx.Where("composite_id IN ?", compositeIDs).Delete(&model.AdFilterCompositeTerm{}).Error; err != nil {
			return err
		}
	}
	for _, deletion := range []struct {
		model any
		query string
	}{
		{&model.AdFilterDetector{}, "revision_id = ?"}, {&model.AdFilterComposite{}, "revision_id = ?"}, {&model.AdFilterSymbolWeight{}, "revision_id = ?"},
	} {
		if err := tx.Where(deletion.query, revisionID).Delete(deletion.model).Error; err != nil {
			return err
		}
	}
	return nil
}

func createFilterAuditTx(tx *gorm.DB, policyKind, action, entityType, entityID string, revision uint64, actor, requestID string, changes map[string]any) error {
	payload, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	audit := model.FilterAudit{PolicyKind: policyKind, Action: action, EntityType: entityType, EntityID: entityID, Revision: &revision, Actor: actor, RequestID: requestID, ChangesText: string(payload)}
	return tx.Create(&audit).Error
}

func cloneManualRevisionGraph(source *model.ManualFilterRevision) *model.ManualFilterRevision {
	result := *source
	result.ID = 0
	result.CreatedAt, result.UpdatedAt = time.Time{}, time.Time{}
	result.Rules = make([]model.ManualFilterRule, len(source.Rules))
	for i := range source.Rules {
		result.Rules[i] = cloneManualRule(source.Rules[i])
	}
	return &result
}

func cloneManualRule(source model.ManualFilterRule) model.ManualFilterRule {
	source.ID, source.RevisionID = 0, 0
	source.CreatedAt, source.UpdatedAt = time.Time{}, time.Time{}
	conditions := make([]model.ManualFilterCondition, len(source.Conditions))
	for i := range source.Conditions {
		conditions[i] = source.Conditions[i]
		conditions[i].ID, conditions[i].RuleID = 0, 0
	}
	source.Conditions = conditions
	return source
}

func cloneAdRevisionGraph(source *model.AdFilterRevision) *model.AdFilterRevision {
	result := *source
	result.ID = 0
	result.CreatedAt, result.UpdatedAt = time.Time{}, time.Time{}
	result.Detectors = make([]model.AdFilterDetector, len(source.Detectors))
	for i := range source.Detectors {
		result.Detectors[i] = cloneAdDetector(source.Detectors[i])
	}
	result.Composites = make([]model.AdFilterComposite, len(source.Composites))
	for i := range source.Composites {
		result.Composites[i] = cloneAdComposite(source.Composites[i])
	}
	result.Weights = append([]model.AdFilterSymbolWeight(nil), source.Weights...)
	for i := range result.Weights {
		result.Weights[i].ID, result.Weights[i].RevisionID = 0, 0
	}
	return &result
}

func cloneAdDetector(source model.AdFilterDetector) model.AdFilterDetector {
	source.ID, source.RevisionID = 0, 0
	source.CreatedAt, source.UpdatedAt = time.Time{}, time.Time{}
	conditions := make([]model.AdFilterCondition, len(source.Conditions))
	for i := range source.Conditions {
		conditions[i] = source.Conditions[i]
		conditions[i].ID, conditions[i].DetectorID = 0, 0
	}
	source.Conditions = conditions
	return source
}

func cloneAdComposite(source model.AdFilterComposite) model.AdFilterComposite {
	source.ID, source.RevisionID = 0, 0
	source.CreatedAt, source.UpdatedAt = time.Time{}, time.Time{}
	terms := make([]model.AdFilterCompositeTerm, len(source.Terms))
	for i := range source.Terms {
		terms[i] = source.Terms[i]
		terms[i].ID, terms[i].CompositeID = 0, 0
	}
	source.Terms = terms
	return source
}

func isDuplicateKeyError(err error) bool {
	var mysqlError *drivermysql.MySQLError
	return errors.As(err, &mysqlError) && mysqlError.Number == 1062
}
