package filtercontract

import (
	"errors"
	"testing"
)

func TestManualBundleRejectsUnsupportedCondition(t *testing.T) {
	bundle := ManualBundle{
		SchemaVersion: SchemaVersionV1, PolicyKind: PolicyManual, Revision: 1,
		Rules: []ManualRule{{
			LogicalID: "manual-1", Name: "Manual rule", ScopeType: "global", Action: ActionAllow, Mode: ModeShadow, Source: "manual",
			Conditions: []Condition{{Field: "url_count", Operator: "gte", Value: IntegerValue(2), Position: 0}},
		}},
	}
	signManualBundle(t, &bundle)
	var contractError *ContractError
	if err := bundle.Validate(); !errors.As(err, &contractError) || contractError.Code != ErrorInvalidValue {
		t.Fatalf("Validate() error = %v, want invalid condition", err)
	}
}

func TestAdBundleRejectsGraphErrors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AdBundle)
	}{
		{
			name: "unknown symbol",
			mutate: func(bundle *AdBundle) {
				bundle.Composites[0].AnyOf = []string{"AD_UNKNOWN"}
			},
		},
		{
			name: "cycle",
			mutate: func(bundle *AdBundle) {
				bundle.Composites = append(bundle.Composites, AdComposite{
					LogicalID: "composite-2", Name: "Second composite", Symbol: "AD_SECOND_COMPOSITE", Mode: ModeShadow,
					ScorePolicy: "keep_inputs", AllOf: []string{"AD_TEST_COMPOSITE"},
				})
				bundle.Composites[0].AnyOf = []string{"AD_SECOND_COMPOSITE"}
				bundle.Weights = append(bundle.Weights, SymbolWeight{Symbol: "AD_SECOND_COMPOSITE", Score: 1000})
			},
		},
		{
			name: "enforce references shadow",
			mutate: func(bundle *AdBundle) {
				bundle.Composites[0].Mode = ModeEnforce
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bundle := validAdBundleForPolicyValidation()
			test.mutate(&bundle)
			signAdBundle(t, &bundle)
			if err := bundle.Validate(); err == nil {
				t.Fatal("invalid graph was accepted")
			}
		})
	}
}

func TestAdBundleAcceptsCompleteShadowGraph(t *testing.T) {
	bundle := validAdBundleForPolicyValidation()
	signAdBundle(t, &bundle)
	if err := bundle.Validate(); err != nil {
		t.Fatalf("valid bundle rejected: %v", err)
	}
}

func validAdBundleForPolicyValidation() AdBundle {
	return AdBundle{
		SchemaVersion: SchemaVersionV1, PolicyKind: PolicyAd, Revision: 1,
		TagThreshold: 4000, QuarantineThreshold: 7000,
		Detectors: []AdDetector{{
			LogicalID: "detector-1", Name: "Test detector", Symbol: "AD_TEST_SIGNAL", Mode: ModeShadow, Source: "local",
			Conditions: []Condition{{Field: "subject", Operator: "contains", Value: StringValue("sale"), Position: 0}},
		}},
		Composites: []AdComposite{{
			LogicalID: "composite-1", Name: "Test composite", Symbol: "AD_TEST_COMPOSITE", Mode: ModeShadow,
			ScorePolicy: "keep_inputs", AnyOf: []string{"AD_TEST_SIGNAL"},
		}},
		Weights: []SymbolWeight{{Symbol: "AD_TEST_COMPOSITE", Score: 2000}, {Symbol: "AD_TEST_SIGNAL", Score: 1000}},
	}
}

func signManualBundle(t *testing.T, bundle *ManualBundle) {
	t.Helper()
	checksum, err := bundle.CalculatedChecksum()
	if err != nil {
		t.Fatal(err)
	}
	bundle.Checksum = checksum
}

func signAdBundle(t *testing.T, bundle *AdBundle) {
	t.Helper()
	checksum, err := bundle.CalculatedChecksum()
	if err != nil {
		t.Fatal(err)
	}
	bundle.Checksum = checksum
}
