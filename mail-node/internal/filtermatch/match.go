package filtermatch

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	filtercontract "github.com/ticket/email-filter-contract"
)

type Condition struct {
	value   filtercontract.Condition
	pattern *regexp.Regexp
}

type Result struct {
	Matched  bool
	Fields   []string
	Evidence []filtercontract.Evidence
}

func Compile(policyKind string, values []filtercontract.Condition) ([]Condition, error) {
	result := make([]Condition, len(values))
	for i, value := range values {
		if err := filtercontract.ValidateCondition(policyKind, value); err != nil {
			return nil, err
		}
		result[i].value = value
		if value.Operator == "regex" {
			pattern, _ := value.Value.String()
			compiled, err := regexp.Compile(pattern)
			if err != nil {
				return nil, fmt.Errorf("compile condition %s: %w", value.Field, err)
			}
			result[i].pattern = compiled
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].value.Position < result[j].value.Position })
	return result, nil
}

func MatchAll(conditions []Condition, features filtercontract.MailFeatures) Result {
	result := Result{Matched: true, Fields: []string{}, Evidence: []filtercontract.Evidence{}}
	seenFields := map[string]struct{}{}
	for _, condition := range conditions {
		matched, known, occurrences := condition.match(features)
		if !known {
			return Result{Matched: false, Fields: []string{}, Evidence: []filtercontract.Evidence{}}
		}
		if condition.value.Negated {
			matched = !matched
			if matched {
				occurrences = 0
			}
		}
		if !matched {
			return Result{Matched: false, Fields: []string{}, Evidence: []filtercontract.Evidence{}}
		}
		if _, exists := seenFields[condition.value.Field]; !exists {
			result.Fields = append(result.Fields, condition.value.Field)
			seenFields[condition.value.Field] = struct{}{}
		}
		result.Evidence = append(result.Evidence, filtercontract.Evidence{
			Field: condition.value.Field, Summary: evidenceSummary(condition.value), Occurrences: occurrences,
		})
	}
	sort.Strings(result.Fields)
	sort.SliceStable(result.Evidence, func(i, j int) bool {
		if result.Evidence[i].Field != result.Evidence[j].Field {
			return result.Evidence[i].Field < result.Evidence[j].Field
		}
		return result.Evidence[i].Summary < result.Evidence[j].Summary
	})
	return result
}

func (condition Condition) match(features filtercontract.MailFeatures) (bool, bool, int) {
	value := condition.value
	switch value.Field {
	case "header_from.address":
		return matchStrings([]string{features.HeaderFrom.Address}, value, condition.pattern)
	case "header_from.domain":
		return matchStrings([]string{features.HeaderFrom.Domain}, value, condition.pattern)
	case "envelope_from.address":
		if features.EnvelopeFrom == nil {
			return false, false, 0
		}
		return matchStrings([]string{features.EnvelopeFrom.Address}, value, condition.pattern)
	case "envelope_from.domain":
		if features.EnvelopeFrom == nil {
			return false, false, 0
		}
		return matchStrings([]string{features.EnvelopeFrom.Domain}, value, condition.pattern)
	case "reply_to.domain":
		if len(features.ReplyTo) == 0 {
			return false, false, 0
		}
		values := make([]string, 0, len(features.ReplyTo))
		for _, address := range features.ReplyTo {
			values = append(values, address.Domain)
		}
		return matchStrings(values, value, condition.pattern)
	case "mailbox.address":
		return matchStrings([]string{features.Mailbox}, value, condition.pattern)
	case "subject":
		return matchStrings([]string{features.Subject}, value, condition.pattern)
	case "text":
		return matchStrings([]string{strings.TrimSpace(features.Text + "\n" + features.HTMLText)}, value, condition.pattern)
	case "headers":
		header, _ := value.Value.String()
		values, exists := features.Headers[header]
		return exists && len(values) > 0, true, len(values)
	case "list_unsubscribe":
		expected, _ := value.Value.Bool()
		return features.ListUnsubscribe == expected, true, boolCount(features.ListUnsubscribe == expected)
	case "list_id":
		matched := features.ListID != ""
		return matched, true, boolCount(matched)
	case "precedence":
		return matchStrings([]string{features.Precedence}, value, condition.pattern)
	case "from_reply_to_domain_match":
		if features.FromReplyToDomainMatch == nil {
			return false, false, 0
		}
		expected, _ := value.Value.Bool()
		matched := *features.FromReplyToDomainMatch == expected
		return matched, true, boolCount(matched)
	case "has_attachment":
		expected, _ := value.Value.Bool()
		matched := (len(features.Attachments) > 0) == expected
		return matched, true, boolCount(matched)
	case "attachment.filename":
		values := make([]string, 0, len(features.Attachments))
		for _, attachment := range features.Attachments {
			values = append(values, attachment.Filename)
		}
		return matchStrings(values, value, condition.pattern)
	case "size_bytes":
		return matchInteger(features.SizeBytes, value)
	case "url_count":
		count := int64(0)
		for _, url := range features.URLs {
			count += int64(url.Occurrences)
		}
		return matchInteger(count, value)
	case "tracking_pixel_count":
		return matchInteger(int64(features.TrackingPixelCount), value)
	default:
		return false, false, 0
	}
}

func matchStrings(values []string, condition filtercontract.Condition, pattern *regexp.Regexp) (bool, bool, int) {
	expected, _ := condition.Value.String()
	occurrences := 0
	for _, actual := range values {
		if actual == "" {
			continue
		}
		switch condition.Operator {
		case "eq":
			if strings.EqualFold(actual, expected) {
				occurrences++
			}
		case "contains":
			occurrences += strings.Count(strings.ToLower(actual), strings.ToLower(expected))
		case "suffix":
			actualLower, expectedLower := strings.ToLower(actual), strings.ToLower(expected)
			if condition.Field == "header_from.domain" || condition.Field == "envelope_from.domain" || condition.Field == "reply_to.domain" {
				if actualLower == expectedLower || strings.HasSuffix(actualLower, "."+expectedLower) {
					occurrences++
				}
			} else if strings.HasSuffix(actualLower, expectedLower) {
				occurrences++
			}
		case "regex":
			if pattern != nil {
				occurrences += len(pattern.FindAllStringIndex(actual, 1000))
			}
		}
	}
	return occurrences > 0, true, occurrences
}

func matchInteger(actual int64, condition filtercontract.Condition) (bool, bool, int) {
	expected, _ := condition.Value.Integer()
	matched := false
	switch condition.Operator {
	case "gte":
		matched = actual >= expected
	case "lte":
		matched = actual <= expected
	}
	return matched, true, boolCount(matched)
}

func evidenceSummary(condition filtercontract.Condition) string {
	if condition.Negated {
		return "negated condition satisfied"
	}
	switch condition.Field {
	case "size_bytes", "url_count", "tracking_pixel_count":
		return "numeric condition satisfied"
	case "subject", "text", "attachment.filename":
		return "content condition matched"
	default:
		return "condition matched"
	}
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}
