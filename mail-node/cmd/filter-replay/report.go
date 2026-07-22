package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	filtercontract "github.com/ticket/email-filter-contract"
	"github.com/ticket/email-mail-node/internal/filterdecision"
	"github.com/ticket/email-mail-node/internal/mailparse"
)

const thresholdWindow = filtercontract.Score(1000)

type replayManifest struct {
	SchemaVersion    int            `json:"schema_version"`
	GuidelineVersion string         `json:"guideline_version"`
	Samples          []replaySample `json:"samples"`
}

type replaySample struct {
	SampleID   string `json:"sample_id"`
	EML        string `json:"eml"`
	Mailbox    string `json:"mailbox"`
	Label      string `json:"label"`
	Split      string `json:"split"`
	ReceivedAt string `json:"received_at"`
}

type calibrationReport struct {
	SchemaVersion         int                       `json:"schema_version"`
	GuidelineVersion      string                    `json:"guideline_version"`
	AdRevision            uint64                    `json:"ad_revision"`
	AdChecksum            string                    `json:"ad_checksum"`
	TagThreshold          filtercontract.Score      `json:"tag_threshold"`
	QuarantineThreshold   filtercontract.Score      `json:"quarantine_threshold"`
	IncludedSamples       int                       `json:"included_samples"`
	ExcludedUncertain     int                       `json:"excluded_uncertain"`
	LabelCounts           map[string]int            `json:"label_counts"`
	LabelActionMatrix     map[string]map[string]int `json:"label_action_matrix"`
	SplitSummaries        []splitSummary            `json:"split_summaries"`
	TimeIsolationPassed   bool                      `json:"time_isolation_passed"`
	DomainIsolationPassed bool                      `json:"domain_isolation_passed"`
	DomainSplitOverlaps   []domainSplitOverlap      `json:"domain_split_overlaps"`
	DomainBreakdown       []domainBreakdown         `json:"domain_breakdown"`
	NearThreshold         []thresholdSample         `json:"near_threshold"`
	WouldQuarantine       []candidateSample         `json:"would_quarantine"`
}

type splitSummary struct {
	Split   string `json:"split"`
	Samples int    `json:"samples"`
	FirstAt string `json:"first_at,omitempty"`
	LastAt  string `json:"last_at,omitempty"`
}

type domainSplitOverlap struct {
	SenderDomainHash string   `json:"sender_domain_hash"`
	Splits           []string `json:"splits"`
}

type domainBreakdown struct {
	SenderDomainHash string         `json:"sender_domain_hash"`
	Samples          int            `json:"samples"`
	LabelCounts      map[string]int `json:"label_counts"`
	ActionCounts     map[string]int `json:"action_counts"`
}

type candidateSample struct {
	SampleID         string               `json:"sample_id"`
	Label            string               `json:"label"`
	Split            string               `json:"split"`
	SenderDomainHash string               `json:"sender_domain_hash"`
	Score            filtercontract.Score `json:"score"`
	Action           string               `json:"action"`
}

type thresholdSample struct {
	candidateSample
	Threshold string               `json:"threshold"`
	Distance  filtercontract.Score `json:"distance"`
}

type splitRange struct {
	count int
	min   time.Time
	max   time.Time
}

func runManifest(engine *filterdecision.Engine, bundle filtercontract.AdBundle, manifestPath string, serverID uint64, output io.Writer) error {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var manifest replayManifest
	if err := filtercontract.DecodeStrict(data, &manifest); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	if err := validateReplayManifest(manifest); err != nil {
		return err
	}
	report := newCalibrationReport(manifest, bundle)
	baseDir := filepath.Dir(manifestPath)
	domainSplits := map[string]map[string]struct{}{}
	domains := map[string]*domainBreakdown{}
	splitRanges := map[string]*splitRange{}

	for _, sample := range manifest.Samples {
		if sample.Label == "uncertain" {
			report.ExcludedUncertain++
			continue
		}
		result, err := evaluateReplaySample(engine, sample, baseDir, serverID)
		if err != nil {
			return fmt.Errorf("sample %s: %w", sample.SampleID, err)
		}
		report.IncludedSamples++
		report.LabelCounts[sample.Label]++
		report.LabelActionMatrix[sample.Label][result.Action]++
		updateSplitRange(splitRanges, sample.Split, result.EvaluatedAt)
		if domainSplits[result.DomainHash] == nil {
			domainSplits[result.DomainHash] = map[string]struct{}{}
		}
		domainSplits[result.DomainHash][sample.Split] = struct{}{}
		if domains[result.DomainHash] == nil {
			domains[result.DomainHash] = &domainBreakdown{SenderDomainHash: result.DomainHash, LabelCounts: labelCountMap(), ActionCounts: actionCountMap()}
		}
		domain := domains[result.DomainHash]
		domain.Samples++
		domain.LabelCounts[sample.Label]++
		domain.ActionCounts[result.Action]++

		candidate := candidateSample{SampleID: sample.SampleID, Label: sample.Label, Split: sample.Split, SenderDomainHash: result.DomainHash, Score: result.Score, Action: result.Action}
		if result.Action == filtercontract.ActionQuarantine {
			report.WouldQuarantine = append(report.WouldQuarantine, candidate)
		}
		for _, threshold := range []struct {
			name  string
			score filtercontract.Score
		}{{"tag", bundle.TagThreshold}, {"quarantine", bundle.QuarantineThreshold}} {
			distance := absoluteScore(result.Score - threshold.score)
			if distance <= thresholdWindow {
				report.NearThreshold = append(report.NearThreshold, thresholdSample{candidateSample: candidate, Threshold: threshold.name, Distance: distance})
			}
		}
	}

	finalizeCalibrationReport(&report, splitRanges, domainSplits, domains)
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

type replayResult struct {
	Score       filtercontract.Score
	Action      string
	DomainHash  string
	EvaluatedAt time.Time
}

func evaluateReplaySample(engine *filterdecision.Engine, sample replaySample, baseDir string, serverID uint64) (replayResult, error) {
	path := sample.EML
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	parsed, err := mailparse.ParseFile(path, mailparse.Options{
		Mailbox: sample.Mailbox, MaildirBase: filepath.Dir(filepath.Dir(path)),
		MaildirUniqueName: filepath.Base(path), ServerID: serverID,
	})
	if err != nil {
		return replayResult{}, err
	}
	evaluatedAt, err := time.Parse(time.RFC3339, sample.ReceivedAt)
	if err != nil {
		return replayResult{}, fmt.Errorf("received_at: %w", err)
	}
	evaluatedAt = evaluatedAt.UTC()
	decision, err := engine.Evaluate(parsed.Features, filterdecision.Options{EvaluatedAt: evaluatedAt})
	if err != nil {
		return replayResult{}, err
	}
	score, action := candidateAdResult(decision)
	return replayResult{Score: score, Action: action, DomainHash: hashDomain(parsed.Features.HeaderFrom.Domain), EvaluatedAt: evaluatedAt}, nil
}

func candidateAdResult(decision filtercontract.FilterDecision) (filtercontract.Score, string) {
	for _, shadow := range decision.ShadowResults {
		if shadow.PolicyKind == filtercontract.PolicyAd && shadow.ProducerLogicalID == "shadow-graph" {
			return shadow.Score, shadow.Action
		}
	}
	return decision.AdScore, decision.AdAction
}

func validateReplayManifest(manifest replayManifest) error {
	if manifest.SchemaVersion != 1 || strings.TrimSpace(manifest.GuidelineVersion) == "" || len(manifest.Samples) == 0 {
		return fmt.Errorf("manifest requires schema_version=1, guideline_version and samples")
	}
	seen := map[string]struct{}{}
	for i, sample := range manifest.Samples {
		if strings.TrimSpace(sample.SampleID) == "" || strings.TrimSpace(sample.EML) == "" || strings.TrimSpace(sample.Mailbox) == "" || strings.TrimSpace(sample.ReceivedAt) == "" {
			return fmt.Errorf("manifest sample %d requires sample_id, eml, mailbox and received_at", i)
		}
		if _, exists := seen[sample.SampleID]; exists {
			return fmt.Errorf("manifest sample_id %q is duplicated", sample.SampleID)
		}
		seen[sample.SampleID] = struct{}{}
		if !contains([]string{"ad", "transactional", "other", "uncertain"}, sample.Label) {
			return fmt.Errorf("manifest sample %s has invalid label %q", sample.SampleID, sample.Label)
		}
		if !contains([]string{"training", "calibration", "validation"}, sample.Split) {
			return fmt.Errorf("manifest sample %s has invalid split %q", sample.SampleID, sample.Split)
		}
		if _, err := time.Parse(time.RFC3339, sample.ReceivedAt); err != nil {
			return fmt.Errorf("manifest sample %s has invalid received_at: %w", sample.SampleID, err)
		}
	}
	return nil
}

func newCalibrationReport(manifest replayManifest, bundle filtercontract.AdBundle) calibrationReport {
	return calibrationReport{
		SchemaVersion: 1, GuidelineVersion: manifest.GuidelineVersion, AdRevision: bundle.Revision, AdChecksum: bundle.Checksum,
		TagThreshold: bundle.TagThreshold, QuarantineThreshold: bundle.QuarantineThreshold,
		LabelCounts: labelCountMap(), LabelActionMatrix: map[string]map[string]int{
			"ad": actionCountMap(), "transactional": actionCountMap(), "other": actionCountMap(),
		},
		SplitSummaries: []splitSummary{}, DomainSplitOverlaps: []domainSplitOverlap{}, DomainBreakdown: []domainBreakdown{},
		NearThreshold: []thresholdSample{}, WouldQuarantine: []candidateSample{},
	}
}

func finalizeCalibrationReport(report *calibrationReport, ranges map[string]*splitRange, domainSplits map[string]map[string]struct{}, domains map[string]*domainBreakdown) {
	for _, split := range []string{"training", "calibration", "validation"} {
		value := ranges[split]
		summary := splitSummary{Split: split}
		if value != nil {
			summary.Samples, summary.FirstAt, summary.LastAt = value.count, value.min.Format(time.RFC3339), value.max.Format(time.RFC3339)
		}
		report.SplitSummaries = append(report.SplitSummaries, summary)
	}
	report.TimeIsolationPassed = orderedSplitRanges(ranges)
	for hash, splitSet := range domainSplits {
		if len(splitSet) > 1 {
			splits := mapKeys(splitSet)
			report.DomainSplitOverlaps = append(report.DomainSplitOverlaps, domainSplitOverlap{SenderDomainHash: hash, Splits: splits})
		}
	}
	sort.Slice(report.DomainSplitOverlaps, func(i, j int) bool {
		return report.DomainSplitOverlaps[i].SenderDomainHash < report.DomainSplitOverlaps[j].SenderDomainHash
	})
	report.DomainIsolationPassed = len(report.DomainSplitOverlaps) == 0
	for _, value := range domains {
		report.DomainBreakdown = append(report.DomainBreakdown, *value)
	}
	sort.Slice(report.DomainBreakdown, func(i, j int) bool {
		return report.DomainBreakdown[i].SenderDomainHash < report.DomainBreakdown[j].SenderDomainHash
	})
	sort.Slice(report.NearThreshold, func(i, j int) bool {
		if report.NearThreshold[i].SampleID == report.NearThreshold[j].SampleID {
			return report.NearThreshold[i].Threshold < report.NearThreshold[j].Threshold
		}
		return report.NearThreshold[i].SampleID < report.NearThreshold[j].SampleID
	})
	sort.Slice(report.WouldQuarantine, func(i, j int) bool { return report.WouldQuarantine[i].SampleID < report.WouldQuarantine[j].SampleID })
}

func updateSplitRange(values map[string]*splitRange, split string, value time.Time) {
	current := values[split]
	if current == nil {
		values[split] = &splitRange{count: 1, min: value, max: value}
		return
	}
	current.count++
	if value.Before(current.min) {
		current.min = value
	}
	if value.After(current.max) {
		current.max = value
	}
}

func orderedSplitRanges(values map[string]*splitRange) bool {
	training, calibration, validation := values["training"], values["calibration"], values["validation"]
	return training != nil && calibration != nil && validation != nil && training.max.Before(calibration.min) && calibration.max.Before(validation.min)
}

func hashDomain(value string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(value))))
	return hex.EncodeToString(sum[:8])
}

func absoluteScore(value filtercontract.Score) filtercontract.Score {
	if value < 0 {
		return -value
	}
	return value
}

func labelCountMap() map[string]int {
	return map[string]int{"ad": 0, "transactional": 0, "other": 0}
}

func actionCountMap() map[string]int {
	return map[string]int{filtercontract.ActionAllow: 0, filtercontract.ActionTag: 0, filtercontract.ActionQuarantine: 0}
}

func mapKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
