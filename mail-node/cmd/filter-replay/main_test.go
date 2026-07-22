package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestManifestReportProducesCalibrationEvidenceWithoutMessageContent(t *testing.T) {
	directory := t.TempDir()
	writeReplayEML(t, filepath.Join(directory, "ad.eml"), "Mon, 01 Jun 2026 10:00:00 +0000", "promo@ads.example", "sale", "private sale body")
	writeReplayEML(t, filepath.Join(directory, "transactional.eml"), "Wed, 01 Jul 2026 10:00:00 +0000", "notice@txn.example", "invoice", "private invoice body")
	writeReplayEML(t, filepath.Join(directory, "other.eml"), "Sat, 01 Aug 2026 10:00:00 +0000", "person@other.example", "hello", "private normal body")

	bundle := filtercontract.AdBundle{
		SchemaVersion: 1, PolicyKind: "ad", Revision: 12, TagThreshold: 1000, QuarantineThreshold: 5000,
		Detectors: []filtercontract.AdDetector{{
			LogicalID: "sale", Name: "Sale", Symbol: "AD_SALE", Mode: "shadow", Source: "local",
			Conditions: []filtercontract.Condition{{Field: "subject", Operator: "contains", Value: filtercontract.StringValue("sale"), Position: 0}},
		}},
		Composites: []filtercontract.AdComposite{}, Weights: []filtercontract.SymbolWeight{{Symbol: "AD_SALE", Score: 6000}},
	}
	bundle.Checksum, _ = bundle.CalculatedChecksum()
	bundleData, _ := bundle.CanonicalJSON()
	bundlePath := filepath.Join(directory, "ad.json")
	if err := os.WriteFile(bundlePath, bundleData, 0600); err != nil {
		t.Fatal(err)
	}
	manifest := replayManifest{SchemaVersion: 1, GuidelineVersion: "v0.1", Samples: []replaySample{
		{SampleID: "sample-ad", EML: "ad.eml", Mailbox: "inbox@example.com", Label: "ad", Split: "training", ReceivedAt: "2026-06-01T10:00:00Z"},
		{SampleID: "sample-transactional", EML: "transactional.eml", Mailbox: "inbox@example.com", Label: "transactional", Split: "calibration", ReceivedAt: "2026-07-01T10:00:00Z"},
		{SampleID: "sample-other", EML: "other.eml", Mailbox: "inbox@example.com", Label: "other", Split: "validation", ReceivedAt: "2026-08-01T10:00:00Z"},
		{SampleID: "sample-uncertain", EML: "must-not-be-read.eml", Mailbox: "inbox@example.com", Label: "uncertain", Split: "validation", ReceivedAt: "2026-08-02T10:00:00Z"},
	}}
	manifestData, _ := json.Marshal(manifest)
	manifestPath := filepath.Join(directory, "manifest.json")
	if err := os.WriteFile(manifestPath, manifestData, 0600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	args := []string{"--manifest", manifestPath, "--server-id", "7", "--ad-bundle", bundlePath}
	if err := run(args, &output); err != nil {
		t.Fatal(err)
	}
	var repeated bytes.Buffer
	if err := run(args, &repeated); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), repeated.Bytes()) {
		t.Fatalf("manifest report is not deterministic\nfirst: %s\nsecond: %s", output.Bytes(), repeated.Bytes())
	}
	var report calibrationReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.IncludedSamples != 3 || report.ExcludedUncertain != 1 || report.AdRevision != 12 || report.AdChecksum != bundle.Checksum {
		t.Fatalf("report summary = %+v", report)
	}
	if report.LabelActionMatrix["ad"]["quarantine"] != 1 || report.LabelActionMatrix["transactional"]["allow"] != 1 || report.LabelActionMatrix["other"]["allow"] != 1 {
		t.Fatalf("matrix = %+v", report.LabelActionMatrix)
	}
	if !report.TimeIsolationPassed || !report.DomainIsolationPassed || len(report.WouldQuarantine) != 1 || report.WouldQuarantine[0].SampleID != "sample-ad" {
		t.Fatalf("isolation/quarantine evidence = time:%v domain:%v quarantine:%+v", report.TimeIsolationPassed, report.DomainIsolationPassed, report.WouldQuarantine)
	}
	for _, sensitive := range []string{"private sale body", "ads.example", filepath.Join(directory, "ad.eml")} {
		if strings.Contains(output.String(), sensitive) {
			t.Fatalf("report leaked %q: %s", sensitive, output.String())
		}
	}
}

func TestFinalizeCalibrationReportDetectsDomainAndTimeLeakage(t *testing.T) {
	report := newCalibrationReport(replayManifest{GuidelineVersion: "v0.1"}, filtercontract.AdBundle{})
	day := func(value string) time.Time {
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	ranges := map[string]*splitRange{
		"training":    {count: 1, min: day("2026-07-02"), max: day("2026-07-02")},
		"calibration": {count: 1, min: day("2026-07-01"), max: day("2026-07-01")},
		"validation":  {count: 1, min: day("2026-08-01"), max: day("2026-08-01")},
	}
	finalizeCalibrationReport(&report, ranges, map[string]map[string]struct{}{
		"domain-hash": {"training": {}, "validation": {}},
	}, map[string]*domainBreakdown{})
	if report.TimeIsolationPassed || report.DomainIsolationPassed || len(report.DomainSplitOverlaps) != 1 {
		t.Fatalf("leakage was not reported: %+v", report)
	}
}

func writeReplayEML(t *testing.T, path, date, from, subject, body string) {
	t.Helper()
	content := "Date: " + date + "\r\nFrom: " + from + "\r\nTo: inbox@example.com\r\nSubject: " + subject + "\r\nMessage-ID: <" + filepath.Base(path) + "@example.com>\r\n\r\n" + body
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
