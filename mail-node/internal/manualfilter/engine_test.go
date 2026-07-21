package manualfilter

import (
	"testing"

	filtercontract "github.com/ticket/email-filter-contract"
)

func TestEvaluateUsesFirstEnforceRuleAndRecordsShadow(t *testing.T) {
	bundle := filtercontract.ManualBundle{
		SchemaVersion: filtercontract.SchemaVersionV1, PolicyKind: filtercontract.PolicyManual, Revision: 3,
		Rules: []filtercontract.ManualRule{
			{LogicalID: "later", Name: "Later", ScopeType: "global", Action: filtercontract.ActionQuarantine, Priority: 20, Mode: filtercontract.ModeEnforce, Source: "manual", Conditions: []filtercontract.Condition{{Field: "subject", Operator: "contains", Value: filtercontract.StringValue("sale"), Position: 0}}},
			{LogicalID: "first", Name: "First", ScopeType: "global", Action: filtercontract.ActionTag, Priority: 10, Mode: filtercontract.ModeEnforce, Source: "manual", Conditions: []filtercontract.Condition{{Field: "subject", Operator: "contains", Value: filtercontract.StringValue("sale"), Position: 0}}},
			{LogicalID: "candidate", Name: "Candidate", ScopeType: "global", Action: filtercontract.ActionQuarantine, Priority: 1, Mode: filtercontract.ModeShadow, Source: "manual", Conditions: []filtercontract.Condition{{Field: "subject", Operator: "contains", Value: filtercontract.StringValue("sale"), Position: 0}}},
		},
	}
	checksum, err := bundle.CalculatedChecksum()
	if err != nil {
		t.Fatal(err)
	}
	bundle.Checksum = checksum
	snapshot, err := Compile(bundle)
	if err != nil {
		t.Fatal(err)
	}
	result := snapshot.Evaluate(filtercontract.MailFeatures{Subject: "Summer Sale"})
	if !result.Matched || result.Action != filtercontract.ActionTag || result.Reasons[0].LogicalID != "first" {
		t.Fatalf("result = %#v", result)
	}
	if len(result.ShadowResults) != 1 || result.ShadowResults[0].ProducerLogicalID != "candidate" {
		t.Fatalf("shadow = %#v", result.ShadowResults)
	}
}
