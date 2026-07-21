package filterdecision

import (
	"strings"
	"testing"
	"time"

	filtercontract "github.com/ticket/email-filter-contract"
)

func TestInvalidCandidateKeepsLastKnownGood(t *testing.T) {
	engine := New()
	manual := manualBundle(t, filtercontract.ActionAllow)
	if err := engine.ApplyManual(manual); err != nil {
		t.Fatal(err)
	}
	invalid := manual
	invalid.Checksum = strings.Repeat("0", 64)
	if err := engine.ApplyManual(invalid); err == nil {
		t.Fatal("invalid checksum was applied")
	}
	if state := engine.State(filtercontract.PolicyManual); state.Revision != manual.Revision || state.Checksum != manual.Checksum {
		t.Fatalf("state = %#v", state)
	}
}

func TestManualAllowOverridesAdAndAutoQuarantineFailsClosedToTag(t *testing.T) {
	engine := New()
	if err := engine.ApplyAd(adBundle(t)); err != nil {
		t.Fatal(err)
	}
	features := filtercontract.MailFeatures{MessageKey: strings.Repeat("a", 64), Subject: "sale"}
	decision, err := engine.Evaluate(features, Options{EvaluatedAt: time.Unix(100, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if decision.AdAction != filtercontract.ActionQuarantine || decision.FinalAction != filtercontract.ActionTag {
		t.Fatalf("decision = %#v", decision)
	}
	if err := engine.ApplyManual(manualBundle(t, filtercontract.ActionAllow)); err != nil {
		t.Fatal(err)
	}
	decision, err = engine.Evaluate(features, Options{EvaluatedAt: time.Unix(100, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if decision.FinalAction != filtercontract.ActionAllow {
		t.Fatalf("manual allow final action = %s", decision.FinalAction)
	}
}

func manualBundle(t *testing.T, action string) filtercontract.ManualBundle {
	t.Helper()
	bundle := filtercontract.ManualBundle{
		SchemaVersion: 1, PolicyKind: filtercontract.PolicyManual, Revision: 2,
		Rules: []filtercontract.ManualRule{{LogicalID: "manual", Name: "Manual", ScopeType: "global", Action: action, Priority: 1, Mode: filtercontract.ModeEnforce, Source: "manual", Conditions: []filtercontract.Condition{{Field: "subject", Operator: "contains", Value: filtercontract.StringValue("sale"), Position: 0}}}},
	}
	bundle.Checksum, _ = bundle.CalculatedChecksum()
	return bundle
}

func adBundle(t *testing.T) filtercontract.AdBundle {
	t.Helper()
	bundle := filtercontract.AdBundle{
		SchemaVersion: 1, PolicyKind: filtercontract.PolicyAd, Revision: 4,
		TagThreshold: filtercontract.Score(2000), QuarantineThreshold: filtercontract.Score(5000),
		Detectors:  []filtercontract.AdDetector{{LogicalID: "sale", Name: "Sale", Symbol: "AD_SALE", Mode: filtercontract.ModeEnforce, Source: "local", Conditions: []filtercontract.Condition{{Field: "subject", Operator: "contains", Value: filtercontract.StringValue("sale"), Position: 0}}}},
		Composites: []filtercontract.AdComposite{}, Weights: []filtercontract.SymbolWeight{{Symbol: "AD_SALE", Score: filtercontract.Score(6000)}},
	}
	bundle.Checksum, _ = bundle.CalculatedChecksum()
	return bundle
}
