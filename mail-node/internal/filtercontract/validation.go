package filtercontract

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
)

const (
	ErrorInvalidJSON          = "contract_invalid_json"
	ErrorInvalidSchemaVersion = "contract_invalid_schema_version"
	ErrorRequired             = "contract_required"
	ErrorInvalidEnum          = "contract_invalid_enum"
	ErrorInvalidValue         = "contract_invalid_value"
	ErrorChecksumMismatch     = "contract_checksum_mismatch"
)

var symbolPattern = regexp.MustCompile(`^AD_[A-Z0-9_]{2,60}$`)

type ContractError struct {
	Code    string
	Path    string
	Message string
}

func (e *ContractError) Error() string {
	if e.Path == "" {
		return e.Code + ": " + e.Message
	}
	return e.Code + " at " + e.Path + ": " + e.Message
}

func DecodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return &ContractError{Code: ErrorInvalidJSON, Message: err.Error()}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return &ContractError{Code: ErrorInvalidJSON, Message: err.Error()}
	}
	return nil
}

func (b ManualBundle) Validate() error {
	if err := validateHeader(b.SchemaVersion, b.PolicyKind, PolicyManual, b.Revision); err != nil {
		return err
	}
	if err := validateChecksum(b.Checksum); err != nil {
		return err
	}
	calculated, err := b.CalculatedChecksum()
	if err != nil {
		return err
	}
	if b.Checksum != calculated {
		return &ContractError{Code: ErrorChecksumMismatch, Path: "checksum", Message: "checksum does not match canonical bundle payload"}
	}
	seen := make(map[string]struct{}, len(b.Rules))
	for i, rule := range b.Rules {
		path := fmt.Sprintf("rules[%d]", i)
		if rule.LogicalID == "" || rule.Name == "" {
			return &ContractError{Code: ErrorRequired, Path: path, Message: "logical_id and name are required"}
		}
		if _, exists := seen[rule.LogicalID]; exists {
			return &ContractError{Code: ErrorInvalidValue, Path: path + ".logical_id", Message: "logical_id must be unique"}
		}
		seen[rule.LogicalID] = struct{}{}
		if rule.ScopeType != "global" || rule.ScopeID != nil {
			return &ContractError{Code: ErrorInvalidValue, Path: path + ".scope_type", Message: "v1 only supports global scope with null scope_id"}
		}
		if !oneOf(rule.Action, ActionAllow, ActionTag, ActionQuarantine) {
			return invalidEnum(path+".action", rule.Action)
		}
		if !oneOf(rule.Mode, ModeShadow, ModeEnforce, ModeDisabled) {
			return invalidEnum(path+".mode", rule.Mode)
		}
		if len(rule.Conditions) == 0 || len(rule.Conditions) > 20 {
			return &ContractError{Code: ErrorInvalidValue, Path: path + ".conditions", Message: "condition count must be between 1 and 20"}
		}
	}
	return nil
}

func (b AdBundle) Validate() error {
	if err := validateHeader(b.SchemaVersion, b.PolicyKind, PolicyAd, b.Revision); err != nil {
		return err
	}
	if err := validateChecksum(b.Checksum); err != nil {
		return err
	}
	calculated, err := b.CalculatedChecksum()
	if err != nil {
		return err
	}
	if b.Checksum != calculated {
		return &ContractError{Code: ErrorChecksumMismatch, Path: "checksum", Message: "checksum does not match canonical bundle payload"}
	}
	if b.TagThreshold <= 0 || b.TagThreshold >= b.QuarantineThreshold || b.QuarantineThreshold > Score(10000*1000) {
		return &ContractError{Code: ErrorInvalidValue, Path: "tag_threshold", Message: "thresholds must satisfy 0 < tag < quarantine <= 10000"}
	}
	producers := make(map[string]struct{}, len(b.Detectors)+len(b.Composites))
	for i, detector := range b.Detectors {
		path := fmt.Sprintf("detectors[%d]", i)
		if err := validateProducer(path, detector.LogicalID, detector.Name, detector.Symbol, detector.Mode); err != nil {
			return err
		}
		if _, exists := producers[detector.Symbol]; exists {
			return &ContractError{Code: ErrorInvalidValue, Path: path + ".symbol", Message: "producer symbol must be unique"}
		}
		producers[detector.Symbol] = struct{}{}
		if len(detector.Conditions) == 0 || len(detector.Conditions) > 20 {
			return &ContractError{Code: ErrorInvalidValue, Path: path + ".conditions", Message: "condition count must be between 1 and 20"}
		}
	}
	for i, composite := range b.Composites {
		path := fmt.Sprintf("composites[%d]", i)
		if err := validateProducer(path, composite.LogicalID, composite.Name, composite.Symbol, composite.Mode); err != nil {
			return err
		}
		if _, exists := producers[composite.Symbol]; exists {
			return &ContractError{Code: ErrorInvalidValue, Path: path + ".symbol", Message: "producer symbol must be unique"}
		}
		producers[composite.Symbol] = struct{}{}
		if !oneOf(composite.ScorePolicy, "keep_inputs", "suppress_direct_inputs") {
			return invalidEnum(path+".score_policy", composite.ScorePolicy)
		}
		if len(composite.AllOf) == 0 && len(composite.AnyOf) == 0 {
			return &ContractError{Code: ErrorInvalidValue, Path: path, Message: "all_of or any_of must contain at least one symbol"}
		}
		if len(composite.AllOf)+len(composite.AnyOf)+len(composite.NoneOf) > 20 {
			return &ContractError{Code: ErrorInvalidValue, Path: path, Message: "composite direct input count must not exceed 20"}
		}
	}
	seenWeights := make(map[string]struct{}, len(b.Weights))
	for i, weight := range b.Weights {
		path := fmt.Sprintf("weights[%d]", i)
		if _, exists := producers[weight.Symbol]; !exists {
			return &ContractError{Code: ErrorInvalidValue, Path: path + ".symbol", Message: "weight references an unknown producer"}
		}
		if _, exists := seenWeights[weight.Symbol]; exists {
			return &ContractError{Code: ErrorInvalidValue, Path: path + ".symbol", Message: "symbol weight must be unique"}
		}
		seenWeights[weight.Symbol] = struct{}{}
		if weight.Score < -100*1000 || weight.Score > 100*1000 {
			return &ContractError{Code: ErrorInvalidValue, Path: path + ".score", Message: "weight must be in [-100, 100]"}
		}
	}
	if len(seenWeights) != len(producers) {
		return &ContractError{Code: ErrorInvalidValue, Path: "weights", Message: "every producer must have exactly one explicit weight"}
	}
	return nil
}

func (d FilterDecision) Validate() error {
	if d.SchemaVersion != SchemaVersionV1 {
		return &ContractError{Code: ErrorInvalidSchemaVersion, Path: "schema_version", Message: "only schema version 1 is supported"}
	}
	if d.DecisionKey == "" || d.MessageKey == "" {
		return &ContractError{Code: ErrorRequired, Path: "decision_key", Message: "decision_key and message_key are required"}
	}
	for path, action := range map[string]string{"manual_action": d.ManualAction, "ad_action": d.AdAction, "final_action": d.FinalAction} {
		if !oneOf(action, ActionAllow, ActionTag, ActionQuarantine) {
			return invalidEnum(path, action)
		}
	}
	return nil
}

func (e OutboxEvent) Validate() error {
	if e.SchemaVersion != SchemaVersionV1 {
		return &ContractError{Code: ErrorInvalidSchemaVersion, Path: "schema_version", Message: "only schema version 1 is supported"}
	}
	if err := e.Decision.Validate(); err != nil {
		return err
	}
	switch e.Phase {
	case "staged":
		if e.Result != nil {
			return &ContractError{Code: ErrorInvalidValue, Path: "result", Message: "staged events must have a null result"}
		}
	case "ready":
		if e.Result == nil {
			return &ContractError{Code: ErrorRequired, Path: "result", Message: "ready events require a processing result"}
		}
		if !oneOf(e.Result.Status, "succeeded", "failed") {
			return invalidEnum("result.status", e.Result.Status)
		}
	default:
		return invalidEnum("phase", e.Phase)
	}
	return nil
}

func (r ReleaseReceipt) Validate() error {
	if r.SchemaVersion != SchemaVersionV1 {
		return &ContractError{Code: ErrorInvalidSchemaVersion, Path: "schema_version", Message: "only schema version 1 is supported"}
	}
	if r.OperationID == "" || r.QuarantineKey == "" || r.DecisionKey == "" {
		return &ContractError{Code: ErrorRequired, Path: "operation_id", Message: "operation_id, quarantine_key, and decision_key are required"}
	}
	if !oneOf(r.Status, "in_progress", "completed", "failed") {
		return invalidEnum("status", r.Status)
	}
	if r.Status == "completed" && (!r.SMTPDelivered || !r.RestoredToCur) {
		return &ContractError{Code: ErrorInvalidValue, Path: "status", Message: "completed requires SMTP delivery and Maildir restore"}
	}
	return nil
}

func validateHeader(schemaVersion int, policyKind, expectedKind string, revision uint64) error {
	if schemaVersion != SchemaVersionV1 {
		return &ContractError{Code: ErrorInvalidSchemaVersion, Path: "schema_version", Message: "only schema version 1 is supported"}
	}
	if policyKind != expectedKind {
		return invalidEnum("policy_kind", policyKind)
	}
	if revision == 0 {
		return &ContractError{Code: ErrorInvalidValue, Path: "revision", Message: "revision must be positive"}
	}
	return nil
}

func validateChecksum(checksum string) error {
	if len(checksum) != sha256HexLength {
		return &ContractError{Code: ErrorInvalidValue, Path: "checksum", Message: "checksum must be a lowercase SHA-256 hex string"}
	}
	decoded, err := hex.DecodeString(checksum)
	if err != nil || hex.EncodeToString(decoded) != checksum {
		return &ContractError{Code: ErrorInvalidValue, Path: "checksum", Message: "checksum must be a lowercase SHA-256 hex string"}
	}
	return nil
}

const sha256HexLength = 64

func validateProducer(path, logicalID, name, symbol, mode string) error {
	if logicalID == "" || name == "" {
		return &ContractError{Code: ErrorRequired, Path: path, Message: "logical_id and name are required"}
	}
	if !symbolPattern.MatchString(symbol) {
		return &ContractError{Code: ErrorInvalidValue, Path: path + ".symbol", Message: "symbol must match ^AD_[A-Z0-9_]{2,60}$"}
	}
	if !oneOf(mode, ModeShadow, ModeEnforce, ModeDisabled) {
		return invalidEnum(path+".mode", mode)
	}
	return nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func invalidEnum(path, value string) error {
	return &ContractError{Code: ErrorInvalidEnum, Path: path, Message: fmt.Sprintf("unsupported value %q", value)}
}
