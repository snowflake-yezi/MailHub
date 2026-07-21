package filterdecision

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"

	filtercontract "github.com/ticket/email-filter-contract"
	"github.com/ticket/email-mail-node/internal/adfilter"
	"github.com/ticket/email-mail-node/internal/manualfilter"
)

type state struct {
	manual *manualfilter.Snapshot
	ad     *adfilter.Snapshot
}

type Engine struct {
	current atomic.Pointer[state]
}

type Options struct {
	AutoQuarantineEnabled bool
	EvaluatedAt           time.Time
}

type PolicyState struct {
	Revision uint64
	Checksum string
}

func New() *Engine {
	engine := &Engine{}
	engine.current.Store(&state{})
	return engine
}

func (engine *Engine) ApplyManual(bundle filtercontract.ManualBundle) error {
	candidate, err := manualfilter.Compile(bundle)
	if err != nil {
		return err
	}
	for {
		current := engine.current.Load()
		next := &state{manual: candidate, ad: current.ad}
		if engine.current.CompareAndSwap(current, next) {
			return nil
		}
	}
}

func (engine *Engine) ApplyAd(bundle filtercontract.AdBundle) error {
	candidate, err := adfilter.Compile(bundle)
	if err != nil {
		return err
	}
	for {
		current := engine.current.Load()
		next := &state{manual: current.manual, ad: candidate}
		if engine.current.CompareAndSwap(current, next) {
			return nil
		}
	}
}

func (engine *Engine) State(policyKind string) PolicyState {
	current := engine.current.Load()
	if policyKind == filtercontract.PolicyManual && current.manual != nil {
		return PolicyState{Revision: current.manual.Revision(), Checksum: current.manual.Checksum()}
	}
	if policyKind == filtercontract.PolicyAd && current.ad != nil {
		return PolicyState{Revision: current.ad.Revision(), Checksum: current.ad.Checksum()}
	}
	return PolicyState{}
}

func (engine *Engine) Evaluate(features filtercontract.MailFeatures, options Options) (filtercontract.FilterDecision, error) {
	captured := engine.current.Load()
	manualResult := manualfilter.Result{Action: filtercontract.ActionAllow, Reasons: []filtercontract.DecisionReason{}, ShadowResults: []filtercontract.ShadowResult{}}
	manualRevision := uint64(0)
	if captured.manual != nil {
		manualResult = captured.manual.Evaluate(features)
		manualRevision = captured.manual.Revision()
	}
	adResult := adfilter.Result{Action: filtercontract.ActionAllow, Symbols: []filtercontract.AdSymbolResult{}, ShadowResults: []filtercontract.ShadowResult{}}
	adRevision := uint64(0)
	if captured.ad != nil {
		adResult = captured.ad.Evaluate(features)
		adRevision = captured.ad.Revision()
	}
	finalAction := adResult.Action
	if manualResult.Matched {
		switch manualResult.Action {
		case filtercontract.ActionAllow, filtercontract.ActionQuarantine:
			finalAction = manualResult.Action
		case filtercontract.ActionTag:
			finalAction = stricterAction(filtercontract.ActionTag, adResult.Action)
		}
	}
	shadow := append([]filtercontract.ShadowResult{}, manualResult.ShadowResults...)
	shadow = append(shadow, adResult.ShadowResults...)
	if finalAction == filtercontract.ActionQuarantine && !options.AutoQuarantineEnabled && (!manualResult.Matched || manualResult.Action != filtercontract.ActionQuarantine) {
		shadow = append(shadow, filtercontract.ShadowResult{
			PolicyKind: filtercontract.PolicyAd, ProducerLogicalID: "auto-quarantine-disabled",
			Action: filtercontract.ActionQuarantine, Score: adResult.Score, Symbols: []filtercontract.AdSymbolResult{}, Evidence: []filtercontract.Evidence{},
		})
		finalAction = filtercontract.ActionTag
	}
	if options.EvaluatedAt.IsZero() {
		options.EvaluatedAt = time.Now().UTC()
	} else {
		options.EvaluatedAt = options.EvaluatedAt.UTC()
	}
	decision := filtercontract.FilterDecision{
		SchemaVersion: filtercontract.SchemaVersionV1, DecisionKey: decisionKey(features.MessageKey, manualRevision, adRevision),
		MessageKey: features.MessageKey, ManualRevision: manualRevision, AdRevision: adRevision,
		ManualAction: manualResult.Action, AdAction: adResult.Action, FinalAction: finalAction, AdScore: adResult.Score,
		Reasons: manualResult.Reasons, AdSymbols: adResult.Symbols, ShadowResults: shadow,
		ParseWarnings: append([]string(nil), features.ParseWarnings...), EvaluatedAt: options.EvaluatedAt,
	}
	if err := decision.Validate(); err != nil {
		return filtercontract.FilterDecision{}, fmt.Errorf("validate decision: %w", err)
	}
	return decision, nil
}

func decisionKey(messageKey string, manualRevision, adRevision uint64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d", messageKey, manualRevision, adRevision)))
	return hex.EncodeToString(sum[:])
}

func stricterAction(left, right string) string {
	severity := map[string]int{filtercontract.ActionAllow: 0, filtercontract.ActionTag: 1, filtercontract.ActionQuarantine: 2}
	if severity[right] > severity[left] {
		return right
	}
	return left
}
