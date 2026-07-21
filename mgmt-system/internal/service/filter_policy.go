package service

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ticket/email-filter-contract"
	"github.com/ticket/email-mgmt-system/internal/mailboxaddr"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/store"
	"gorm.io/gorm"
)

//go:embed ad_seed_v1.json
var adSeedV1 []byte

const AdSeedV1 = "ad-seed-v1"

var (
	ErrFilterPolicyDraftRequired = errors.New("filter policy revision is not an editable draft")
	ErrFilterPolicySeed          = errors.New("unknown filter policy seed")
	ErrFilterDecisionNode        = errors.New("filter decision node does not own mailbox")
)

type FilterPolicyService struct {
	store *store.Store
}

type FilterPolicyValidation struct {
	Valid       bool   `json:"valid"`
	Checksum    string `json:"checksum,omitempty"`
	BundleBytes int    `json:"bundle_bytes,omitempty"`
	Error       string `json:"error,omitempty"`
}

type ManualFilterRevisionView struct {
	Revision      uint64                      `json:"revision"`
	Status        string                      `json:"status"`
	BaseRevision  *uint64                     `json:"base_revision,omitempty"`
	SchemaVersion int                         `json:"schema_version"`
	Checksum      string                      `json:"checksum"`
	CreatedBy     string                      `json:"created_by"`
	PublishedBy   string                      `json:"published_by,omitempty"`
	CreatedAt     time.Time                   `json:"created_at"`
	UpdatedAt     time.Time                   `json:"updated_at"`
	PublishedAt   *time.Time                  `json:"published_at,omitempty"`
	Rules         []filtercontract.ManualRule `json:"rules"`
}

type AdFilterRevisionView struct {
	Revision            uint64                        `json:"revision"`
	Status              string                        `json:"status"`
	BaseRevision        *uint64                       `json:"base_revision,omitempty"`
	SchemaVersion       int                           `json:"schema_version"`
	Checksum            string                        `json:"checksum"`
	TagThreshold        filtercontract.Score          `json:"tag_threshold"`
	QuarantineThreshold filtercontract.Score          `json:"quarantine_threshold"`
	CreatedBy           string                        `json:"created_by"`
	PublishedBy         string                        `json:"published_by,omitempty"`
	CreatedAt           time.Time                     `json:"created_at"`
	UpdatedAt           time.Time                     `json:"updated_at"`
	PublishedAt         *time.Time                    `json:"published_at,omitempty"`
	Detectors           []filtercontract.AdDetector   `json:"detectors"`
	Composites          []filtercontract.AdComposite  `json:"composites"`
	Weights             []filtercontract.SymbolWeight `json:"weights"`
}

type FilterPolicyStatus struct {
	ActiveStates []model.FilterActiveState `json:"active_states"`
	NodeStates   []model.FilterNodeState   `json:"node_states"`
}

type FilterDecisionView struct {
	ID             uint64                          `json:"id"`
	DecisionKey    string                          `json:"decision_key"`
	MessageKey     string                          `json:"message_key"`
	MessageID      string                          `json:"message_id,omitempty"`
	Mailbox        string                          `json:"mailbox"`
	NodeID         uint64                          `json:"node_id"`
	ManualRevision uint64                          `json:"manual_revision"`
	AdRevision     uint64                          `json:"ad_revision"`
	ManualAction   string                          `json:"manual_action"`
	AdAction       string                          `json:"ad_action"`
	FinalAction    string                          `json:"final_action"`
	AdScore        filtercontract.Score            `json:"ad_score"`
	Reasons        []filtercontract.DecisionReason `json:"reasons"`
	AdSymbols      []filtercontract.AdSymbolResult `json:"ad_symbols"`
	ShadowResults  []filtercontract.ShadowResult   `json:"shadow_results"`
	ParseWarnings  []string                        `json:"parse_warnings"`
	EvaluatedAt    time.Time                       `json:"evaluated_at"`
}

type FilterDecisionPage struct {
	Items []FilterDecisionView `json:"items"`
	Total int64                `json:"total"`
	Page  int                  `json:"page"`
	Size  int                  `json:"size"`
}

type FilterQuarantineView struct {
	store.FilterQuarantineRecord
	ReleaseReceipt *filtercontract.ReleaseReceipt `json:"release_receipt,omitempty"`
}

type FilterQuarantinePage struct {
	Items []FilterQuarantineView `json:"items"`
	Total int64                  `json:"total"`
	Page  int                    `json:"page"`
	Size  int                    `json:"size"`
}

func NewFilterPolicyService(s *store.Store) *FilterPolicyService {
	return &FilterPolicyService{store: s}
}

func (s *FilterPolicyService) ListManualRevisions() ([]model.ManualFilterRevision, error) {
	return s.store.ListManualFilterRevisions()
}

func (s *FilterPolicyService) ListAdRevisions() ([]model.AdFilterRevision, error) {
	return s.store.ListAdFilterRevisions()
}

func (s *FilterPolicyService) GetManualRevision(revision uint64) (*ManualFilterRevisionView, error) {
	value, err := s.store.GetManualFilterRevision(revision)
	if err != nil {
		return nil, err
	}
	return manualView(value)
}

func (s *FilterPolicyService) GetAdRevision(revision uint64) (*AdFilterRevisionView, error) {
	value, err := s.store.GetAdFilterRevision(revision)
	if err != nil {
		return nil, err
	}
	return adView(value)
}

func (s *FilterPolicyService) CreateManualDraft(baseRevision *uint64, actor, requestID string) (*ManualFilterRevisionView, error) {
	draft := &model.ManualFilterRevision{BaseRevision: cloneUint64Pointer(baseRevision)}
	if baseRevision != nil {
		base, err := s.store.GetManualFilterRevision(*baseRevision)
		if err != nil {
			return nil, err
		}
		if base.Status == "draft" {
			return nil, ErrFilterPolicyDraftRequired
		}
		draft.Rules = base.Rules
	}
	if err := s.store.CreateManualFilterDraft(draft, actor, requestID); err != nil {
		return nil, err
	}
	return s.GetManualRevision(draft.Revision)
}

func (s *FilterPolicyService) CreateAdDraft(baseRevision *uint64, seed, actor, requestID string) (*AdFilterRevisionView, error) {
	if baseRevision != nil && seed != "" {
		return nil, &filtercontract.ContractError{Code: filtercontract.ErrorInvalidValue, Path: "seed", Message: "base_revision and seed are mutually exclusive"}
	}
	draft := &model.AdFilterRevision{BaseRevision: cloneUint64Pointer(baseRevision)}
	if baseRevision != nil {
		base, err := s.store.GetAdFilterRevision(*baseRevision)
		if err != nil {
			return nil, err
		}
		if base.Status == "draft" {
			return nil, ErrFilterPolicyDraftRequired
		}
		draft.TagThresholdMilli = base.TagThresholdMilli
		draft.QuarantineThresholdMilli = base.QuarantineThresholdMilli
		draft.Detectors, draft.Composites, draft.Weights = base.Detectors, base.Composites, base.Weights
	} else if seed != "" {
		if seed != AdSeedV1 {
			return nil, ErrFilterPolicySeed
		}
		var bundle filtercontract.AdBundle
		if err := filtercontract.DecodeStrict(adSeedV1, &bundle); err != nil {
			return nil, fmt.Errorf("decode embedded seed: %w", err)
		}
		bundle.Checksum = ""
		if checksum, err := bundle.CalculatedChecksum(); err != nil {
			return nil, err
		} else {
			bundle.Checksum = checksum
		}
		if err := bundle.Validate(); err != nil {
			return nil, fmt.Errorf("validate embedded seed: %w", err)
		}
		draft = adModelFromBundle(bundle)
	}
	if err := s.store.CreateAdFilterDraft(draft, actor, requestID); err != nil {
		return nil, err
	}
	return s.GetAdRevision(draft.Revision)
}

func (s *FilterPolicyService) ValidateManualRevision(revision uint64) (FilterPolicyValidation, error) {
	value, err := s.store.GetManualFilterRevision(revision)
	if err != nil {
		return FilterPolicyValidation{}, err
	}
	bundle, err := manualBundle(value, true)
	return validationResult(bundle, err), nil
}

func (s *FilterPolicyService) ValidateAdRevision(revision uint64) (FilterPolicyValidation, error) {
	value, err := s.store.GetAdFilterRevision(revision)
	if err != nil {
		return FilterPolicyValidation{}, err
	}
	bundle, err := adBundle(value, true)
	return validationResult(bundle, err), nil
}

func (s *FilterPolicyService) PublishManualRevision(revision uint64, actor, requestID string) (*ManualFilterRevisionView, error) {
	published, err := s.store.PublishManualFilterRevision(revision, actor, requestID, func(value *model.ManualFilterRevision) (string, error) {
		bundle, err := manualBundle(value, true)
		if err != nil {
			return "", err
		}
		return bundle.Checksum, nil
	})
	if err != nil {
		return nil, err
	}
	return manualView(published)
}

func (s *FilterPolicyService) PublishAdRevision(revision uint64, actor, requestID string) (*AdFilterRevisionView, error) {
	published, err := s.store.PublishAdFilterRevision(revision, actor, requestID, func(value *model.AdFilterRevision) (string, error) {
		bundle, err := adBundle(value, true)
		if err != nil {
			return "", err
		}
		return bundle.Checksum, nil
	})
	if err != nil {
		return nil, err
	}
	return adView(published)
}

func (s *FilterPolicyService) PutManualRules(revision uint64, rules []filtercontract.ManualRule, actor, requestID, action string) (*ManualFilterRevisionView, error) {
	value, err := s.store.GetManualFilterRevision(revision)
	if err != nil {
		return nil, err
	}
	if value.Status != "draft" {
		return nil, ErrFilterPolicyDraftRequired
	}
	models := make([]model.ManualFilterRule, len(rules))
	logicalIDs := make(map[string]struct{}, len(rules))
	for i := range rules {
		rule, err := normalizeManualRule(rules[i])
		if err != nil {
			return nil, err
		}
		if _, exists := logicalIDs[rule.LogicalID]; exists {
			return nil, &filtercontract.ContractError{Code: filtercontract.ErrorInvalidValue, Path: "rules", Message: "logical_id must be unique"}
		}
		logicalIDs[rule.LogicalID] = struct{}{}
		models[i], err = manualRuleModel(rule)
		if err != nil {
			return nil, err
		}
	}
	if err := s.store.ReplaceManualFilterDraft(revision, models, actor, requestID, action); err != nil {
		return nil, err
	}
	return s.GetManualRevision(revision)
}

func (s *FilterPolicyService) AddManualRule(revision uint64, rule filtercontract.ManualRule, actor, requestID string) (*ManualFilterRevisionView, error) {
	view, err := s.GetManualRevision(revision)
	if err != nil {
		return nil, err
	}
	if view.Status != "draft" {
		return nil, ErrFilterPolicyDraftRequired
	}
	for _, existing := range view.Rules {
		if existing.LogicalID == rule.LogicalID && rule.LogicalID != "" {
			return nil, store.ErrFilterPolicyConflict
		}
	}
	return s.PutManualRules(revision, append(view.Rules, rule), actor, requestID, "add_rule")
}

func (s *FilterPolicyService) UpdateManualRule(revision uint64, logicalID string, rule filtercontract.ManualRule, actor, requestID string) (*ManualFilterRevisionView, error) {
	view, err := s.GetManualRevision(revision)
	if err != nil {
		return nil, err
	}
	found := false
	rule.LogicalID = logicalID
	for i := range view.Rules {
		if view.Rules[i].LogicalID == logicalID {
			view.Rules[i], found = rule, true
		}
	}
	if !found {
		return nil, gorm.ErrRecordNotFound
	}
	return s.PutManualRules(revision, view.Rules, actor, requestID, "update_rule")
}

func (s *FilterPolicyService) DeleteManualRule(revision uint64, logicalID, actor, requestID string) (*ManualFilterRevisionView, error) {
	view, err := s.GetManualRevision(revision)
	if err != nil {
		return nil, err
	}
	rules := make([]filtercontract.ManualRule, 0, len(view.Rules))
	found := false
	for _, rule := range view.Rules {
		if rule.LogicalID == logicalID {
			found = true
			continue
		}
		rules = append(rules, rule)
	}
	if !found {
		return nil, gorm.ErrRecordNotFound
	}
	return s.PutManualRules(revision, rules, actor, requestID, "delete_rule")
}

func (s *FilterPolicyService) PutAdThresholds(revision uint64, tag, quarantine filtercontract.Score, actor, requestID string) (*AdFilterRevisionView, error) {
	view, err := s.GetAdRevision(revision)
	if err != nil {
		return nil, err
	}
	view.TagThreshold, view.QuarantineThreshold = tag, quarantine
	return s.replaceAdView(view, actor, requestID, "update_thresholds")
}

func (s *FilterPolicyService) AddAdDetector(revision uint64, detector filtercontract.AdDetector, actor, requestID string) (*AdFilterRevisionView, error) {
	view, err := s.GetAdRevision(revision)
	if err != nil {
		return nil, err
	}
	return s.putAdDetectors(view, append(view.Detectors, detector), actor, requestID, "add_detector")
}

func (s *FilterPolicyService) UpdateAdDetector(revision uint64, logicalID string, detector filtercontract.AdDetector, actor, requestID string) (*AdFilterRevisionView, error) {
	view, err := s.GetAdRevision(revision)
	if err != nil {
		return nil, err
	}
	found := false
	detector.LogicalID = logicalID
	for i := range view.Detectors {
		if view.Detectors[i].LogicalID == logicalID {
			view.Detectors[i], found = detector, true
		}
	}
	if !found {
		return nil, gorm.ErrRecordNotFound
	}
	return s.putAdDetectors(view, view.Detectors, actor, requestID, "update_detector")
}

func (s *FilterPolicyService) DeleteAdDetector(revision uint64, logicalID, actor, requestID string) (*AdFilterRevisionView, error) {
	view, err := s.GetAdRevision(revision)
	if err != nil {
		return nil, err
	}
	values := make([]filtercontract.AdDetector, 0, len(view.Detectors))
	found := false
	for _, value := range view.Detectors {
		if value.LogicalID == logicalID {
			found = true
			continue
		}
		values = append(values, value)
	}
	if !found {
		return nil, gorm.ErrRecordNotFound
	}
	return s.putAdDetectors(view, values, actor, requestID, "delete_detector")
}

func (s *FilterPolicyService) AddAdComposite(revision uint64, composite filtercontract.AdComposite, actor, requestID string) (*AdFilterRevisionView, error) {
	view, err := s.GetAdRevision(revision)
	if err != nil {
		return nil, err
	}
	return s.putAdComposites(view, append(view.Composites, composite), actor, requestID, "add_composite")
}

func (s *FilterPolicyService) UpdateAdComposite(revision uint64, logicalID string, composite filtercontract.AdComposite, actor, requestID string) (*AdFilterRevisionView, error) {
	view, err := s.GetAdRevision(revision)
	if err != nil {
		return nil, err
	}
	found := false
	composite.LogicalID = logicalID
	for i := range view.Composites {
		if view.Composites[i].LogicalID == logicalID {
			view.Composites[i], found = composite, true
		}
	}
	if !found {
		return nil, gorm.ErrRecordNotFound
	}
	return s.putAdComposites(view, view.Composites, actor, requestID, "update_composite")
}

func (s *FilterPolicyService) DeleteAdComposite(revision uint64, logicalID, actor, requestID string) (*AdFilterRevisionView, error) {
	view, err := s.GetAdRevision(revision)
	if err != nil {
		return nil, err
	}
	values := make([]filtercontract.AdComposite, 0, len(view.Composites))
	found := false
	for _, value := range view.Composites {
		if value.LogicalID == logicalID {
			found = true
			continue
		}
		values = append(values, value)
	}
	if !found {
		return nil, gorm.ErrRecordNotFound
	}
	return s.putAdComposites(view, values, actor, requestID, "delete_composite")
}

func (s *FilterPolicyService) PutAdWeight(revision uint64, symbol string, score filtercontract.Score, actor, requestID string) (*AdFilterRevisionView, error) {
	view, err := s.GetAdRevision(revision)
	if err != nil {
		return nil, err
	}
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	weight := filtercontract.SymbolWeight{Symbol: symbol, Score: score}
	if err := filtercontract.ValidateSymbolWeight(weight); err != nil {
		return nil, err
	}
	found := false
	for i := range view.Weights {
		if view.Weights[i].Symbol == symbol {
			view.Weights[i], found = weight, true
		}
	}
	if !found {
		view.Weights = append(view.Weights, weight)
	}
	return s.replaceAdView(view, actor, requestID, "put_weight")
}

func (s *FilterPolicyService) DeleteAdWeight(revision uint64, symbol, actor, requestID string) (*AdFilterRevisionView, error) {
	view, err := s.GetAdRevision(revision)
	if err != nil {
		return nil, err
	}
	values := make([]filtercontract.SymbolWeight, 0, len(view.Weights))
	found := false
	for _, value := range view.Weights {
		if value.Symbol == symbol {
			found = true
			continue
		}
		values = append(values, value)
	}
	if !found {
		return nil, gorm.ErrRecordNotFound
	}
	view.Weights = values
	return s.replaceAdView(view, actor, requestID, "delete_weight")
}

func (s *FilterPolicyService) ActiveBundle(policyKind string) (any, error) {
	active, err := s.store.GetFilterActiveState(policyKind)
	if err != nil {
		return nil, err
	}
	switch policyKind {
	case filtercontract.PolicyManual:
		value, err := s.store.GetManualFilterRevision(active.ActiveRevision)
		if err != nil {
			return nil, err
		}
		bundle, err := manualBundle(value, false)
		if err != nil || bundle.Checksum != active.Checksum {
			if err == nil {
				err = store.ErrFilterPolicyConflict
			}
			return nil, err
		}
		return bundle, nil
	case filtercontract.PolicyAd:
		value, err := s.store.GetAdFilterRevision(active.ActiveRevision)
		if err != nil {
			return nil, err
		}
		bundle, err := adBundle(value, false)
		if err != nil || bundle.Checksum != active.Checksum {
			if err == nil {
				err = store.ErrFilterPolicyConflict
			}
			return nil, err
		}
		return bundle, nil
	default:
		return nil, store.ErrInvalidFilterPolicyRevision
	}
}

func (s *FilterPolicyService) Status() (*FilterPolicyStatus, error) {
	active, err := s.store.ListFilterActiveStates()
	if err != nil {
		return nil, err
	}
	nodes, err := s.store.ListFilterNodeStates("")
	if err != nil {
		return nil, err
	}
	return &FilterPolicyStatus{ActiveStates: active, NodeStates: nodes}, nil
}

func (s *FilterPolicyService) ReportNodeState(state *model.FilterNodeState) error {
	if state == nil {
		return store.ErrInvalidFilterNodeState
	}
	if _, err := s.store.GetServer(state.NodeID); err != nil {
		return err
	}
	active, err := s.store.GetFilterActiveState(state.PolicyKind)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		state.DesiredRevision = 0
	} else {
		state.DesiredRevision = active.ActiveRevision
		if state.AppliedRevision == active.ActiveRevision && state.Checksum != active.Checksum && state.LastError == "" {
			return store.ErrInvalidFilterNodeState
		}
	}
	return s.store.UpsertFilterNodeState(state)
}

func (s *FilterPolicyService) RecordDecision(event filtercontract.OutboxEvent) (*model.FilterDecision, error) {
	if err := event.Validate(); err != nil {
		return nil, err
	}
	if event.Phase != "ready" || event.Result == nil {
		return nil, store.ErrInvalidFilterDecision
	}
	mailbox, err := s.store.GetMailboxByEmail(event.Mailbox)
	if err != nil {
		return nil, err
	}
	if mailbox.ServerID != event.NodeID {
		return nil, ErrFilterDecisionNode
	}
	decision, err := decisionModel(event, mailbox.ID)
	if err != nil {
		return nil, err
	}
	var quarantine *model.FilterQuarantine
	if event.Result.QuarantineKey != "" {
		retentionDays := s.store.GetConfigInt("general.default_retention_days", 30)
		if retentionDays <= 0 {
			retentionDays = 30
		}
		quarantine, err = quarantineModel(event, retentionDays)
		if err != nil {
			return nil, err
		}
	}
	if err := s.store.SaveFilterDecisionAndQuarantine(decision, quarantine); err != nil {
		return nil, err
	}
	return decision, nil
}

func quarantineModel(event filtercontract.OutboxEvent, retentionDays int) (*model.FilterQuarantine, error) {
	if event.Result == nil || event.Result.Status != "succeeded" || event.Result.ActualAction != filtercontract.ActionQuarantine ||
		strings.TrimSpace(event.Result.QuarantineKey) == "" || retentionDays <= 0 {
		return nil, store.ErrInvalidFilterQuarantine
	}
	return &model.FilterQuarantine{
		Status: "quarantined", QuarantineKey: event.Result.QuarantineKey,
		OriginalMaildirKey: event.Result.OriginalMaildirKey,
		ExpiresAt:          event.Decision.EvaluatedAt.Add(time.Duration(retentionDays) * 24 * time.Hour),
	}, nil
}

func (s *FilterPolicyService) ListQuarantines(page, size int, status string) (*FilterQuarantinePage, error) {
	records, total, err := s.store.ListFilterQuarantines(page, size, status)
	if err != nil {
		return nil, err
	}
	items := make([]FilterQuarantineView, 0, len(records))
	for _, record := range records {
		view, err := quarantineView(record)
		if err != nil {
			return nil, err
		}
		items = append(items, *view)
	}
	return &FilterQuarantinePage{Items: items, Total: total, Page: page, Size: size}, nil
}

func (s *FilterPolicyService) GetQuarantine(key string) (*FilterQuarantineView, error) {
	record, err := s.store.GetFilterQuarantine(key)
	if err != nil {
		return nil, err
	}
	return quarantineView(*record)
}

func quarantineView(record store.FilterQuarantineRecord) (*FilterQuarantineView, error) {
	view := &FilterQuarantineView{FilterQuarantineRecord: record}
	if record.ReleaseReceiptText != "" {
		var receipt filtercontract.ReleaseReceipt
		if err := filtercontract.DecodeStrict([]byte(record.ReleaseReceiptText), &receipt); err != nil {
			return nil, err
		}
		view.ReleaseReceipt = &receipt
	}
	return view, nil
}

func (s *FilterPolicyService) BeginQuarantineRelease(key, operationID, actor string) (*FilterQuarantineView, error) {
	record, err := s.store.BeginFilterQuarantineRelease(key, operationID, actor)
	if err != nil {
		return nil, err
	}
	return quarantineView(*record)
}

func (s *FilterPolicyService) CompleteQuarantineRelease(key, operationID, feedbackLabel, actor string, receipt filtercontract.ReleaseReceipt, callErr error) error {
	if receipt.OperationID != operationID || receipt.QuarantineKey != key {
		return store.ErrInvalidFilterQuarantine
	}
	if err := receipt.Validate(); err != nil {
		return err
	}
	data, err := receipt.CanonicalJSON()
	if err != nil {
		return err
	}
	status := "release_failed"
	lastError := receipt.ErrorSummary
	if callErr == nil && receipt.Status == filtercontract.ReleaseStatusReleased && receipt.SMTPDelivered && receipt.RestoredToCur {
		status = "released"
		lastError = ""
	}
	if err := s.store.CompleteFilterQuarantineRelease(key, operationID, status, string(data), lastError, feedbackLabel, actor); err != nil {
		return err
	}
	changes, _ := json.Marshal(map[string]any{"operation_id": operationID, "status": status, "receipt": receipt})
	return s.store.CreateFilterAudit(&model.FilterAudit{
		PolicyKind: "quarantine", Action: "release", EntityType: "filter_quarantine", EntityID: key,
		Actor: actor, ChangesText: string(changes), RequestID: operationID,
	})
}

func (s *FilterPolicyService) ConfirmQuarantineAd(key, note, actor, requestID string) error {
	if err := s.store.ReviewFilterQuarantine(key, "confirmed_ad", "confirmed_ad", note, actor); err != nil {
		return err
	}
	changes, _ := json.Marshal(map[string]string{"feedback_label": "confirmed_ad", "note": note})
	return s.store.CreateFilterAudit(&model.FilterAudit{
		PolicyKind: "quarantine", Action: "confirm_ad", EntityType: "filter_quarantine", EntityID: key,
		Actor: actor, ChangesText: string(changes), RequestID: requestID,
	})
}

func (s *FilterPolicyService) CreateQuarantineAllowDraft(key, sender, scope, actor, requestID string) (*ManualFilterRevisionView, error) {
	address, err := mail.ParseAddress(strings.TrimSpace(sender))
	if err != nil || strings.TrimSpace(address.Address) == "" {
		return nil, &filtercontract.ContractError{Code: filtercontract.ErrorInvalidValue, Path: "sender", Message: "quarantined message sender is invalid"}
	}
	normalized := strings.ToLower(strings.TrimSpace(address.Address))
	field := "header_from.address"
	allowValue := normalized
	if scope == "domain" {
		parts := strings.Split(normalized, "@")
		if len(parts) != 2 || parts[1] == "" {
			return nil, &filtercontract.ContractError{Code: filtercontract.ErrorInvalidValue, Path: "sender", Message: "sender domain is invalid"}
		}
		field, allowValue = "header_from.domain", parts[1]
	} else if scope != "" && scope != "email" {
		return nil, &filtercontract.ContractError{Code: filtercontract.ErrorInvalidEnum, Path: "scope", Message: "scope must be email or domain"}
	}
	var baseRevision *uint64
	active, err := s.store.GetFilterActiveState(filtercontract.PolicyManual)
	if err == nil {
		baseRevision = &active.ActiveRevision
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	draft, err := s.CreateManualDraft(baseRevision, actor, requestID)
	if err != nil {
		return nil, err
	}
	logicalSuffix := key
	if len(logicalSuffix) > 12 {
		logicalSuffix = logicalSuffix[:12]
	}
	draft, err = s.AddManualRule(draft.Revision, filtercontract.ManualRule{
		LogicalID: "quarantine-allow-" + logicalSuffix, Name: "Allow " + allowValue,
		ScopeType: "global", Action: filtercontract.ActionAllow, Priority: 1,
		Mode: filtercontract.ModeShadow, Source: "manual",
		Conditions: []filtercontract.Condition{{
			Field: field, Operator: "eq", Value: filtercontract.StringValue(allowValue), Position: 0,
		}},
	}, actor, requestID)
	if err != nil {
		return nil, err
	}
	changes, _ := json.Marshal(map[string]any{"manual_revision": draft.Revision, "scope": scope, "value": allowValue, "mode": "shadow"})
	if err := s.store.CreateFilterAudit(&model.FilterAudit{
		PolicyKind: "quarantine", Action: "create_allow_draft", EntityType: "filter_quarantine", EntityID: key,
		Actor: actor, ChangesText: string(changes), RequestID: requestID,
	}); err != nil {
		return nil, err
	}
	return draft, nil
}

func (s *FilterPolicyService) MarkQuarantinesExpired(nodeID uint64, keys []string) error {
	return s.store.MarkFilterQuarantinesExpired(nodeID, keys)
}

func (s *FilterPolicyService) ListDecisions(page, size int, action string) (*FilterDecisionPage, error) {
	records, total, err := s.store.ListFilterDecisions(page, size, action)
	if err != nil {
		return nil, err
	}
	items := make([]FilterDecisionView, 0, len(records))
	for _, record := range records {
		view, err := decisionView(record)
		if err != nil {
			return nil, err
		}
		items = append(items, *view)
	}
	return &FilterDecisionPage{Items: items, Total: total, Page: page, Size: size}, nil
}

func (s *FilterPolicyService) GetDecision(decisionKey string) (*FilterDecisionView, error) {
	record, err := s.store.GetFilterDecision(decisionKey)
	if err != nil {
		return nil, err
	}
	return decisionView(*record)
}

func decisionView(record store.FilterDecisionRecord) (*FilterDecisionView, error) {
	view := &FilterDecisionView{
		ID: record.ID, DecisionKey: record.DecisionKey, MessageKey: record.MessageKey, MessageID: record.MessageID,
		Mailbox: record.Mailbox, NodeID: record.NodeID, ManualRevision: record.ManualRevision, AdRevision: record.AdRevision,
		ManualAction: record.ManualAction, AdAction: record.AdAction, FinalAction: record.FinalAction,
		AdScore: filtercontract.Score(record.AdScoreMilli), EvaluatedAt: record.EvaluatedAt,
		Reasons: []filtercontract.DecisionReason{}, AdSymbols: []filtercontract.AdSymbolResult{},
		ShadowResults: []filtercontract.ShadowResult{}, ParseWarnings: []string{},
	}
	for _, item := range []struct {
		payload string
		target  any
	}{
		{record.ReasonsText, &view.Reasons},
		{record.AdSymbolsText, &view.AdSymbols},
		{record.ShadowResultsText, &view.ShadowResults},
		{record.ParseWarningsText, &view.ParseWarnings},
	} {
		if err := json.Unmarshal([]byte(item.payload), item.target); err != nil {
			return nil, fmt.Errorf("decode filter decision evidence: %w", err)
		}
	}
	return view, nil
}

func decisionModel(event filtercontract.OutboxEvent, mailboxAccountID uint64) (*model.FilterDecision, error) {
	marshal := func(value any) (string, error) {
		data, err := json.Marshal(value)
		return string(data), err
	}
	reasons, err := marshal(event.Decision.Reasons)
	if err != nil {
		return nil, err
	}
	symbols, err := marshal(event.Decision.AdSymbols)
	if err != nil {
		return nil, err
	}
	shadow, err := marshal(event.Decision.ShadowResults)
	if err != nil {
		return nil, err
	}
	warnings, err := marshal(event.Decision.ParseWarnings)
	if err != nil {
		return nil, err
	}
	return &model.FilterDecision{
		SchemaVersion: event.Decision.SchemaVersion, DecisionKey: event.Decision.DecisionKey,
		MessageKey: event.Decision.MessageKey, MessageID: event.MessageID, MailboxAccountID: mailboxAccountID, NodeID: event.NodeID,
		ManualRevision: event.Decision.ManualRevision, AdRevision: event.Decision.AdRevision,
		ManualAction: event.Decision.ManualAction, AdAction: event.Decision.AdAction, FinalAction: event.Decision.FinalAction,
		AdScoreMilli: int64(event.Decision.AdScore), ReasonsText: reasons, AdSymbolsText: symbols,
		ShadowResultsText: shadow, ParseWarningsText: warnings, EvaluatedAt: event.Decision.EvaluatedAt,
	}, nil
}

func (s *FilterPolicyService) putAdDetectors(view *AdFilterRevisionView, detectors []filtercontract.AdDetector, actor, requestID, action string) (*AdFilterRevisionView, error) {
	values := make([]filtercontract.AdDetector, len(detectors))
	for i := range detectors {
		value, err := normalizeAdDetector(detectors[i])
		if err != nil {
			return nil, err
		}
		values[i] = value
	}
	view.Detectors = values
	return s.replaceAdView(view, actor, requestID, action)
}

func (s *FilterPolicyService) putAdComposites(view *AdFilterRevisionView, composites []filtercontract.AdComposite, actor, requestID, action string) (*AdFilterRevisionView, error) {
	values := make([]filtercontract.AdComposite, len(composites))
	for i := range composites {
		value := normalizeAdComposite(composites[i])
		if err := filtercontract.ValidateAdComposite(value); err != nil {
			return nil, err
		}
		values[i] = value
	}
	view.Composites = values
	return s.replaceAdView(view, actor, requestID, action)
}

func (s *FilterPolicyService) replaceAdView(view *AdFilterRevisionView, actor, requestID, action string) (*AdFilterRevisionView, error) {
	if view.Status != "draft" {
		return nil, ErrFilterPolicyDraftRequired
	}
	if err := validateAdDraftShape(view); err != nil {
		return nil, err
	}
	detectors := make([]model.AdFilterDetector, len(view.Detectors))
	for i := range view.Detectors {
		var err error
		detectors[i], err = adDetectorModel(view.Detectors[i])
		if err != nil {
			return nil, err
		}
	}
	composites := make([]model.AdFilterComposite, len(view.Composites))
	for i := range view.Composites {
		composites[i] = adCompositeModel(view.Composites[i])
	}
	weights := make([]model.AdFilterSymbolWeight, len(view.Weights))
	for i := range view.Weights {
		weights[i] = model.AdFilterSymbolWeight{Symbol: view.Weights[i].Symbol, ScoreMilli: int64(view.Weights[i].Score)}
	}
	if err := s.store.ReplaceAdFilterDraft(view.Revision, int64(view.TagThreshold), int64(view.QuarantineThreshold), detectors, composites, weights, actor, requestID, action); err != nil {
		return nil, err
	}
	return s.GetAdRevision(view.Revision)
}

func validateAdDraftShape(view *AdFilterRevisionView) error {
	logicalIDs := make(map[string]struct{}, len(view.Detectors)+len(view.Composites))
	symbols := make(map[string]struct{}, len(view.Detectors)+len(view.Composites))
	for _, detector := range view.Detectors {
		if err := filtercontract.ValidateAdDetector(detector); err != nil {
			return err
		}
		if _, exists := logicalIDs[detector.LogicalID]; exists {
			return &filtercontract.ContractError{Code: filtercontract.ErrorInvalidValue, Path: "detectors", Message: "logical_id must be unique across producers"}
		}
		if _, exists := symbols[detector.Symbol]; exists {
			return &filtercontract.ContractError{Code: filtercontract.ErrorInvalidValue, Path: "detectors", Message: "symbol must be unique across producers"}
		}
		logicalIDs[detector.LogicalID], symbols[detector.Symbol] = struct{}{}, struct{}{}
	}
	for _, composite := range view.Composites {
		if err := filtercontract.ValidateAdComposite(composite); err != nil {
			return err
		}
		if _, exists := logicalIDs[composite.LogicalID]; exists {
			return &filtercontract.ContractError{Code: filtercontract.ErrorInvalidValue, Path: "composites", Message: "logical_id must be unique across producers"}
		}
		if _, exists := symbols[composite.Symbol]; exists {
			return &filtercontract.ContractError{Code: filtercontract.ErrorInvalidValue, Path: "composites", Message: "symbol must be unique across producers"}
		}
		logicalIDs[composite.LogicalID], symbols[composite.Symbol] = struct{}{}, struct{}{}
	}
	weights := make(map[string]struct{}, len(view.Weights))
	for _, weight := range view.Weights {
		if err := filtercontract.ValidateSymbolWeight(weight); err != nil {
			return err
		}
		if _, exists := weights[weight.Symbol]; exists {
			return &filtercontract.ContractError{Code: filtercontract.ErrorInvalidValue, Path: "weights", Message: "symbol must be unique"}
		}
		weights[weight.Symbol] = struct{}{}
	}
	return nil
}

func manualView(value *model.ManualFilterRevision) (*ManualFilterRevisionView, error) {
	bundle, err := manualBundle(value, false)
	if err != nil && value.Status != "draft" {
		return nil, err
	}
	if err != nil {
		bundle = filtercontract.ManualBundle{Rules: []filtercontract.ManualRule{}}
		for _, rule := range value.Rules {
			converted, conversionErr := manualRuleContract(rule)
			if conversionErr != nil {
				return nil, conversionErr
			}
			bundle.Rules = append(bundle.Rules, converted)
		}
	}
	return &ManualFilterRevisionView{
		Revision: value.Revision, Status: value.Status, BaseRevision: value.BaseRevision, SchemaVersion: value.SchemaVersion,
		Checksum: value.Checksum, CreatedBy: value.CreatedBy, PublishedBy: value.PublishedBy,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, PublishedAt: value.PublishedAt, Rules: bundle.Rules,
	}, nil
}

func adView(value *model.AdFilterRevision) (*AdFilterRevisionView, error) {
	bundle, err := adBundle(value, false)
	if err != nil && value.Status != "draft" {
		return nil, err
	}
	if err != nil {
		bundle, err = adBundle(value, true)
		if err != nil {
			// Drafts may be intentionally incomplete; conversion errors are still fatal.
			bundle, err = adBundleUnchecked(value)
			if err != nil {
				return nil, err
			}
		}
	}
	return &AdFilterRevisionView{
		Revision: value.Revision, Status: value.Status, BaseRevision: value.BaseRevision, SchemaVersion: value.SchemaVersion,
		Checksum: value.Checksum, TagThreshold: filtercontract.Score(value.TagThresholdMilli), QuarantineThreshold: filtercontract.Score(value.QuarantineThresholdMilli),
		CreatedBy: value.CreatedBy, PublishedBy: value.PublishedBy, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
		PublishedAt: value.PublishedAt, Detectors: bundle.Detectors, Composites: bundle.Composites, Weights: bundle.Weights,
	}, nil
}

func manualBundle(value *model.ManualFilterRevision, calculate bool) (filtercontract.ManualBundle, error) {
	bundle := filtercontract.ManualBundle{SchemaVersion: value.SchemaVersion, PolicyKind: filtercontract.PolicyManual, Revision: value.Revision, Checksum: value.Checksum, Rules: []filtercontract.ManualRule{}}
	for _, rule := range value.Rules {
		converted, err := manualRuleContract(rule)
		if err != nil {
			return bundle, err
		}
		bundle.Rules = append(bundle.Rules, converted)
	}
	if calculate {
		checksum, err := bundle.CalculatedChecksum()
		if err != nil {
			return bundle, err
		}
		bundle.Checksum = checksum
	}
	if err := bundle.Validate(); err != nil {
		return bundle, err
	}
	return bundle, nil
}

func adBundle(value *model.AdFilterRevision, calculate bool) (filtercontract.AdBundle, error) {
	bundle, err := adBundleUnchecked(value)
	if err != nil {
		return bundle, err
	}
	bundle.Checksum = value.Checksum
	if calculate {
		checksum, err := bundle.CalculatedChecksum()
		if err != nil {
			return bundle, err
		}
		bundle.Checksum = checksum
	}
	if err := bundle.Validate(); err != nil {
		return bundle, err
	}
	return bundle, nil
}

func adBundleUnchecked(value *model.AdFilterRevision) (filtercontract.AdBundle, error) {
	bundle := filtercontract.AdBundle{
		SchemaVersion: value.SchemaVersion, PolicyKind: filtercontract.PolicyAd, Revision: value.Revision,
		TagThreshold: filtercontract.Score(value.TagThresholdMilli), QuarantineThreshold: filtercontract.Score(value.QuarantineThresholdMilli),
		Detectors: []filtercontract.AdDetector{}, Composites: []filtercontract.AdComposite{}, Weights: []filtercontract.SymbolWeight{},
	}
	for _, detector := range value.Detectors {
		converted, err := adDetectorContract(detector)
		if err != nil {
			return bundle, err
		}
		bundle.Detectors = append(bundle.Detectors, converted)
	}
	for _, composite := range value.Composites {
		bundle.Composites = append(bundle.Composites, adCompositeContract(composite))
	}
	for _, weight := range value.Weights {
		bundle.Weights = append(bundle.Weights, filtercontract.SymbolWeight{Symbol: weight.Symbol, Score: filtercontract.Score(weight.ScoreMilli)})
	}
	return bundle, nil
}

func manualRuleContract(value model.ManualFilterRule) (filtercontract.ManualRule, error) {
	result := filtercontract.ManualRule{LogicalID: value.LogicalID, Name: value.Name, ScopeType: value.ScopeType, ScopeID: value.ScopeID, Action: value.Action, Priority: value.Priority, Mode: value.Mode, Source: value.Source, Conditions: []filtercontract.Condition{}}
	for _, condition := range value.Conditions {
		converted, err := manualConditionContract(condition)
		if err != nil {
			return result, err
		}
		result.Conditions = append(result.Conditions, converted)
	}
	return result, nil
}

func adDetectorContract(value model.AdFilterDetector) (filtercontract.AdDetector, error) {
	result := filtercontract.AdDetector{LogicalID: value.LogicalID, Name: value.Name, Symbol: value.Symbol, Mode: value.Mode, Source: value.Source, SourceReference: value.SourceReference, Conditions: []filtercontract.Condition{}}
	for _, condition := range value.Conditions {
		converted, err := adConditionContract(condition)
		if err != nil {
			return result, err
		}
		result.Conditions = append(result.Conditions, converted)
	}
	return result, nil
}

func conditionValue(text string) (filtercontract.ConditionValue, error) {
	var value filtercontract.ConditionValue
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return value, fmt.Errorf("invalid stored condition value: %w", err)
	}
	return value, nil
}

func manualConditionContract(value model.ManualFilterCondition) (filtercontract.Condition, error) {
	parsed, err := conditionValue(value.ValueText)
	return filtercontract.Condition{Field: value.Field, Operator: value.Operator, Value: parsed, Negated: value.Negated, Position: value.Position}, err
}

func adConditionContract(value model.AdFilterCondition) (filtercontract.Condition, error) {
	parsed, err := conditionValue(value.ValueText)
	return filtercontract.Condition{Field: value.Field, Operator: value.Operator, Value: parsed, Negated: value.Negated, Position: value.Position}, err
}

func manualRuleModel(value filtercontract.ManualRule) (model.ManualFilterRule, error) {
	result := model.ManualFilterRule{LogicalID: value.LogicalID, Name: value.Name, ScopeType: value.ScopeType, ScopeID: value.ScopeID, Action: value.Action, Priority: value.Priority, Mode: value.Mode, Source: value.Source}
	for _, condition := range value.Conditions {
		payload, err := json.Marshal(condition.Value)
		if err != nil {
			return result, err
		}
		result.Conditions = append(result.Conditions, model.ManualFilterCondition{Field: condition.Field, Operator: condition.Operator, ValueText: string(payload), Negated: condition.Negated, Position: condition.Position})
	}
	return result, nil
}

func adDetectorModel(value filtercontract.AdDetector) (model.AdFilterDetector, error) {
	result := model.AdFilterDetector{LogicalID: value.LogicalID, Name: value.Name, Symbol: value.Symbol, Mode: value.Mode, Source: value.Source, SourceReference: value.SourceReference}
	for _, condition := range value.Conditions {
		payload, err := json.Marshal(condition.Value)
		if err != nil {
			return result, err
		}
		result.Conditions = append(result.Conditions, model.AdFilterCondition{Field: condition.Field, Operator: condition.Operator, ValueText: string(payload), Negated: condition.Negated, Position: condition.Position})
	}
	return result, nil
}

func adCompositeContract(value model.AdFilterComposite) filtercontract.AdComposite {
	result := filtercontract.AdComposite{LogicalID: value.LogicalID, Name: value.Name, Symbol: value.Symbol, Mode: value.Mode, ScorePolicy: value.ScorePolicy, AllOf: []string{}, AnyOf: []string{}, NoneOf: []string{}}
	for _, term := range value.Terms {
		switch term.GroupKind {
		case "all_of":
			result.AllOf = append(result.AllOf, term.InputSymbol)
		case "any_of":
			result.AnyOf = append(result.AnyOf, term.InputSymbol)
		case "none_of":
			result.NoneOf = append(result.NoneOf, term.InputSymbol)
		}
	}
	return result
}

func adCompositeModel(value filtercontract.AdComposite) model.AdFilterComposite {
	result := model.AdFilterComposite{LogicalID: value.LogicalID, Name: value.Name, Symbol: value.Symbol, Mode: value.Mode, ScorePolicy: value.ScorePolicy}
	for _, group := range []struct {
		name   string
		values []string
	}{{"all_of", value.AllOf}, {"any_of", value.AnyOf}, {"none_of", value.NoneOf}} {
		for position, symbol := range group.values {
			result.Terms = append(result.Terms, model.AdFilterCompositeTerm{GroupKind: group.name, InputSymbol: symbol, Position: position})
		}
	}
	return result
}

func adModelFromBundle(bundle filtercontract.AdBundle) *model.AdFilterRevision {
	result := &model.AdFilterRevision{TagThresholdMilli: int64(bundle.TagThreshold), QuarantineThresholdMilli: int64(bundle.QuarantineThreshold)}
	for _, value := range bundle.Detectors {
		converted, _ := adDetectorModel(value)
		result.Detectors = append(result.Detectors, converted)
	}
	for _, value := range bundle.Composites {
		result.Composites = append(result.Composites, adCompositeModel(value))
	}
	for _, value := range bundle.Weights {
		result.Weights = append(result.Weights, model.AdFilterSymbolWeight{Symbol: value.Symbol, ScoreMilli: int64(value.Score)})
	}
	return result
}

func normalizeManualRule(value filtercontract.ManualRule) (filtercontract.ManualRule, error) {
	value.LogicalID = defaultLogicalID(value.LogicalID)
	value.Name = strings.TrimSpace(value.Name)
	if value.ScopeType == "" {
		value.ScopeType = "global"
	}
	if value.Mode == "" {
		value.Mode = filtercontract.ModeShadow
	}
	if value.Source == "" {
		value.Source = "manual"
	}
	var err error
	value.Conditions, err = normalizeConditions(filtercontract.PolicyManual, value.Conditions)
	if err != nil {
		return value, err
	}
	return value, filtercontract.ValidateManualRule(value)
}

func normalizeAdDetector(value filtercontract.AdDetector) (filtercontract.AdDetector, error) {
	value.LogicalID = defaultLogicalID(value.LogicalID)
	value.Name = strings.TrimSpace(value.Name)
	value.Symbol = strings.ToUpper(strings.TrimSpace(value.Symbol))
	if value.Mode == "" {
		value.Mode = filtercontract.ModeShadow
	}
	if value.Source == "" {
		value.Source = "local"
	}
	var err error
	value.Conditions, err = normalizeConditions(filtercontract.PolicyAd, value.Conditions)
	if err != nil {
		return value, err
	}
	return value, filtercontract.ValidateAdDetector(value)
}

func normalizeAdComposite(value filtercontract.AdComposite) filtercontract.AdComposite {
	value.LogicalID = defaultLogicalID(value.LogicalID)
	value.Name = strings.TrimSpace(value.Name)
	value.Symbol = strings.ToUpper(strings.TrimSpace(value.Symbol))
	if value.Mode == "" {
		value.Mode = filtercontract.ModeShadow
	}
	if value.ScorePolicy == "" {
		value.ScorePolicy = "keep_inputs"
	}
	for _, values := range [][]string{value.AllOf, value.AnyOf, value.NoneOf} {
		for i := range values {
			values[i] = strings.ToUpper(strings.TrimSpace(values[i]))
		}
		sort.Strings(values)
	}
	return value
}

func normalizeConditions(policyKind string, values []filtercontract.Condition) ([]filtercontract.Condition, error) {
	result := append([]filtercontract.Condition(nil), values...)
	for i := range result {
		result[i].Field = strings.TrimSpace(result[i].Field)
		result[i].Operator = strings.TrimSpace(result[i].Operator)
		result[i].Position = i
		if text, ok := result[i].Value.String(); ok {
			normalized, err := normalizeConditionString(result[i].Field, text)
			if err != nil {
				return nil, &filtercontract.ContractError{Code: filtercontract.ErrorInvalidValue, Path: fmt.Sprintf("conditions[%d].value", i), Message: err.Error()}
			}
			result[i].Value = filtercontract.StringValue(normalized)
		}
		if err := filtercontract.ValidateCondition(policyKind, result[i]); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func normalizeConditionString(field, value string) (string, error) {
	switch field {
	case "header_from.domain", "envelope_from.domain", "reply_to.domain":
		return mailboxaddr.NormalizeDomain(value)
	case "header_from.address", "envelope_from.address", "mailbox.address":
		parsed, err := mail.ParseAddress(strings.TrimSpace(value))
		if err != nil || parsed.Address != strings.TrimSpace(value) {
			return "", fmt.Errorf("invalid canonical email address")
		}
		at := strings.LastIndexByte(parsed.Address, '@')
		if at <= 0 {
			return "", fmt.Errorf("invalid canonical email address")
		}
		domain, err := mailboxaddr.NormalizeDomain(parsed.Address[at+1:])
		if err != nil {
			return "", err
		}
		return parsed.Address[:at] + "@" + domain, nil
	case "headers", "precedence":
		return strings.ToLower(strings.TrimSpace(value)), nil
	default:
		return value, nil
	}
}

func validationResult(bundle any, err error) FilterPolicyValidation {
	if err != nil {
		return invalidPolicyValidation(err)
	}
	data, marshalErr := filtercontract.MarshalCanonical(bundle)
	if marshalErr != nil {
		return invalidPolicyValidation(marshalErr)
	}
	checksum := ""
	switch value := bundle.(type) {
	case filtercontract.ManualBundle:
		checksum = value.Checksum
	case filtercontract.AdBundle:
		checksum = value.Checksum
	}
	return FilterPolicyValidation{Valid: true, Checksum: checksum, BundleBytes: len(data)}
}

func invalidPolicyValidation(err error) FilterPolicyValidation {
	return FilterPolicyValidation{Valid: false, Error: err.Error()}
}

func defaultLogicalID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return uuid.NewString()
	}
	return value
}

func cloneUint64Pointer(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
