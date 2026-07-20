package filtercontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jhillyerd/enmime"
)

func TestCanonicalContractFixtures(t *testing.T) {
	tests := []struct {
		name      string
		file      string
		newValue  func() any
		canonical func(any) ([]byte, error)
		validate  func(any) error
	}{
		{
			name: "manual bundle", file: "manual-bundle.canonical.json",
			newValue:  func() any { return &ManualBundle{} },
			canonical: func(value any) ([]byte, error) { return value.(*ManualBundle).CanonicalJSON() },
			validate:  func(value any) error { return value.(*ManualBundle).Validate() },
		},
		{
			name: "ad bundle", file: "ad-bundle.canonical.json",
			newValue:  func() any { return &AdBundle{} },
			canonical: func(value any) ([]byte, error) { return value.(*AdBundle).CanonicalJSON() },
			validate:  func(value any) error { return value.(*AdBundle).Validate() },
		},
		{
			name: "decision", file: "decision.canonical.json",
			newValue:  func() any { return &FilterDecision{} },
			canonical: func(value any) ([]byte, error) { return value.(*FilterDecision).CanonicalJSON() },
			validate:  func(value any) error { return value.(*FilterDecision).Validate() },
		},
		{
			name: "ready outbox", file: "outbox-ready.canonical.json",
			newValue:  func() any { return &OutboxEvent{} },
			canonical: func(value any) ([]byte, error) { return value.(*OutboxEvent).CanonicalJSON() },
			validate:  func(value any) error { return value.(*OutboxEvent).Validate() },
		},
		{
			name: "release receipt", file: "release-receipt.canonical.json",
			newValue:  func() any { return &ReleaseReceipt{} },
			canonical: func(value any) ([]byte, error) { return value.(*ReleaseReceipt).CanonicalJSON() },
			validate:  func(value any) error { return value.(*ReleaseReceipt).Validate() },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := readContractFixture(t, test.file)
			value := test.newValue()
			if err := DecodeStrict(data, value); err != nil {
				t.Fatalf("DecodeStrict() error = %v", err)
			}
			if err := test.validate(value); err != nil {
				if bundle, ok := value.(*ManualBundle); ok {
					checksum, _ := bundle.CalculatedChecksum()
					t.Fatalf("Validate() error = %v; calculated checksum = %s", err, checksum)
				}
				if bundle, ok := value.(*AdBundle); ok {
					checksum, _ := bundle.CalculatedChecksum()
					t.Fatalf("Validate() error = %v; calculated checksum = %s", err, checksum)
				}
				t.Fatalf("Validate() error = %v", err)
			}
			got, err := test.canonical(value)
			if err != nil {
				t.Fatalf("CanonicalJSON() error = %v", err)
			}
			if want := bytes.TrimSpace(data); !bytes.Equal(got, want) {
				t.Fatalf("canonical bytes differ\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

func TestScoreCanonicalization(t *testing.T) {
	tests := map[string]string{
		"0":      "0",
		"3.0":    "3",
		"-2.750": "-2.75",
		"0.001":  "0.001",
		"100":    "100",
	}
	for input, want := range tests {
		score, err := ParseScore(input)
		if err != nil {
			t.Fatalf("ParseScore(%q) error = %v", input, err)
		}
		if got := score.String(); got != want {
			t.Fatalf("ParseScore(%q).String() = %q, want %q", input, got, want)
		}
	}
	for _, input := range []string{"", "+1", "01", "1.0000", "1e3", "NaN", " 1"} {
		if _, err := ParseScore(input); err == nil {
			t.Fatalf("ParseScore(%q) accepted invalid input", input)
		}
	}
}

func TestDecodeStrictRejectsUnknownFields(t *testing.T) {
	var receipt ReleaseReceipt
	err := DecodeStrict([]byte(`{"schema_version":1,"unknown":true}`), &receipt)
	if err == nil {
		t.Fatal("DecodeStrict() accepted an unknown field")
	}
	var contractErr *ContractError
	if !errors.As(err, &contractErr) || contractErr.Code != ErrorInvalidJSON {
		t.Fatalf("DecodeStrict() error = %v", err)
	}
}

func TestMessageKeyIgnoresMaildirFlags(t *testing.T) {
	plain := MessageKey(7, "inbox@tenant.example", "maildir-name", 123)
	flagged := MessageKey(7, "inbox@tenant.example", "maildir-name:2,S", 123)
	if plain != flagged {
		t.Fatalf("MessageKey changed after flags: %s != %s", plain, flagged)
	}
	if changed := MessageKey(7, "inbox@tenant.example", "maildir-name", 124); changed == plain {
		t.Fatal("MessageKey did not change with message size")
	}
}

func TestGoldenCasesCoverFixtureBaseline(t *testing.T) {
	data := readContractFixture(t, "golden-cases.json")
	var cases []GoldenCase
	if err := DecodeStrict(data, &cases); err != nil {
		t.Fatalf("DecodeStrict(golden cases) error = %v", err)
	}
	if len(cases) != 8 {
		t.Fatalf("golden case count = %d, want 8", len(cases))
	}

	wantCases := map[string]bool{
		"ad-promotion": false, "transactional-notice": false,
		"other-normal": false, "parse-failure": false,
		"duplicate-headers": false, "multiple-urls": false,
		"inline-image": false, "large-attachment-boundary": false,
	}
	labels := map[string]bool{"ad": false, "transactional": false, "other": false, "uncertain": false}
	for _, golden := range cases {
		if golden.SchemaVersion != SchemaVersionV1 {
			t.Fatalf("case %s schema_version = %d", golden.CaseID, golden.SchemaVersion)
		}
		if _, ok := wantCases[golden.CaseID]; !ok {
			t.Fatalf("unexpected case_id %q", golden.CaseID)
		}
		if wantCases[golden.CaseID] {
			t.Fatalf("duplicate case_id %q", golden.CaseID)
		}
		wantCases[golden.CaseID] = true
		if _, ok := labels[golden.Label]; !ok {
			t.Fatalf("case %s has invalid label %q", golden.CaseID, golden.Label)
		}
		labels[golden.Label] = true

		info, err := os.Stat(contractPath("eml", golden.Fixture))
		if err != nil {
			t.Fatalf("case %s fixture: %v", golden.CaseID, err)
		}
		if golden.Features.SizeBytes != info.Size() {
			t.Fatalf("case %s size = %d, fixture size = %d", golden.CaseID, golden.Features.SizeBytes, info.Size())
		}
		wantKey := MessageKey(golden.Features.ServerID, golden.Features.Mailbox, golden.MaildirUniqueName, info.Size())
		if golden.Features.MessageKey != wantKey || golden.Decision.MessageKey != wantKey {
			t.Fatalf("case %s message key mismatch: want %s", golden.CaseID, wantKey)
		}
		if err := golden.Decision.Validate(); err != nil {
			t.Fatalf("case %s decision validation: %v", golden.CaseID, err)
		}
	}
	for label, covered := range labels {
		if !covered {
			t.Fatalf("golden labels do not cover %q", label)
		}
	}
}

func TestLargeAttachmentFixtureHasExactBoundaryPayload(t *testing.T) {
	file, err := os.Open(contractPath("eml", "large-attachment-boundary.eml"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	envelope, err := enmime.ReadEnvelope(file)
	if err != nil {
		t.Fatalf("ReadEnvelope() error = %v", err)
	}
	if len(envelope.Attachments) != 1 {
		t.Fatalf("attachment count = %d, want 1", len(envelope.Attachments))
	}
	if got := len(envelope.Attachments[0].Content); got != 4096 {
		t.Fatalf("attachment bytes = %d, want 4096", got)
	}
}

func TestContractSchemaIsVersionedAndClosed(t *testing.T) {
	data := readContractFixture(t, "contract.schema.json")
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("schema JSON error = %v", err)
	}
	definitions, ok := schema["$defs"].(map[string]any)
	if !ok {
		t.Fatal("schema has no $defs object")
	}
	for _, name := range []string{"manualBundle", "adBundle", "filterDecision", "outboxEvent", "releaseReceipt"} {
		definition, ok := definitions[name].(map[string]any)
		if !ok {
			t.Fatalf("schema definition %q is missing", name)
		}
		if additional, exists := definition["additionalProperties"]; !exists || additional != false {
			t.Fatalf("schema definition %q must reject unknown fields", name)
		}
	}
}

func TestCanonicalBundleOrderingIsDeterministic(t *testing.T) {
	var bundle AdBundle
	if err := DecodeStrict(readContractFixture(t, "ad-bundle.canonical.json"), &bundle); err != nil {
		t.Fatal(err)
	}
	want, err := bundle.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	reverse(bundle.Detectors)
	reverse(bundle.Weights)
	reverse(bundle.Composites[0].AllOf)
	reverse(bundle.Composites[0].AnyOf)
	got, err := bundle.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("canonical output changed with unordered input\n got: %s\nwant: %s", got, want)
	}
}

func TestContractFixturesContainNoCredentialMaterial(t *testing.T) {
	for _, pattern := range []string{"*.json", "eml/*.eml"} {
		files, err := filepath.Glob(contractPath(pattern))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			lower := strings.ToLower(string(data))
			for _, forbidden := range []string{"authorization: bearer", "x-internal-token:", "password=", "api_key="} {
				if strings.Contains(lower, forbidden) {
					t.Fatalf("fixture %s contains forbidden credential marker %q", file, forbidden)
				}
			}
		}
	}
}

func readContractFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(contractPath(name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func contractPath(parts ...string) string {
	base := []string{"..", "..", "..", "docs", "filter-contract", "v1"}
	return filepath.Join(append(base, parts...)...)
}

func reverse[T any](values []T) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}
