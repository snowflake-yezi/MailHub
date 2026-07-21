package manualfilter

import (
	"sort"

	filtercontract "github.com/ticket/email-filter-contract"
	"github.com/ticket/email-mail-node/internal/filtermatch"
)

type compiledRule struct {
	rule       filtercontract.ManualRule
	conditions []filtermatch.Condition
}

type Snapshot struct {
	revision uint64
	checksum string
	rules    []compiledRule
}

type Result struct {
	Matched       bool
	Action        string
	Reasons       []filtercontract.DecisionReason
	ShadowResults []filtercontract.ShadowResult
}

func Compile(bundle filtercontract.ManualBundle) (*Snapshot, error) {
	if err := bundle.Validate(); err != nil {
		return nil, err
	}
	rules := append([]filtercontract.ManualRule(nil), bundle.Rules...)
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Priority != rules[j].Priority {
			return rules[i].Priority < rules[j].Priority
		}
		return rules[i].LogicalID < rules[j].LogicalID
	})
	compiled := make([]compiledRule, 0, len(rules))
	for _, rule := range rules {
		if rule.Mode == filtercontract.ModeDisabled {
			continue
		}
		conditions, err := filtermatch.Compile(filtercontract.PolicyManual, rule.Conditions)
		if err != nil {
			return nil, err
		}
		compiled = append(compiled, compiledRule{rule: rule, conditions: conditions})
	}
	return &Snapshot{revision: bundle.Revision, checksum: bundle.Checksum, rules: compiled}, nil
}

func (snapshot *Snapshot) Revision() uint64 { return snapshot.revision }
func (snapshot *Snapshot) Checksum() string { return snapshot.checksum }

func (snapshot *Snapshot) Evaluate(features filtercontract.MailFeatures) Result {
	result := Result{Action: filtercontract.ActionAllow, Reasons: []filtercontract.DecisionReason{}, ShadowResults: []filtercontract.ShadowResult{}}
	for _, compiled := range snapshot.rules {
		matched := filtermatch.MatchAll(compiled.conditions, features)
		if !matched.Matched {
			continue
		}
		if compiled.rule.Mode == filtercontract.ModeShadow {
			result.ShadowResults = append(result.ShadowResults, filtercontract.ShadowResult{
				PolicyKind: filtercontract.PolicyManual, ProducerLogicalID: compiled.rule.LogicalID,
				Action: compiled.rule.Action, Evidence: matched.Evidence, Symbols: []filtercontract.AdSymbolResult{},
			})
			continue
		}
		if !result.Matched {
			result.Matched = true
			result.Action = compiled.rule.Action
			result.Reasons = append(result.Reasons, filtercontract.DecisionReason{
				LogicalID: compiled.rule.LogicalID, Name: compiled.rule.Name, Action: compiled.rule.Action,
				MatchedFields: matched.Fields, Evidence: matched.Evidence,
			})
		}
	}
	return result
}
