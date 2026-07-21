package adfilter

import (
	"fmt"
	"sort"

	filtercontract "github.com/ticket/email-filter-contract"
	"github.com/ticket/email-mail-node/internal/filtermatch"
)

type compiledDetector struct {
	value      filtercontract.AdDetector
	conditions []filtermatch.Condition
}

type Snapshot struct {
	revision            uint64
	checksum            string
	tagThreshold        filtercontract.Score
	quarantineThreshold filtercontract.Score
	detectors           []compiledDetector
	composites          []filtercontract.AdComposite
	weights             map[string]filtercontract.Score
	producers           map[string]producer
}

type producer struct {
	logicalID string
	mode      string
}

type Result struct {
	Action        string
	Score         filtercontract.Score
	Symbols       []filtercontract.AdSymbolResult
	ShadowResults []filtercontract.ShadowResult
}

type matchState struct {
	matched  bool
	evidence []filtercontract.Evidence
}

func Compile(bundle filtercontract.AdBundle) (*Snapshot, error) {
	if err := bundle.Validate(); err != nil {
		return nil, err
	}
	snapshot := &Snapshot{
		revision: bundle.Revision, checksum: bundle.Checksum, tagThreshold: bundle.TagThreshold,
		quarantineThreshold: bundle.QuarantineThreshold, weights: map[string]filtercontract.Score{}, producers: map[string]producer{},
	}
	for _, weight := range bundle.Weights {
		snapshot.weights[weight.Symbol] = weight.Score
	}
	for _, detector := range bundle.Detectors {
		snapshot.producers[detector.Symbol] = producer{logicalID: detector.LogicalID, mode: detector.Mode}
		if detector.Mode == filtercontract.ModeDisabled {
			continue
		}
		conditions, err := filtermatch.Compile(filtercontract.PolicyAd, detector.Conditions)
		if err != nil {
			return nil, err
		}
		snapshot.detectors = append(snapshot.detectors, compiledDetector{value: detector, conditions: conditions})
	}
	for _, composite := range bundle.Composites {
		snapshot.producers[composite.Symbol] = producer{logicalID: composite.LogicalID, mode: composite.Mode}
	}
	ordered, err := orderComposites(bundle.Composites)
	if err != nil {
		return nil, err
	}
	for _, composite := range ordered {
		if composite.Mode != filtercontract.ModeDisabled {
			snapshot.composites = append(snapshot.composites, composite)
		}
	}
	return snapshot, nil
}

func (snapshot *Snapshot) Revision() uint64 { return snapshot.revision }
func (snapshot *Snapshot) Checksum() string { return snapshot.checksum }

func (snapshot *Snapshot) Evaluate(features filtercontract.MailFeatures) Result {
	states := map[string]matchState{}
	for _, detector := range snapshot.detectors {
		matched := filtermatch.MatchAll(detector.conditions, features)
		states[detector.value.Symbol] = matchState{matched: matched.Matched, evidence: matched.Evidence}
	}
	for _, composite := range snapshot.composites {
		matched := compositeMatches(composite, states)
		states[composite.Symbol] = matchState{matched: matched, evidence: []filtercontract.Evidence{}}
	}

	suppressed := map[string][]string{}
	for _, composite := range snapshot.composites {
		if composite.Mode != filtercontract.ModeEnforce || composite.ScorePolicy != "suppress_direct_inputs" || !states[composite.Symbol].matched {
			continue
		}
		for _, symbol := range append(append([]string{}, composite.AllOf...), composite.AnyOf...) {
			if states[symbol].matched {
				suppressed[symbol] = append(suppressed[symbol], composite.Symbol)
			}
		}
	}

	result := Result{Action: filtercontract.ActionAllow, Symbols: []filtercontract.AdSymbolResult{}, ShadowResults: []filtercontract.ShadowResult{}}
	shadowSymbols := []filtercontract.AdSymbolResult{}
	shadowScore := filtercontract.Score(0)
	symbols := make([]string, 0, len(snapshot.producers))
	for symbol := range snapshot.producers {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	for _, symbol := range symbols {
		meta := snapshot.producers[symbol]
		state := states[symbol]
		weight := snapshot.weights[symbol]
		item := filtercontract.AdSymbolResult{
			ProducerLogicalID: meta.logicalID, Symbol: symbol, Matched: state.matched, Weight: weight,
			SuppressedBy: append([]string(nil), suppressed[symbol]...), Evidence: state.evidence,
		}
		if state.matched {
			item.OccurrenceCount = evidenceOccurrences(state.evidence)
			if item.OccurrenceCount == 0 {
				item.OccurrenceCount = 1
			}
			if len(item.SuppressedBy) == 0 {
				item.Contribution = weight
			}
		}
		if meta.mode == filtercontract.ModeEnforce {
			result.Score += item.Contribution
			result.Symbols = append(result.Symbols, item)
		} else if meta.mode == filtercontract.ModeShadow {
			shadowScore += item.Contribution
			shadowSymbols = append(shadowSymbols, item)
		}
	}
	result.Action = thresholdAction(result.Score, snapshot.tagThreshold, snapshot.quarantineThreshold)
	if len(shadowSymbols) > 0 {
		result.ShadowResults = append(result.ShadowResults, filtercontract.ShadowResult{
			PolicyKind: filtercontract.PolicyAd, ProducerLogicalID: "shadow-graph",
			Action: thresholdAction(shadowScore, snapshot.tagThreshold, snapshot.quarantineThreshold),
			Score:  shadowScore, Symbols: shadowSymbols, Evidence: []filtercontract.Evidence{},
		})
	}
	return result
}

func compositeMatches(value filtercontract.AdComposite, states map[string]matchState) bool {
	for _, symbol := range value.AllOf {
		if !states[symbol].matched {
			return false
		}
	}
	if len(value.AnyOf) > 0 {
		matched := false
		for _, symbol := range value.AnyOf {
			matched = matched || states[symbol].matched
		}
		if !matched {
			return false
		}
	}
	for _, symbol := range value.NoneOf {
		if states[symbol].matched {
			return false
		}
	}
	return len(value.AllOf) > 0 || len(value.AnyOf) > 0
}

func thresholdAction(score, tag, quarantine filtercontract.Score) string {
	if score >= quarantine {
		return filtercontract.ActionQuarantine
	}
	if score >= tag {
		return filtercontract.ActionTag
	}
	return filtercontract.ActionAllow
}

func evidenceOccurrences(values []filtercontract.Evidence) int {
	result := 0
	for _, value := range values {
		result += value.Occurrences
	}
	return result
}

func orderComposites(values []filtercontract.AdComposite) ([]filtercontract.AdComposite, error) {
	bySymbol := map[string]filtercontract.AdComposite{}
	for _, value := range values {
		bySymbol[value.Symbol] = value
	}
	visited, visiting := map[string]bool{}, map[string]bool{}
	result := make([]filtercontract.AdComposite, 0, len(values))
	var visit func(string) error
	visit = func(symbol string) error {
		if visited[symbol] {
			return nil
		}
		if visiting[symbol] {
			return fmt.Errorf("composite cycle at %s", symbol)
		}
		value, exists := bySymbol[symbol]
		if !exists {
			return nil
		}
		visiting[symbol] = true
		for _, dependency := range append(append(append([]string{}, value.AllOf...), value.AnyOf...), value.NoneOf...) {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visiting[symbol] = false
		visited[symbol] = true
		result = append(result, value)
		return nil
	}
	symbols := make([]string, 0, len(bySymbol))
	for symbol := range bySymbol {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	for _, symbol := range symbols {
		if err := visit(symbol); err != nil {
			return nil, err
		}
	}
	return result, nil
}
