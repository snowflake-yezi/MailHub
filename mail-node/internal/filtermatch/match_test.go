package filtermatch

import (
	"testing"

	filtercontract "github.com/ticket/email-filter-contract"
)

func TestMatchAllUsesDomainBoundariesAndUnknownSemantics(t *testing.T) {
	conditions, err := Compile(filtercontract.PolicyAd, []filtercontract.Condition{
		{Field: "header_from.domain", Operator: "suffix", Value: filtercontract.StringValue("example.com"), Position: 0},
		{Field: "from_reply_to_domain_match", Operator: "eq", Value: filtercontract.BoolValue(false), Position: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	mismatch := false
	features := filtercontract.MailFeatures{
		HeaderFrom: filtercontract.MailAddress{Domain: "news.example.com"}, FromReplyToDomainMatch: &mismatch,
	}
	if result := MatchAll(conditions, features); !result.Matched || len(result.Evidence) != 2 {
		t.Fatalf("matched result = %#v", result)
	}
	features.HeaderFrom.Domain = "badexample.com"
	if MatchAll(conditions, features).Matched {
		t.Fatal("domain suffix ignored label boundary")
	}
	features.HeaderFrom.Domain = "example.com"
	features.FromReplyToDomainMatch = nil
	conditions[1].value.Negated = true
	if MatchAll(conditions, features).Matched {
		t.Fatal("unknown reply-to fact matched through negation")
	}
}

func TestMatchAllCountsVisibleTextAndURLs(t *testing.T) {
	conditions, err := Compile(filtercontract.PolicyAd, []filtercontract.Condition{
		{Field: "text", Operator: "contains", Value: filtercontract.StringValue("offer"), Position: 0},
		{Field: "url_count", Operator: "gte", Value: filtercontract.IntegerValue(3), Position: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	features := filtercontract.MailFeatures{
		Text: "Limited offer", HTMLText: "Second OFFER",
		URLs: []filtercontract.URLFeature{{Occurrences: 2}, {Occurrences: 1}},
	}
	result := MatchAll(conditions, features)
	if !result.Matched || result.Evidence[0].Occurrences != 2 {
		t.Fatalf("result = %#v", result)
	}
}
