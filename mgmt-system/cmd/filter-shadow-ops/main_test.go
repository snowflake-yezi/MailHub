package main

import (
	"strings"
	"testing"

	filtercontract "github.com/ticket/email-filter-contract"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/service"
)

const testChecksum = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestValidateShadowDrafts(t *testing.T) {
	manual := &service.ManualFilterRevisionView{Status: "draft", Rules: []filtercontract.ManualRule{}}
	ad := &service.AdFilterRevisionView{
		Status:     "draft",
		Detectors:  []filtercontract.AdDetector{{LogicalID: "detector", Mode: filtercontract.ModeShadow}},
		Composites: []filtercontract.AdComposite{{LogicalID: "composite", Mode: filtercontract.ModeShadow}},
		Weights:    []filtercontract.SymbolWeight{{Symbol: "AD_TEST", Score: 1000}},
	}
	valid := service.FilterPolicyValidation{Valid: true, Checksum: testChecksum}
	if err := validateShadowDrafts(manual, ad, valid, valid, testChecksum); err != nil {
		t.Fatal(err)
	}
	ad.Detectors[0].Mode = filtercontract.ModeEnforce
	if err := validateShadowDrafts(manual, ad, valid, valid, testChecksum); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("enforce detector error = %v", err)
	}
}

func TestValidateConvergence(t *testing.T) {
	manualChecksum := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	status := &service.FilterPolicyStatus{
		ActiveStates: []model.FilterActiveState{
			{PolicyKind: filtercontract.PolicyManual, ActiveRevision: 1, Checksum: manualChecksum},
			{PolicyKind: filtercontract.PolicyAd, ActiveRevision: 1, Checksum: testChecksum},
		},
		NodeStates: []model.FilterNodeState{
			{NodeID: 1, PolicyKind: filtercontract.PolicyManual, DesiredRevision: 1, AppliedRevision: 1, Checksum: manualChecksum},
			{NodeID: 1, PolicyKind: filtercontract.PolicyAd, DesiredRevision: 1, AppliedRevision: 1, Checksum: testChecksum},
			{NodeID: 2, PolicyKind: filtercontract.PolicyManual, DesiredRevision: 1, AppliedRevision: 1, Checksum: manualChecksum},
			{NodeID: 2, PolicyKind: filtercontract.PolicyAd, DesiredRevision: 1, AppliedRevision: 1, Checksum: testChecksum},
		},
	}
	if err := validateConvergence(status, 1, testChecksum); err != nil {
		t.Fatal(err)
	}
	status.NodeStates[3].AppliedRevision = 0
	if err := validateConvergence(status, 1, testChecksum); err == nil {
		t.Fatal("non-converged node was accepted")
	}
}

func TestValidateSafetyOverrides(t *testing.T) {
	if err := validateSafetyOverrides(2, []model.ServerConfigOverride{{ConfigKey: "forward.scan_interval"}}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"filter.engine_mode", "filter.auto_quarantine_enabled"} {
		err := validateSafetyOverrides(2, []model.ServerConfigOverride{{ConfigKey: key}})
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Fatalf("override %s error = %v", key, err)
		}
	}
}
