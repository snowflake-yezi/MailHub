package filtercontract

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	ErrorInvalidJSON          = "contract_invalid_json"
	ErrorInvalidSchemaVersion = "contract_invalid_schema_version"
	ErrorRequired             = "contract_required"
	ErrorInvalidEnum          = "contract_invalid_enum"
	ErrorInvalidValue         = "contract_invalid_value"
	ErrorChecksumMismatch     = "contract_checksum_mismatch"
)

const (
	maxBundleBytes    = 4 << 20
	maxConditionBytes = 2 << 10
	maxManualRules    = 500
	maxAdDetectors    = 500
	maxAdComposites   = 200
	maxCompositeDepth = 5
	maxDirectInputs   = 20
)

var (
	symbolPattern     = regexp.MustCompile(`^AD_[A-Z0-9_]{2,60}$`)
	logicalIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
	headerNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
)

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
	if len(b.Rules) > maxManualRules {
		return &ContractError{Code: ErrorInvalidValue, Path: "rules", Message: "manual rule count must not exceed 500"}
	}
	if err := validateBundleSize(b); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(b.Rules))
	for i, rule := range b.Rules {
		path := fmt.Sprintf("rules[%d]", i)
		if err := validateManualRule(path, rule); err != nil {
			return err
		}
		if _, exists := seen[rule.LogicalID]; exists {
			return &ContractError{Code: ErrorInvalidValue, Path: path + ".logical_id", Message: "logical_id must be unique"}
		}
		seen[rule.LogicalID] = struct{}{}
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
	if len(b.Detectors) > maxAdDetectors {
		return &ContractError{Code: ErrorInvalidValue, Path: "detectors", Message: "detector count must not exceed 500"}
	}
	if len(b.Detectors) == 0 {
		return &ContractError{Code: ErrorRequired, Path: "detectors", Message: "an ad bundle must contain at least one detector"}
	}
	if len(b.Composites) > maxAdComposites {
		return &ContractError{Code: ErrorInvalidValue, Path: "composites", Message: "composite count must not exceed 200"}
	}
	if err := validateBundleSize(b); err != nil {
		return err
	}
	if b.TagThreshold <= 0 || b.TagThreshold >= b.QuarantineThreshold || b.QuarantineThreshold > Score(10000*1000) {
		return &ContractError{Code: ErrorInvalidValue, Path: "tag_threshold", Message: "thresholds must satisfy 0 < tag < quarantine <= 10000"}
	}
	producers := make(map[string]struct{}, len(b.Detectors)+len(b.Composites))
	for i, detector := range b.Detectors {
		path := fmt.Sprintf("detectors[%d]", i)
		if err := validateAdDetector(path, detector); err != nil {
			return err
		}
		if _, exists := producers[detector.Symbol]; exists {
			return &ContractError{Code: ErrorInvalidValue, Path: path + ".symbol", Message: "producer symbol must be unique"}
		}
		producers[detector.Symbol] = struct{}{}
	}
	for i, composite := range b.Composites {
		path := fmt.Sprintf("composites[%d]", i)
		if err := validateAdComposite(path, composite); err != nil {
			return err
		}
		if _, exists := producers[composite.Symbol]; exists {
			return &ContractError{Code: ErrorInvalidValue, Path: path + ".symbol", Message: "producer symbol must be unique"}
		}
		producers[composite.Symbol] = struct{}{}
	}
	if err := validateCompositeGraph(b.Composites, producers, producerModes(b)); err != nil {
		return err
	}
	seenWeights := make(map[string]struct{}, len(b.Weights))
	for i, weight := range b.Weights {
		path := fmt.Sprintf("weights[%d]", i)
		if err := validateSymbolWeight(path, weight); err != nil {
			return err
		}
		if _, exists := producers[weight.Symbol]; !exists {
			return &ContractError{Code: ErrorInvalidValue, Path: path + ".symbol", Message: "weight references an unknown producer"}
		}
		if _, exists := seenWeights[weight.Symbol]; exists {
			return &ContractError{Code: ErrorInvalidValue, Path: path + ".symbol", Message: "symbol weight must be unique"}
		}
		seenWeights[weight.Symbol] = struct{}{}
	}
	if len(seenWeights) != len(producers) {
		return &ContractError{Code: ErrorInvalidValue, Path: "weights", Message: "every producer must have exactly one explicit weight"}
	}
	return nil
}

// ValidateManualRule validates a rule independently so draft editors can
// reject malformed writes before the full bundle is publishable.
func ValidateManualRule(rule ManualRule) error { return validateManualRule("rule", rule) }

func validateManualRule(path string, rule ManualRule) error {
	if err := validateIdentity(path, rule.LogicalID, rule.Name); err != nil {
		return err
	}
	if rule.ScopeType != "global" || rule.ScopeID != nil {
		return &ContractError{Code: ErrorInvalidValue, Path: path + ".scope_type", Message: "v1 only supports global scope with null scope_id"}
	}
	if !oneOf(rule.Action, ActionAllow, ActionTag, ActionQuarantine) {
		return invalidEnum(path+".action", rule.Action)
	}
	if !oneOf(rule.Mode, ModeShadow, ModeEnforce, ModeDisabled) {
		return invalidEnum(path+".mode", rule.Mode)
	}
	if !oneOf(rule.Source, "manual", "legacy_migration", "external") {
		return invalidEnum(path+".source", rule.Source)
	}
	return validateConditions(path+".conditions", PolicyManual, rule.Conditions)
}

// ValidateAdDetector validates one detector without requiring its bundle's
// weights or composite references to exist yet.
func ValidateAdDetector(detector AdDetector) error { return validateAdDetector("detector", detector) }

func validateAdDetector(path string, detector AdDetector) error {
	if err := validateProducer(path, detector.LogicalID, detector.Name, detector.Symbol, detector.Mode); err != nil {
		return err
	}
	if !oneOf(detector.Source, "local", "rspamd_seed", "stalwart_seed", "spamassassin_seed", "external") {
		return invalidEnum(path+".source", detector.Source)
	}
	if len(detector.SourceReference) > 255 {
		return &ContractError{Code: ErrorInvalidValue, Path: path + ".source_reference", Message: "source_reference must not exceed 255 bytes"}
	}
	return validateConditions(path+".conditions", PolicyAd, detector.Conditions)
}

func ValidateAdComposite(composite AdComposite) error {
	return validateAdComposite("composite", composite)
}

func validateAdComposite(path string, composite AdComposite) error {
	if err := validateProducer(path, composite.LogicalID, composite.Name, composite.Symbol, composite.Mode); err != nil {
		return err
	}
	if !oneOf(composite.ScorePolicy, "keep_inputs", "suppress_direct_inputs") {
		return invalidEnum(path+".score_policy", composite.ScorePolicy)
	}
	if len(composite.AllOf) == 0 && len(composite.AnyOf) == 0 {
		return &ContractError{Code: ErrorInvalidValue, Path: path, Message: "all_of or any_of must contain at least one symbol"}
	}
	if len(composite.AllOf)+len(composite.AnyOf)+len(composite.NoneOf) > maxDirectInputs {
		return &ContractError{Code: ErrorInvalidValue, Path: path, Message: "composite direct input count must not exceed 20"}
	}
	seen := make(map[string]struct{})
	for _, symbol := range append(append(append([]string{}, composite.AllOf...), composite.AnyOf...), composite.NoneOf...) {
		if !symbolPattern.MatchString(symbol) {
			return &ContractError{Code: ErrorInvalidValue, Path: path, Message: "input symbols must use the AD_ namespace"}
		}
		if _, exists := seen[symbol]; exists {
			return &ContractError{Code: ErrorInvalidValue, Path: path, Message: "a direct input symbol may appear only once"}
		}
		seen[symbol] = struct{}{}
	}
	return nil
}

func ValidateSymbolWeight(weight SymbolWeight) error { return validateSymbolWeight("weight", weight) }

func validateSymbolWeight(path string, weight SymbolWeight) error {
	if !symbolPattern.MatchString(weight.Symbol) {
		return &ContractError{Code: ErrorInvalidValue, Path: path + ".symbol", Message: "symbol must match ^AD_[A-Z0-9_]{2,60}$"}
	}
	if weight.Score < -100*1000 || weight.Score > 100*1000 {
		return &ContractError{Code: ErrorInvalidValue, Path: path + ".score", Message: "weight must be in [-100, 100]"}
	}
	return nil
}

func ValidateCondition(policyKind string, condition Condition) error {
	return validateCondition("condition", policyKind, condition)
}

func validateConditions(path, policyKind string, conditions []Condition) error {
	if len(conditions) == 0 || len(conditions) > 20 {
		return &ContractError{Code: ErrorInvalidValue, Path: path, Message: "condition count must be between 1 and 20"}
	}
	positions := make(map[int]struct{}, len(conditions))
	for i, condition := range conditions {
		conditionPath := fmt.Sprintf("%s[%d]", path, i)
		if condition.Position < 0 {
			return &ContractError{Code: ErrorInvalidValue, Path: conditionPath + ".position", Message: "position must not be negative"}
		}
		if _, exists := positions[condition.Position]; exists {
			return &ContractError{Code: ErrorInvalidValue, Path: conditionPath + ".position", Message: "position must be unique"}
		}
		positions[condition.Position] = struct{}{}
		if err := validateCondition(conditionPath, policyKind, condition); err != nil {
			return err
		}
	}
	return nil
}

func validateCondition(path, policyKind string, condition Condition) error {
	allowed := map[string][]string{
		"header_from.address": {"eq"}, "header_from.domain": {"eq", "suffix"},
		"envelope_from.address": {"eq"}, "envelope_from.domain": {"eq", "suffix"},
		"reply_to.domain": {"eq", "suffix"}, "subject": {"contains", "regex"},
		"text": {"contains", "regex"}, "headers": {"exists"},
		"has_attachment": {"eq"}, "attachment.filename": {"suffix", "regex"},
		"size_bytes": {"gte", "lte"},
	}
	if policyKind == PolicyManual {
		allowed["mailbox.address"] = []string{"eq"}
	} else if policyKind == PolicyAd {
		allowed["list_unsubscribe"] = []string{"eq"}
		allowed["list_id"] = []string{"exists"}
		allowed["precedence"] = []string{"eq"}
		allowed["from_reply_to_domain_match"] = []string{"eq"}
		allowed["url_count"] = []string{"gte"}
		allowed["tracking_pixel_count"] = []string{"gte"}
	} else {
		return invalidEnum(path+".policy_kind", policyKind)
	}
	operators, ok := allowed[condition.Field]
	if !ok || !oneOf(condition.Operator, operators...) {
		return &ContractError{Code: ErrorInvalidValue, Path: path, Message: "field/operator combination is not supported"}
	}
	if condition.Operator == "exists" && condition.Field == "list_id" {
		if !condition.Value.IsNull() {
			return &ContractError{Code: ErrorInvalidValue, Path: path + ".value", Message: "list_id exists requires null"}
		}
		return nil
	}
	if condition.Field == "list_unsubscribe" || condition.Field == "from_reply_to_domain_match" || condition.Field == "has_attachment" {
		if _, ok := condition.Value.Bool(); !ok {
			return &ContractError{Code: ErrorInvalidValue, Path: path + ".value", Message: "value must be boolean"}
		}
		return nil
	}
	if condition.Field == "size_bytes" || condition.Field == "url_count" || condition.Field == "tracking_pixel_count" {
		value, ok := condition.Value.Integer()
		if !ok || value < 0 {
			return &ContractError{Code: ErrorInvalidValue, Path: path + ".value", Message: "value must be a non-negative integer"}
		}
		return nil
	}
	value, ok := condition.Value.String()
	if !ok || value == "" || len(value) > maxConditionBytes || strings.ContainsRune(value, '\x00') {
		return &ContractError{Code: ErrorInvalidValue, Path: path + ".value", Message: "value must be a non-empty string of at most 2 KiB"}
	}
	if condition.Field == "headers" && !headerNamePattern.MatchString(value) {
		return &ContractError{Code: ErrorInvalidValue, Path: path + ".value", Message: "header name must be normalized lowercase ASCII"}
	}
	switch condition.Field {
	case "header_from.domain", "envelope_from.domain", "reply_to.domain":
		if !isCanonicalDomain(value) {
			return &ContractError{Code: ErrorInvalidValue, Path: path + ".value", Message: "domain must be canonical lowercase ASCII"}
		}
	case "header_from.address", "envelope_from.address", "mailbox.address":
		if !isCanonicalEmail(value) {
			return &ContractError{Code: ErrorInvalidValue, Path: path + ".value", Message: "email address must be canonical and contain no display name"}
		}
	}
	if condition.Field == "precedence" && !oneOf(value, "bulk", "list", "junk") {
		return invalidEnum(path+".value", value)
	}
	if condition.Operator == "regex" {
		if _, err := regexp.Compile(value); err != nil {
			return &ContractError{Code: ErrorInvalidValue, Path: path + ".value", Message: "value must be a valid Go regular expression"}
		}
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
	if e.NodeID == 0 || strings.TrimSpace(e.Mailbox) == "" {
		return &ContractError{Code: ErrorRequired, Path: "node_id", Message: "node_id and mailbox are required"}
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
		if e.Result.QuarantineKey != "" && (e.Result.Status != "succeeded" || e.Result.ActualAction != ActionQuarantine || e.Result.OriginalMaildirKey == "") {
			return &ContractError{Code: ErrorInvalidValue, Path: "result.quarantine_key", Message: "quarantine result requires succeeded quarantine action and original_maildir_key"}
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

func validateBundleSize(value any) error {
	data, err := MarshalCanonical(value)
	if err != nil {
		return err
	}
	if len(data) > maxBundleBytes {
		return &ContractError{Code: ErrorInvalidValue, Path: "bundle", Message: "canonical bundle must not exceed 4 MiB"}
	}
	return nil
}

func validateIdentity(path, logicalID, name string) error {
	if logicalID == "" || name == "" {
		return &ContractError{Code: ErrorRequired, Path: path, Message: "logical_id and name are required"}
	}
	if !logicalIDPattern.MatchString(logicalID) {
		return &ContractError{Code: ErrorInvalidValue, Path: path + ".logical_id", Message: "logical_id contains unsupported characters or exceeds 64 bytes"}
	}
	if utf8.RuneCountInString(name) > 191 || strings.TrimSpace(name) != name {
		return &ContractError{Code: ErrorInvalidValue, Path: path + ".name", Message: "name must be trimmed and not exceed 191 characters"}
	}
	return nil
}

func validateProducer(path, logicalID, name, symbol, mode string) error {
	if err := validateIdentity(path, logicalID, name); err != nil {
		return err
	}
	if !symbolPattern.MatchString(symbol) {
		return &ContractError{Code: ErrorInvalidValue, Path: path + ".symbol", Message: "symbol must match ^AD_[A-Z0-9_]{2,60}$"}
	}
	if !oneOf(mode, ModeShadow, ModeEnforce, ModeDisabled) {
		return invalidEnum(path+".mode", mode)
	}
	return nil
}

func producerModes(bundle AdBundle) map[string]string {
	result := make(map[string]string, len(bundle.Detectors)+len(bundle.Composites))
	for _, detector := range bundle.Detectors {
		result[detector.Symbol] = detector.Mode
	}
	for _, composite := range bundle.Composites {
		result[composite.Symbol] = composite.Mode
	}
	return result
}

func validateCompositeGraph(composites []AdComposite, producers map[string]struct{}, modes map[string]string) error {
	bySymbol := make(map[string]AdComposite, len(composites))
	for _, composite := range composites {
		bySymbol[composite.Symbol] = composite
	}
	for i, composite := range composites {
		for _, input := range compositeInputs(composite) {
			if _, exists := producers[input]; !exists {
				return &ContractError{Code: ErrorInvalidValue, Path: fmt.Sprintf("composites[%d]", i), Message: "composite references unknown symbol " + input}
			}
			if composite.Mode == ModeEnforce && modes[input] != ModeEnforce {
				return &ContractError{Code: ErrorInvalidValue, Path: fmt.Sprintf("composites[%d].mode", i), Message: "enforce composite may reference only enforce producers"}
			}
			if composite.Mode != ModeDisabled && modes[input] == ModeDisabled {
				return &ContractError{Code: ErrorInvalidValue, Path: fmt.Sprintf("composites[%d].mode", i), Message: "enabled composite may not reference a disabled producer"}
			}
		}
	}
	visiting := make(map[string]bool, len(composites))
	depthMemo := make(map[string]int, len(composites))
	var depth func(string) (int, error)
	depth = func(symbol string) (int, error) {
		if visiting[symbol] {
			return 0, &ContractError{Code: ErrorInvalidValue, Path: "composites", Message: "composite graph contains a cycle at " + symbol}
		}
		if cached := depthMemo[symbol]; cached > 0 {
			return cached, nil
		}
		composite, isComposite := bySymbol[symbol]
		if !isComposite {
			return 0, nil
		}
		visiting[symbol] = true
		maxChild := 0
		for _, input := range compositeInputs(composite) {
			childDepth, err := depth(input)
			if err != nil {
				return 0, err
			}
			if childDepth > maxChild {
				maxChild = childDepth
			}
		}
		visiting[symbol] = false
		result := maxChild + 1
		if result > maxCompositeDepth {
			return 0, &ContractError{Code: ErrorInvalidValue, Path: "composites", Message: "composite graph depth must not exceed 5"}
		}
		depthMemo[symbol] = result
		return result, nil
	}
	for symbol := range bySymbol {
		if _, err := depth(symbol); err != nil {
			return err
		}
	}
	return nil
}

func compositeInputs(composite AdComposite) []string {
	result := make([]string, 0, len(composite.AllOf)+len(composite.AnyOf)+len(composite.NoneOf))
	result = append(result, composite.AllOf...)
	result = append(result, composite.AnyOf...)
	result = append(result, composite.NoneOf...)
	return result
}

func isCanonicalEmail(value string) bool {
	if strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
		return false
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value {
		return false
	}
	at := strings.LastIndexByte(value, '@')
	return at > 0 && at < len(value)-1 && isCanonicalDomain(value[at+1:])
}

func isCanonicalDomain(value string) bool {
	if value == "" || len(value) > 253 || value != strings.ToLower(value) || strings.HasSuffix(value, ".") {
		return false
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
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
