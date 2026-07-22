package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	filtercontract "github.com/ticket/email-filter-contract"
	"github.com/ticket/email-mgmt-system/internal/config"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/service"
	"github.com/ticket/email-mgmt-system/internal/store"
)

const actor = "ops:p4-s11-shadow"

type options struct {
	configPath         string
	action             string
	revision           uint64
	expectedAdChecksum string
	confirm            string
}

type statusOutput struct {
	Action                string                         `json:"action"`
	EngineMode            string                         `json:"engine_mode"`
	AutoQuarantineEnabled bool                           `json:"auto_quarantine_enabled"`
	ManualValidation      service.FilterPolicyValidation `json:"manual_validation"`
	AdValidation          service.FilterPolicyValidation `json:"ad_validation"`
	ActiveStates          []model.FilterActiveState      `json:"active_states"`
	NodeStates            []model.FilterNodeState        `json:"node_states"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	var opts options
	flags := flag.NewFlagSet("filter-shadow-ops", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&opts.configPath, "config", "/opt/mgmt-system/config.yaml", "management config path")
	flags.StringVar(&opts.action, "action", "status", "status, publish-shadow, enable-dual-shadow, or legacy")
	flags.Uint64Var(&opts.revision, "revision", 1, "manual and ad revision")
	flags.StringVar(&opts.expectedAdChecksum, "expected-ad-checksum", "", "required canonical ad checksum")
	flags.StringVar(&opts.confirm, "confirm", "", "explicit mutation confirmation")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return err
	}
	db, err := store.New(cfg.Database.DSN, cfg.Server.Mode)
	if err != nil {
		return err
	}
	if sqlDB, dbErr := db.DB().DB(); dbErr == nil {
		defer sqlDB.Close()
	}
	policies := service.NewFilterPolicyService(db)

	switch opts.action {
	case "status":
		return printStatus(db, policies, opts.revision, "status")
	case "publish-shadow":
		return publishShadow(db, policies, opts)
	case "enable-dual-shadow":
		return enableDualShadow(db, policies, opts)
	case "legacy":
		return returnLegacy(db, policies, opts)
	default:
		return fmt.Errorf("unsupported action %q", opts.action)
	}
}

func publishShadow(db *store.Store, policies *service.FilterPolicyService, opts options) error {
	if opts.confirm != "PUBLISH_SHADOW" {
		return errors.New("publish-shadow requires --confirm PUBLISH_SHADOW")
	}
	if db.GetConfig("filter.engine_mode", "") != "legacy" || db.GetConfigBool("filter.auto_quarantine_enabled", true) {
		return errors.New("publish-shadow requires engine_mode=legacy and auto_quarantine_enabled=false")
	}
	if err := ensureNoSafetyOverrides(db); err != nil {
		return err
	}
	manual, err := policies.GetManualRevision(opts.revision)
	if err != nil {
		return err
	}
	ad, err := policies.GetAdRevision(opts.revision)
	if err != nil {
		return err
	}
	manualValidation, err := policies.ValidateManualRevision(opts.revision)
	if err != nil {
		return err
	}
	adValidation, err := policies.ValidateAdRevision(opts.revision)
	if err != nil {
		return err
	}
	if err := validateShadowDrafts(manual, ad, manualValidation, adValidation, opts.expectedAdChecksum); err != nil {
		return err
	}
	requestID := "filter-shadow-ops-" + time.Now().UTC().Format("20060102T150405Z")
	if _, err := policies.PublishManualRevision(opts.revision, actor, requestID+"-manual"); err != nil {
		return err
	}
	if _, err := policies.PublishAdRevision(opts.revision, actor, requestID+"-ad"); err != nil {
		return err
	}
	return printStatus(db, policies, opts.revision, "publish-shadow")
}

func enableDualShadow(db *store.Store, policies *service.FilterPolicyService, opts options) error {
	if opts.confirm != "ENABLE_DUAL_SHADOW" {
		return errors.New("enable-dual-shadow requires --confirm ENABLE_DUAL_SHADOW")
	}
	if db.GetConfig("filter.engine_mode", "") != "legacy" || db.GetConfigBool("filter.auto_quarantine_enabled", true) {
		return errors.New("enable-dual-shadow requires engine_mode=legacy and auto_quarantine_enabled=false")
	}
	if err := ensureNoSafetyOverrides(db); err != nil {
		return err
	}
	status, err := policies.Status()
	if err != nil {
		return err
	}
	if err := validateConvergence(status, opts.revision, opts.expectedAdChecksum); err != nil {
		return err
	}
	if err := db.SetConfig("filter.engine_mode", "dual_shadow"); err != nil {
		return err
	}
	db.InvalidateConfigCache()
	return printStatus(db, policies, opts.revision, "enable-dual-shadow")
}

func returnLegacy(db *store.Store, policies *service.FilterPolicyService, opts options) error {
	if opts.confirm != "RETURN_LEGACY" {
		return errors.New("legacy requires --confirm RETURN_LEGACY")
	}
	if err := db.BatchSetConfigs(map[string]string{
		"filter.engine_mode":             "legacy",
		"filter.auto_quarantine_enabled": "false",
	}); err != nil {
		return err
	}
	db.InvalidateConfigCache()
	return printStatus(db, policies, opts.revision, "legacy")
}

func validateShadowDrafts(manual *service.ManualFilterRevisionView, ad *service.AdFilterRevisionView, manualValidation, adValidation service.FilterPolicyValidation, expectedAdChecksum string) error {
	if manual == nil || ad == nil {
		return errors.New("manual and ad revisions are required")
	}
	if manual.Status != "draft" && manual.Status != "published" {
		return fmt.Errorf("manual revision status %q is not publishable", manual.Status)
	}
	if ad.Status != "draft" && ad.Status != "published" {
		return fmt.Errorf("ad revision status %q is not publishable", ad.Status)
	}
	if len(manual.Rules) != 0 {
		return errors.New("manual shadow baseline must contain no rules")
	}
	if !manualValidation.Valid || !adValidation.Valid {
		return errors.New("manual and ad revisions must validate")
	}
	if len(expectedAdChecksum) != 64 || adValidation.Checksum != expectedAdChecksum {
		return fmt.Errorf("ad checksum %q does not match expected checksum", adValidation.Checksum)
	}
	if len(ad.Detectors) == 0 || len(ad.Weights) == 0 {
		return errors.New("ad shadow baseline must contain detectors and weights")
	}
	for _, detector := range ad.Detectors {
		if detector.Mode != filtercontract.ModeShadow {
			return fmt.Errorf("detector %s mode is %s", detector.LogicalID, detector.Mode)
		}
	}
	for _, composite := range ad.Composites {
		if composite.Mode != filtercontract.ModeShadow {
			return fmt.Errorf("composite %s mode is %s", composite.LogicalID, composite.Mode)
		}
	}
	return nil
}

func validateConvergence(status *service.FilterPolicyStatus, revision uint64, expectedAdChecksum string) error {
	if status == nil {
		return errors.New("policy status is required")
	}
	active := make(map[string]model.FilterActiveState, len(status.ActiveStates))
	for _, state := range status.ActiveStates {
		active[state.PolicyKind] = state
	}
	manual, manualOK := active[filtercontract.PolicyManual]
	ad, adOK := active[filtercontract.PolicyAd]
	if !manualOK || !adOK || manual.ActiveRevision != revision || ad.ActiveRevision != revision {
		return errors.New("manual and ad active revisions are not ready")
	}
	if len(expectedAdChecksum) != 64 || ad.Checksum != expectedAdChecksum {
		return errors.New("active ad checksum does not match expected checksum")
	}
	if len(status.NodeStates) == 0 {
		return errors.New("no node policy states reported")
	}
	seen := map[uint64]map[string]bool{}
	for _, state := range status.NodeStates {
		expected, ok := active[state.PolicyKind]
		if !ok {
			return fmt.Errorf("node %d reported unexpected policy %s", state.NodeID, state.PolicyKind)
		}
		if state.DesiredRevision != revision || state.AppliedRevision != revision || state.Checksum != expected.Checksum || strings.TrimSpace(state.LastError) != "" {
			return fmt.Errorf("node %d policy %s has not converged", state.NodeID, state.PolicyKind)
		}
		if seen[state.NodeID] == nil {
			seen[state.NodeID] = map[string]bool{}
		}
		seen[state.NodeID][state.PolicyKind] = true
	}
	for nodeID, kinds := range seen {
		if !kinds[filtercontract.PolicyManual] || !kinds[filtercontract.PolicyAd] {
			return fmt.Errorf("node %d is missing a policy state", nodeID)
		}
	}
	return nil
}

func ensureNoSafetyOverrides(db *store.Store) error {
	servers, err := db.ListServers()
	if err != nil {
		return err
	}
	for _, server := range servers {
		overrides, err := db.ListServerConfigOverrides(server.ID)
		if err != nil {
			return err
		}
		if err := validateSafetyOverrides(server.ID, overrides); err != nil {
			return err
		}
	}
	return nil
}

func validateSafetyOverrides(nodeID uint64, overrides []model.ServerConfigOverride) error {
	for _, override := range overrides {
		if override.ConfigKey == "filter.engine_mode" || override.ConfigKey == "filter.auto_quarantine_enabled" {
			return fmt.Errorf("node %d has safety override %s", nodeID, override.ConfigKey)
		}
	}
	return nil
}

func printStatus(db *store.Store, policies *service.FilterPolicyService, revision uint64, action string) error {
	manualValidation, err := policies.ValidateManualRevision(revision)
	if err != nil {
		return err
	}
	adValidation, err := policies.ValidateAdRevision(revision)
	if err != nil {
		return err
	}
	status, err := policies.Status()
	if err != nil {
		return err
	}
	sort.Slice(status.ActiveStates, func(i, j int) bool { return status.ActiveStates[i].PolicyKind < status.ActiveStates[j].PolicyKind })
	sort.Slice(status.NodeStates, func(i, j int) bool {
		if status.NodeStates[i].NodeID == status.NodeStates[j].NodeID {
			return status.NodeStates[i].PolicyKind < status.NodeStates[j].PolicyKind
		}
		return status.NodeStates[i].NodeID < status.NodeStates[j].NodeID
	})
	value := statusOutput{
		Action: action, EngineMode: db.GetConfig("filter.engine_mode", ""), AutoQuarantineEnabled: db.GetConfigBool("filter.auto_quarantine_enabled", true),
		ManualValidation: manualValidation, AdValidation: adValidation, ActiveStates: status.ActiveStates, NodeStates: status.NodeStates,
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
