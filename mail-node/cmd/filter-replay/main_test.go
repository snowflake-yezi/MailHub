package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	filtercontract "github.com/ticket/email-filter-contract"
)

func TestReplayIsByteForByteDeterministic(t *testing.T) {
	directory := t.TempDir()
	emlPath := filepath.Join(directory, "fixture.eml")
	if err := os.WriteFile(emlPath, []byte("From: sender@example.com\r\nTo: inbox@example.com\r\nSubject: sale\r\nMessage-ID: <fixture@example.com>\r\n\r\nsale body"), 0600); err != nil {
		t.Fatal(err)
	}
	bundle := filtercontract.AdBundle{
		SchemaVersion: 1, PolicyKind: "ad", Revision: 2, TagThreshold: 1000, QuarantineThreshold: 5000,
		Detectors:  []filtercontract.AdDetector{{LogicalID: "sale", Name: "Sale", Symbol: "AD_SALE", Mode: "enforce", Source: "local", Conditions: []filtercontract.Condition{{Field: "subject", Operator: "contains", Value: filtercontract.StringValue("sale"), Position: 0}}}},
		Composites: []filtercontract.AdComposite{}, Weights: []filtercontract.SymbolWeight{{Symbol: "AD_SALE", Score: 2000}},
	}
	bundle.Checksum, _ = bundle.CalculatedChecksum()
	bundleData, _ := bundle.CanonicalJSON()
	bundlePath := filepath.Join(directory, "ad.json")
	if err := os.WriteFile(bundlePath, bundleData, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--eml", emlPath, "--mailbox", "inbox@example.com", "--server-id", "7", "--ad-bundle", bundlePath}
	var first, second bytes.Buffer
	if err := run(args, &first); err != nil {
		t.Fatal(err)
	}
	if err := run(args, &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatalf("replay differs\nfirst: %s\nsecond: %s", first.Bytes(), second.Bytes())
	}
}
