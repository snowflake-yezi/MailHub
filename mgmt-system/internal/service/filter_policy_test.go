package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/ticket/email-filter-contract"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/store"
)

func TestEmbeddedAdSeedV1IsCompleteShadowBundle(t *testing.T) {
	var bundle filtercontract.AdBundle
	if err := filtercontract.DecodeStrict(adSeedV1, &bundle); err != nil {
		t.Fatalf("DecodeStrict(seed) error = %v", err)
	}
	checksum, err := bundle.CalculatedChecksum()
	if err != nil {
		t.Fatal(err)
	}
	bundle.Checksum = checksum
	if err := bundle.Validate(); err != nil {
		t.Fatalf("seed validation error = %v", err)
	}
	if len(bundle.Detectors) < 10 || len(bundle.Composites) == 0 {
		t.Fatalf("seed coverage is incomplete: detectors=%d composites=%d", len(bundle.Detectors), len(bundle.Composites))
	}
	for _, detector := range bundle.Detectors {
		if detector.Mode != filtercontract.ModeShadow {
			t.Fatalf("detector %s mode = %s, want shadow", detector.Symbol, detector.Mode)
		}
	}
	for _, composite := range bundle.Composites {
		if composite.Mode != filtercontract.ModeShadow {
			t.Fatalf("composite %s mode = %s, want shadow", composite.Symbol, composite.Mode)
		}
	}
}

func TestNormalizeManualRuleAppliesSafeDefaults(t *testing.T) {
	rule, err := normalizeManualRule(filtercontract.ManualRule{
		Name: "Domain allow", Action: filtercontract.ActionAllow,
		Conditions: []filtercontract.Condition{{
			Field: "header_from.domain", Operator: "eq", Value: filtercontract.StringValue("EXAMPLE.COM."),
		}},
	})
	if err != nil {
		t.Fatalf("normalizeManualRule() error = %v", err)
	}
	if rule.LogicalID == "" || rule.ScopeType != "global" || rule.Mode != "shadow" || rule.Source != "manual" {
		t.Fatalf("defaults not applied: %+v", rule)
	}
	value, ok := rule.Conditions[0].Value.String()
	if !ok || value != "example.com" || rule.Conditions[0].Position != 0 {
		t.Fatalf("condition not canonicalized: %+v", rule.Conditions[0])
	}
}

func TestAdModelRoundTripPreservesCanonicalChecksum(t *testing.T) {
	var source filtercontract.AdBundle
	if err := filtercontract.DecodeStrict(adSeedV1, &source); err != nil {
		t.Fatal(err)
	}
	modelValue := adModelFromBundle(source)
	modelValue.Revision = source.Revision
	modelValue.SchemaVersion = source.SchemaVersion
	roundTrip, err := adBundle(modelValue, true)
	if err != nil {
		t.Fatalf("adBundle() error = %v", err)
	}
	want, err := source.CalculatedChecksum()
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.Checksum != want {
		t.Fatalf("round-trip checksum = %s, want %s", roundTrip.Checksum, want)
	}
}

func TestDecisionModelPreservesOutboxEvidence(t *testing.T) {
	event := filtercontract.OutboxEvent{
		SchemaVersion: 1, Phase: "ready", NodeID: 7, Mailbox: "inbox@example.com", MessageID: "<fixture@example.com>",
		Decision: filtercontract.FilterDecision{
			SchemaVersion: 1, DecisionKey: "decision", MessageKey: "message", ManualRevision: 2, AdRevision: 4,
			ManualAction: "allow", AdAction: "tag", FinalAction: "tag", AdScore: 2500,
			Reasons: []filtercontract.DecisionReason{}, AdSymbols: []filtercontract.AdSymbolResult{},
			ShadowResults: []filtercontract.ShadowResult{}, ParseWarnings: []string{"warning"}, EvaluatedAt: time.Unix(100, 0).UTC(),
		},
		Result: &filtercontract.ProcessingResult{Status: "succeeded", AttemptedAction: "tag", ActualAction: "allow"},
	}
	decision, err := decisionModel(event, 11)
	if err != nil {
		t.Fatal(err)
	}
	if decision.MailboxAccountID != 11 || decision.NodeID != 7 || decision.MessageID != event.MessageID || decision.AdScoreMilli != 2500 {
		t.Fatalf("decision = %#v", decision)
	}
	var warnings []string
	if err := json.Unmarshal([]byte(decision.ParseWarningsText), &warnings); err != nil || len(warnings) != 1 {
		t.Fatalf("warnings = %#v / %v", warnings, err)
	}
}

func TestQuarantineModelUsesGlobalRetention(t *testing.T) {
	evaluatedAt := time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC)
	event := filtercontract.OutboxEvent{
		Decision: filtercontract.FilterDecision{EvaluatedAt: evaluatedAt},
		Result: &filtercontract.ProcessingResult{
			Status: "succeeded", ActualAction: filtercontract.ActionQuarantine,
			QuarantineKey: "quarantine-key", OriginalMaildirKey: "maildir-key",
		},
	}
	quarantine, err := quarantineModel(event, 45)
	if err != nil {
		t.Fatal(err)
	}
	want := evaluatedAt.Add(45 * 24 * time.Hour)
	if !quarantine.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", quarantine.ExpiresAt, want)
	}
}

func TestDecisionViewDecodesStructuredEvidence(t *testing.T) {
	record := store.FilterDecisionRecord{
		FilterDecision: model.FilterDecision{
			ID: 1, DecisionKey: "decision", MessageKey: "message", NodeID: 7,
			ManualAction: "allow", AdAction: "tag", FinalAction: "tag", AdScoreMilli: 2500,
			ReasonsText: "[null]", AdSymbolsText: "[null]", ShadowResultsText: "[null]", ParseWarningsText: "[null]",
			EvaluatedAt: time.Unix(100, 0).UTC(),
		},
		Mailbox: "inbox@example.com",
	}
	view, err := decisionView(record)
	if err != nil {
		t.Fatal(err)
	}
	if view.Mailbox != record.Mailbox || view.AdScore != 2500 || len(view.Reasons) != 1 ||
		len(view.AdSymbols) != 1 || len(view.ShadowResults) != 1 || len(view.ParseWarnings) != 1 {
		t.Fatalf("view = %#v", view)
	}
}

func TestFilterPolicyServiceMariaDBWorkflow(t *testing.T) {
	sourceDSN := os.Getenv("MAILHUB_TEST_MYSQL_DSN")
	if sourceDSN == "" {
		t.Skip("MAILHUB_TEST_MYSQL_DSN is not set")
	}
	config, err := drivermysql.ParseDSN(sourceDSN)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	adminConfig := *config
	adminConfig.DBName = "information_schema"
	adminDB, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	defer adminDB.Close()
	if err := adminDB.Ping(); err != nil {
		t.Fatalf("ping MariaDB: %v", err)
	}
	var version string
	if err := adminDB.QueryRow("SELECT VERSION()").Scan(&version); err != nil {
		t.Fatalf("read MariaDB version: %v", err)
	}
	t.Logf("database version: %s", version)

	databaseName := fmt.Sprintf("mailhub_filter_s4_%d_%d", os.Getpid(), time.Now().UnixNano())
	quotedName := "`" + databaseName + "`"
	if _, err := adminDB.Exec("CREATE DATABASE " + quotedName + " CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	defer func() {
		if _, err := adminDB.Exec("DROP DATABASE " + quotedName); err != nil {
			t.Errorf("drop test database: %v", err)
		}
	}()

	targetConfig := *config
	targetConfig.DBName = databaseName
	policyStore, err := store.New(targetConfig.FormatDSN(), "release")
	if err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	server := &model.MailServer{Name: "policy-node", APIHost: "127.0.0.1:18081", SMTPHost: "127.0.0.1", IMAPHost: "127.0.0.1", Status: "healthy"}
	if err := policyStore.CreateServer(server); err != nil {
		t.Fatalf("create policy node: %v", err)
	}

	policyService := NewFilterPolicyService(policyStore)
	draft, err := policyService.CreateAdDraft(nil, AdSeedV1, "admin", "create-seed")
	if err != nil {
		t.Fatalf("create seed draft: %v", err)
	}
	validation, err := policyService.ValidateAdRevision(draft.Revision)
	if err != nil || !validation.Valid || len(validation.Checksum) != 64 {
		t.Fatalf("seed validation = %+v, error = %v", validation, err)
	}

	var wait sync.WaitGroup
	errorsByAttempt := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func(attempt int) {
			defer wait.Done()
			_, publishErr := policyService.PublishAdRevision(draft.Revision, "admin", fmt.Sprintf("publish-%d", attempt))
			errorsByAttempt <- publishErr
		}(i)
	}
	wait.Wait()
	close(errorsByAttempt)
	for publishErr := range errorsByAttempt {
		if publishErr != nil {
			t.Fatalf("concurrent publish error: %v", publishErr)
		}
	}
	retried, err := policyService.PublishAdRevision(draft.Revision, "external-app:7:ticket", "same-publish-key")
	if err != nil || retried.Revision != draft.Revision || retried.Status != "published" {
		t.Fatalf("idempotent publish retry = %+v, error = %v", retried, err)
	}

	status, err := policyService.Status()
	if err != nil {
		t.Fatalf("read policy status: %v", err)
	}
	if len(status.ActiveStates) != 1 || status.ActiveStates[0].PolicyKind != "ad" || status.ActiveStates[0].ActiveRevision != draft.Revision {
		t.Fatalf("unexpected active state: %+v", status.ActiveStates)
	}
	if len(status.NodeStates) != 1 || status.NodeStates[0].DesiredRevision != draft.Revision || status.NodeStates[0].AppliedRevision != 0 {
		t.Fatalf("unexpected node state: %+v", status.NodeStates)
	}
	bundleValue, err := policyService.ActiveBundle("ad")
	if err != nil {
		t.Fatalf("load active bundle: %v", err)
	}
	bundle := bundleValue.(filtercontract.AdBundle)
	if bundle.Checksum != validation.Checksum || bundle.Revision != draft.Revision {
		t.Fatalf("active bundle mismatch: revision=%d checksum=%s", bundle.Revision, bundle.Checksum)
	}
	if _, err := policyService.PutAdThresholds(draft.Revision, 5_000, 8_000, "admin", "immutable"); !errors.Is(err, ErrFilterPolicyDraftRequired) {
		t.Fatalf("published update error = %v, want draft-required", err)
	}
	clone, err := policyService.CreateAdDraft(&draft.Revision, "", "admin", "clone")
	if err != nil || clone.Revision <= draft.Revision || clone.Status != "draft" {
		t.Fatalf("clone = %+v, error = %v", clone, err)
	}

	manualDraft, err := policyService.CreateManualDraft(nil, "admin", "create-manual")
	if err != nil {
		t.Fatalf("create manual draft: %v", err)
	}
	manualDraft, err = policyService.AddManualRule(manualDraft.Revision, filtercontract.ManualRule{
		Name: "Allow example domain", Action: filtercontract.ActionAllow,
		Conditions: []filtercontract.Condition{{
			Field: "header_from.domain", Operator: "eq", Value: filtercontract.StringValue("example.test"),
		}},
	}, "admin", "add-manual-rule")
	if err != nil {
		t.Fatalf("add manual rule: %v", err)
	}
	manualValidation, err := policyService.ValidateManualRevision(manualDraft.Revision)
	if err != nil || !manualValidation.Valid {
		t.Fatalf("manual validation = %+v, error = %v", manualValidation, err)
	}
	if _, err := policyService.PublishManualRevision(manualDraft.Revision, "admin", "publish-manual"); err != nil {
		t.Fatalf("publish manual revision: %v", err)
	}
	manualBundleValue, err := policyService.ActiveBundle("manual")
	if err != nil {
		t.Fatalf("load manual bundle: %v", err)
	}
	manualBundle := manualBundleValue.(filtercontract.ManualBundle)
	if manualBundle.Revision != manualDraft.Revision || manualBundle.Checksum != manualValidation.Checksum || len(manualBundle.Rules) != 1 {
		t.Fatalf("manual active bundle mismatch: %+v", manualBundle)
	}
}
