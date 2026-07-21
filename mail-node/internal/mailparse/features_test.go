package mailparse

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ticket/email-filter-contract"
	"golang.org/x/text/encoding/simplifiedchinese"
)

type featureGoldenCase struct {
	CaseID            string                      `json:"case_id"`
	Fixture           string                      `json:"fixture"`
	MaildirUniqueName string                      `json:"maildir_unique_name"`
	Features          filtercontract.MailFeatures `json:"features"`
}

func TestParseFileMatchesFilterContractGolden(t *testing.T) {
	contractDir := filepath.Join("..", "..", "..", "docs", "filter-contract", "v1")
	data, err := os.ReadFile(filepath.Join(contractDir, "golden-cases.json"))
	if err != nil {
		t.Fatalf("ReadFile(golden-cases.json) error = %v", err)
	}
	var cases []featureGoldenCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("Unmarshal(golden-cases.json) error = %v", err)
	}

	for _, golden := range cases {
		t.Run(golden.CaseID, func(t *testing.T) {
			result, err := ParseFile(filepath.Join(contractDir, "eml", golden.Fixture), Options{
				Mailbox:           golden.Features.Mailbox,
				MaildirBase:       filepath.Join(contractDir, "eml"),
				MaildirUniqueName: golden.MaildirUniqueName,
				ServerID:          golden.Features.ServerID,
			})
			if err != nil {
				t.Fatalf("ParseFile() error = %v", err)
			}
			if !reflect.DeepEqual(result.Features, golden.Features) {
				want, _ := json.MarshalIndent(golden.Features, "", "  ")
				got, _ := json.MarshalIndent(result.Features, "", "  ")
				t.Fatalf("features mismatch\nwant: %s\n got: %s", want, got)
			}
		})
	}
}

func TestParseFileDecodesGB18030HeaderAndBody(t *testing.T) {
	const subject = "订单状态更新"
	const body = "您的订单已经发货"
	subjectBytes, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(subject))
	if err != nil {
		t.Fatalf("encode subject: %v", err)
	}
	bodyBytes, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(body))
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}
	eml := strings.Join([]string{
		"Message-ID: <gb18030@example.test>",
		"From: Sender <sender@example.test>",
		"To: inbox@example.test",
		"Subject: =?GB18030?B?" + base64.StdEncoding.EncodeToString(subjectBytes) + "?=",
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=gb18030",
		"Content-Transfer-Encoding: base64",
		"",
		base64.StdEncoding.EncodeToString(bodyBytes),
		"",
	}, "\r\n")

	filePath := filepath.Join(t.TempDir(), "encoded.eml")
	if err := os.WriteFile(filePath, []byte(eml), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	result, err := ParseFile(filePath, Options{
		Mailbox:           "inbox@example.test",
		MaildirBase:       filepath.Dir(filePath),
		MaildirUniqueName: "encoded:2,S",
		ServerID:          9,
	})
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	if result.Features.Subject != subject || result.Features.Text != body {
		t.Fatalf("decoded features = subject %q, text %q", result.Features.Subject, result.Features.Text)
	}
}

func TestParseFileNormalizesIdentityAndHonorsURLLimit(t *testing.T) {
	eml := strings.Join([]string{
		"Message-ID: <identity@example.test>",
		"Return-Path: <bounce@same.example>",
		"From: Sender <sender@same.example>",
		"Reply-To: Reply <reply@same.example>",
		"To: inbox@example.test",
		"Subject: identity",
		"List-ID: Updates <updates.same.example>",
		"List-Unsubscribe: not a URI",
		"Precedence: LIST",
		"Content-Type: text/html; charset=utf-8",
		"",
		`<a href="https://a.example/one">One</a><a href="https://b.example/two">Two</a><a href="https://c.example/three">Three</a>`,
		"",
	}, "\r\n")
	filePath := filepath.Join(t.TempDir(), "identity.eml")
	if err := os.WriteFile(filePath, []byte(eml), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	limits := DefaultLimits()
	limits.MaxURLs = 2
	result, err := ParseFile(filePath, Options{
		Mailbox:           "inbox@example.test",
		MaildirBase:       filepath.Dir(filePath),
		MaildirUniqueName: "identity:2,S",
		ServerID:          9,
		Limits:            limits,
	})
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	features := result.Features
	if features.EnvelopeFrom == nil || features.EnvelopeFrom.Address != "bounce@same.example" {
		t.Fatalf("EnvelopeFrom = %+v", features.EnvelopeFrom)
	}
	if features.FromReplyToDomainMatch == nil || !*features.FromReplyToDomainMatch {
		t.Fatalf("FromReplyToDomainMatch = %v", features.FromReplyToDomainMatch)
	}
	if features.ListID != "updates.same.example" || features.Precedence != "list" || features.ListUnsubscribe {
		t.Fatalf("list features = id %q precedence %q unsubscribe %v", features.ListID, features.Precedence, features.ListUnsubscribe)
	}
	if len(features.URLs) != 2 || !contains(features.ParseWarnings, "url_limit_exceeded") {
		t.Fatalf("URL limit result = URLs %+v warnings %v", features.URLs, features.ParseWarnings)
	}
}

func TestParseFileCapsDuplicateHeaderValues(t *testing.T) {
	eml := strings.Join([]string{
		"Message-ID: <headers@example.test>",
		"From: Sender <sender@example.test>",
		"To: inbox@example.test",
		"Subject: first",
		"Subject: second",
		"X-Campaign: alpha",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"body",
		"",
	}, "\r\n")
	filePath := filepath.Join(t.TempDir(), "headers.eml")
	if err := os.WriteFile(filePath, []byte(eml), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	limits := DefaultLimits()
	limits.MaxHeaderValues = 1
	result, err := ParseFile(filePath, Options{
		Mailbox:           "inbox@example.test",
		MaildirBase:       filepath.Dir(filePath),
		MaildirUniqueName: "headers:2,S",
		ServerID:          9,
		Limits:            limits,
	})
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	if got := result.Features.Headers["subject"]; !reflect.DeepEqual(got, []string{"first"}) {
		t.Fatalf("subject headers = %v", got)
	}
	if !contains(result.Features.ParseWarnings, "header_values_limit_exceeded") {
		t.Fatalf("warnings = %v", result.Features.ParseWarnings)
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
