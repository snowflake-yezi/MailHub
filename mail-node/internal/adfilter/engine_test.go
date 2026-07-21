package adfilter

import (
	"testing"

	filtercontract "github.com/ticket/email-filter-contract"
)

func TestCompositeSuppressesDirectInputsAndShadowIsSeparate(t *testing.T) {
	bundle := testBundle(t)
	snapshot, err := Compile(bundle)
	if err != nil {
		t.Fatal(err)
	}
	result := snapshot.Evaluate(filtercontract.MailFeatures{Subject: "sale offer"})
	if result.Action != filtercontract.ActionTag || result.Score != filtercontract.Score(7000) {
		t.Fatalf("action/score = %s/%s", result.Action, result.Score.String())
	}
	bySymbol := map[string]filtercontract.AdSymbolResult{}
	for _, symbol := range result.Symbols {
		bySymbol[symbol.Symbol] = symbol
	}
	if bySymbol["AD_SALE"].Contribution != 0 || len(bySymbol["AD_SALE"].SuppressedBy) != 1 {
		t.Fatalf("sale result = %#v", bySymbol["AD_SALE"])
	}
	if bySymbol["AD_COMBINED"].Contribution != filtercontract.Score(7000) {
		t.Fatalf("combined result = %#v", bySymbol["AD_COMBINED"])
	}
	if len(result.ShadowResults) != 1 || result.ShadowResults[0].Action != filtercontract.ActionQuarantine {
		t.Fatalf("shadow = %#v", result.ShadowResults)
	}
}

func testBundle(t *testing.T) filtercontract.AdBundle {
	t.Helper()
	bundle := filtercontract.AdBundle{
		SchemaVersion: filtercontract.SchemaVersionV1, PolicyKind: filtercontract.PolicyAd, Revision: 8,
		TagThreshold: filtercontract.Score(5000), QuarantineThreshold: filtercontract.Score(10000),
		Detectors: []filtercontract.AdDetector{
			{LogicalID: "sale", Name: "Sale", Symbol: "AD_SALE", Mode: filtercontract.ModeEnforce, Source: "local", Conditions: []filtercontract.Condition{{Field: "subject", Operator: "contains", Value: filtercontract.StringValue("sale"), Position: 0}}},
			{LogicalID: "offer", Name: "Offer", Symbol: "AD_OFFER", Mode: filtercontract.ModeEnforce, Source: "local", Conditions: []filtercontract.Condition{{Field: "subject", Operator: "contains", Value: filtercontract.StringValue("offer"), Position: 0}}},
			{LogicalID: "shadow", Name: "Shadow", Symbol: "AD_SHADOW", Mode: filtercontract.ModeShadow, Source: "local", Conditions: []filtercontract.Condition{{Field: "subject", Operator: "contains", Value: filtercontract.StringValue("sale"), Position: 0}}},
		},
		Composites: []filtercontract.AdComposite{{LogicalID: "combined", Name: "Combined", Symbol: "AD_COMBINED", Mode: filtercontract.ModeEnforce, ScorePolicy: "suppress_direct_inputs", AllOf: []string{"AD_SALE", "AD_OFFER"}, AnyOf: []string{}, NoneOf: []string{}}},
		Weights: []filtercontract.SymbolWeight{
			{Symbol: "AD_SALE", Score: filtercontract.Score(2000)}, {Symbol: "AD_OFFER", Score: filtercontract.Score(3000)},
			{Symbol: "AD_COMBINED", Score: filtercontract.Score(7000)}, {Symbol: "AD_SHADOW", Score: filtercontract.Score(12000)},
		},
	}
	checksum, err := bundle.CalculatedChecksum()
	if err != nil {
		t.Fatal(err)
	}
	bundle.Checksum = checksum
	return bundle
}
