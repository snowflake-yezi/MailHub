package filtercontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// MarshalCanonical encodes contract JSON without insignificant whitespace.
// Struct field order defines object order, map keys are ordered by encoding/json,
// and callers must normalize semantically unordered arrays first.
func MarshalCanonical(value any) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(output.Bytes(), []byte("\n")), nil
}

func (b ManualBundle) CanonicalJSON() ([]byte, error) {
	normalized := normalizeManualBundle(b)
	return MarshalCanonical(normalized)
}

func (b ManualBundle) CalculatedChecksum() (string, error) {
	b.Checksum = ""
	data, err := b.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (b AdBundle) CanonicalJSON() ([]byte, error) {
	normalized := normalizeAdBundle(b)
	return MarshalCanonical(normalized)
}

func (b AdBundle) CalculatedChecksum() (string, error) {
	b.Checksum = ""
	data, err := b.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (d FilterDecision) CanonicalJSON() ([]byte, error) {
	normalized := normalizeDecision(d)
	return MarshalCanonical(normalized)
}

func (e OutboxEvent) CanonicalJSON() ([]byte, error) {
	e.Decision = normalizeDecision(e.Decision)
	return MarshalCanonical(e)
}

func (r ReleaseReceipt) CanonicalJSON() ([]byte, error) {
	return MarshalCanonical(r)
}

func normalizeManualBundle(bundle ManualBundle) ManualBundle {
	bundle.Rules = append([]ManualRule(nil), bundle.Rules...)
	if bundle.Rules == nil {
		bundle.Rules = []ManualRule{}
	}
	for i := range bundle.Rules {
		bundle.Rules[i].Conditions = normalizeConditions(bundle.Rules[i].Conditions)
	}
	sort.SliceStable(bundle.Rules, func(i, j int) bool {
		if bundle.Rules[i].Priority != bundle.Rules[j].Priority {
			return bundle.Rules[i].Priority < bundle.Rules[j].Priority
		}
		return bundle.Rules[i].LogicalID < bundle.Rules[j].LogicalID
	})
	return bundle
}

func normalizeAdBundle(bundle AdBundle) AdBundle {
	bundle.Detectors = append([]AdDetector(nil), bundle.Detectors...)
	bundle.Composites = append([]AdComposite(nil), bundle.Composites...)
	bundle.Weights = append([]SymbolWeight(nil), bundle.Weights...)
	if bundle.Detectors == nil {
		bundle.Detectors = []AdDetector{}
	}
	if bundle.Composites == nil {
		bundle.Composites = []AdComposite{}
	}
	if bundle.Weights == nil {
		bundle.Weights = []SymbolWeight{}
	}
	for i := range bundle.Detectors {
		bundle.Detectors[i].Conditions = normalizeConditions(bundle.Detectors[i].Conditions)
	}
	for i := range bundle.Composites {
		bundle.Composites[i].AllOf = sortedStrings(bundle.Composites[i].AllOf)
		bundle.Composites[i].AnyOf = sortedStrings(bundle.Composites[i].AnyOf)
		bundle.Composites[i].NoneOf = sortedStrings(bundle.Composites[i].NoneOf)
	}
	sort.SliceStable(bundle.Detectors, func(i, j int) bool {
		if bundle.Detectors[i].Symbol != bundle.Detectors[j].Symbol {
			return bundle.Detectors[i].Symbol < bundle.Detectors[j].Symbol
		}
		return bundle.Detectors[i].LogicalID < bundle.Detectors[j].LogicalID
	})
	sort.SliceStable(bundle.Composites, func(i, j int) bool {
		if bundle.Composites[i].Symbol != bundle.Composites[j].Symbol {
			return bundle.Composites[i].Symbol < bundle.Composites[j].Symbol
		}
		return bundle.Composites[i].LogicalID < bundle.Composites[j].LogicalID
	})
	sort.SliceStable(bundle.Weights, func(i, j int) bool {
		return bundle.Weights[i].Symbol < bundle.Weights[j].Symbol
	})
	return bundle
}

func normalizeDecision(decision FilterDecision) FilterDecision {
	decision.Reasons = append([]DecisionReason(nil), decision.Reasons...)
	decision.AdSymbols = normalizeSymbolResults(decision.AdSymbols)
	decision.ShadowResults = append([]ShadowResult(nil), decision.ShadowResults...)
	decision.ParseWarnings = sortedStrings(decision.ParseWarnings)
	if decision.Reasons == nil {
		decision.Reasons = []DecisionReason{}
	}
	if decision.ShadowResults == nil {
		decision.ShadowResults = []ShadowResult{}
	}
	for i := range decision.Reasons {
		decision.Reasons[i].MatchedFields = sortedStrings(decision.Reasons[i].MatchedFields)
		decision.Reasons[i].Evidence = normalizeEvidence(decision.Reasons[i].Evidence)
	}
	for i := range decision.ShadowResults {
		decision.ShadowResults[i].Symbols = normalizeSymbolResults(decision.ShadowResults[i].Symbols)
		decision.ShadowResults[i].Evidence = normalizeEvidence(decision.ShadowResults[i].Evidence)
	}
	sort.SliceStable(decision.Reasons, func(i, j int) bool {
		return decision.Reasons[i].LogicalID < decision.Reasons[j].LogicalID
	})
	sort.SliceStable(decision.ShadowResults, func(i, j int) bool {
		left, right := decision.ShadowResults[i], decision.ShadowResults[j]
		if left.PolicyKind != right.PolicyKind {
			return left.PolicyKind < right.PolicyKind
		}
		if left.ProducerLogicalID != right.ProducerLogicalID {
			return left.ProducerLogicalID < right.ProducerLogicalID
		}
		return left.Symbol < right.Symbol
	})
	return decision
}

func normalizeConditions(conditions []Condition) []Condition {
	result := append([]Condition(nil), conditions...)
	if result == nil {
		return []Condition{}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Position != result[j].Position {
			return result[i].Position < result[j].Position
		}
		if result[i].Field != result[j].Field {
			return result[i].Field < result[j].Field
		}
		return result[i].Operator < result[j].Operator
	})
	return result
}

func normalizeSymbolResults(symbols []AdSymbolResult) []AdSymbolResult {
	result := append([]AdSymbolResult(nil), symbols...)
	if result == nil {
		return []AdSymbolResult{}
	}
	for i := range result {
		result[i].SuppressedBy = sortedStrings(result[i].SuppressedBy)
		result[i].Evidence = normalizeEvidence(result[i].Evidence)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Symbol != result[j].Symbol {
			return result[i].Symbol < result[j].Symbol
		}
		return result[i].ProducerLogicalID < result[j].ProducerLogicalID
	})
	return result
}

func normalizeEvidence(evidence []Evidence) []Evidence {
	result := append([]Evidence(nil), evidence...)
	if result == nil {
		return []Evidence{}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Field != result[j].Field {
			return result[i].Field < result[j].Field
		}
		if result[i].Summary != result[j].Summary {
			return result[i].Summary < result[j].Summary
		}
		return result[i].Occurrences < result[j].Occurrences
	})
	return result
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	if result == nil {
		return []string{}
	}
	sort.Strings(result)
	return result
}
