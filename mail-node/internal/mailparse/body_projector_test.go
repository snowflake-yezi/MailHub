package mailparse

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBodyProjectionSelectsLastAlternative(t *testing.T) {
	result, err := ParseFile(filepath.Join("testdata", "body_projection", "alternative-last.eml"), Options{
		Mailbox:       "inbox@example.test",
		MaildirBase:   filepath.Join("testdata", "body_projection"),
		ProjectorMode: ProjectorEnforce,
	})
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	if result.Message.HTMLBody != "<p>selected html</p>" {
		t.Fatalf("HTMLBody = %q, want selected final alternative", result.Message.HTMLBody)
	}
}

func TestBodyProjectionMixedRelatedAlternative(t *testing.T) {
	result := parseProjectionFixture(t, "mixed-related-alternative.eml")
	if result.Status != ParseOK || result.ErrorCode != "" {
		t.Fatalf("status = %s/%s, warnings = %+v", result.Status, result.ErrorCode, result.Warnings)
	}
	if result.Message.TextBody != "primary plain\n--\nsupporting plain" {
		t.Fatalf("TextBody = %q", result.Message.TextBody)
	}
	if result.Message.HTMLBody != `<p>primary html<img src="cid:logo@example.test"></p>` {
		t.Fatalf("HTMLBody = %q", result.Message.HTMLBody)
	}
	if len(result.BodyViews) == 0 || result.PrimaryView == nil {
		t.Fatalf("body views = %+v, primary = %+v", result.BodyViews, result.PrimaryView)
	}
	if !reflect.DeepEqual(result.PrimaryView.PlainPath, PartPath{0, 0, 0}) ||
		!reflect.DeepEqual(result.PrimaryView.HTMLPath, PartPath{0, 0, 1}) ||
		!reflect.DeepEqual(result.PrimaryView.SelectedPath, PartPath{0, 0, 1}) ||
		!reflect.DeepEqual(result.PrimaryView.RelatedRoot, PartPath{0, 0}) {
		t.Fatalf("primary view = %+v", result.PrimaryView)
	}

	parts := projectedPartsByPath(result.Parts)
	assertProjectedPart(t, parts, "0.0.0.0", RoleBodyPlain, nil)
	assertProjectedPart(t, parts, "0.0.0.1", RoleBodyHTML, nil)
	assertProjectedPart(t, parts, "0.0.1", RoleRelatedResource, intPointer(2))
	assertProjectedPart(t, parts, "0.1", RoleBodyPlain, nil)
	assertProjectedPart(t, parts, "0.2", RoleAttachment, intPointer(0))
	assertProjectedPart(t, parts, "0.3", RoleAttachment, intPointer(1))
	if got := parts["0.0.1"].ReferencedBy; !reflect.DeepEqual(got, []PartPath{{0, 0, 1}}) {
		t.Fatalf("scoped related reference = %v, want HTML path", got)
	}
	if got := parts["0.3"].ReferencedBy; len(got) != 0 {
		t.Fatalf("scope-external duplicate CID was referenced: %v", got)
	}
	if len(result.Message.Attachments) != 3 || result.Message.Attachments[0].Index != 0 || result.Message.Attachments[1].Index != 1 || result.Message.Attachments[2].Index != 2 {
		t.Fatalf("attachments = %+v", result.Message.Attachments)
	}
}

func TestBodyProjectionContainerRoles(t *testing.T) {
	tests := []struct {
		fixture    string
		wantText   string
		wantStatus ParseStatus
		wantCode   string
		wantPath   string
		wantRole   PartRole
	}{
		{fixture: "signed.eml", wantText: "signed body", wantStatus: ParseOK, wantPath: "0.1", wantRole: RoleSignature},
		{fixture: "encrypted.eml", wantStatus: ParsePartial, wantCode: "unsupported_encrypted_body", wantPath: "0.0", wantRole: RoleEncrypted},
		{fixture: "report.eml", wantText: "Delivery failed", wantStatus: ParseOK, wantPath: "0.1", wantRole: RoleReport},
		{fixture: "digest.eml", wantStatus: ParseOK, wantPath: "0.0", wantRole: RoleEmbeddedMessage},
		{fixture: "embedded.eml", wantStatus: ParseOK, wantPath: "0", wantRole: RoleEmbeddedMessage},
	}
	for _, test := range tests {
		t.Run(test.fixture, func(t *testing.T) {
			result := parseProjectionFixture(t, test.fixture)
			if result.Status != test.wantStatus || result.ErrorCode != test.wantCode {
				t.Fatalf("status = %s/%s, want %s/%s; warnings = %+v", result.Status, result.ErrorCode, test.wantStatus, test.wantCode, result.Warnings)
			}
			if result.Message.TextBody != test.wantText {
				t.Fatalf("TextBody = %q, want %q", result.Message.TextBody, test.wantText)
			}
			part, ok := projectedPartsByPath(result.Parts)[test.wantPath]
			if !ok || part.Role != test.wantRole {
				t.Fatalf("part %s = %+v, want role %s", test.wantPath, part, test.wantRole)
			}
		})
	}
}

func TestBodyProjectionWholeAndFieldLimits(t *testing.T) {
	fixture := filepath.Join("testdata", "body_projection", "alternative-last.eml")
	tests := []struct {
		name       string
		limits     Limits
		wantStatus ParseStatus
		wantCode   string
	}{
		{name: "raw", limits: Limits{MaxMessageBytes: 1}, wantStatus: ParseTooLarge, wantCode: "message_size_limit_exceeded"},
		{name: "part", limits: Limits{MaxMessageBytes: 1024 * 1024, MaxPartBytes: 4}, wantStatus: ParseTooLarge, wantCode: "part_size_limit_exceeded"},
		{name: "count", limits: Limits{MaxMessageBytes: 1024 * 1024, MaxParts: 2}, wantStatus: ParseTooLarge, wantCode: "part_count_limit_exceeded"},
		{name: "body", limits: Limits{MaxMessageBytes: 1024 * 1024, MaxTextBytes: 4, MaxHTMLBytes: 4}, wantStatus: ParsePartial, wantCode: "body_size_limit_exceeded"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := ParseFile(fixture, Options{
				Mailbox:       "inbox@example.test",
				MaildirBase:   filepath.Dir(fixture),
				ProjectorMode: ProjectorEnforce,
				Limits:        test.limits,
			})
			if err != nil {
				t.Fatalf("ParseFile() error = %v", err)
			}
			if result.Status != test.wantStatus || result.ErrorCode != test.wantCode {
				t.Fatalf("status = %s/%s, want %s/%s; warnings = %+v", result.Status, result.ErrorCode, test.wantStatus, test.wantCode, result.Warnings)
			}
			if test.wantStatus == ParseTooLarge && (result.Message.TextBody != "" || result.Message.HTMLBody != "") {
				t.Fatalf("too-large body was retained: text=%q html=%q", result.Message.TextBody, result.Message.HTMLBody)
			}
		})
	}
}

func TestBodyProjectionDepthLimit(t *testing.T) {
	content := strings.Join([]string{
		"Message-ID: <depth@example.test>",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="outer"`,
		"", "--outer",
		`Content-Type: multipart/mixed; boundary="inner"`,
		"", "--inner",
		"Content-Type: text/plain; charset=utf-8",
		"", "too deep", "--inner--", "--outer--", "",
	}, "\r\n")
	path := filepath.Join(t.TempDir(), "depth.eml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	result, err := ParseFile(path, Options{
		Mailbox:       "inbox@example.test",
		MaildirBase:   filepath.Dir(path),
		ProjectorMode: ProjectorEnforce,
		Limits:        Limits{MaxMessageBytes: 1024 * 1024, MaxDepth: 1},
	})
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	if result.Status != ParseTooLarge || result.ErrorCode != "mime_depth_limit_exceeded" {
		t.Fatalf("status = %s/%s, warnings = %+v", result.Status, result.ErrorCode, result.Warnings)
	}
}

func TestBodyProjectionBindsMalformedChildToRetainedParent(t *testing.T) {
	result := parseProjectionFixture(t, "malformed-child.eml")
	if result.Status != ParsePartial || result.ErrorCode != "mime_partial" || result.Message.TextBody != "retained body" {
		t.Fatalf("result = status %s/%s, text %q, warnings %+v", result.Status, result.ErrorCode, result.Message.TextBody, result.Warnings)
	}
	found := false
	for _, warning := range result.Warnings {
		if warning.Code == "malformed_child_part" {
			found = true
			if !reflect.DeepEqual(warning.Path, PartPath{}) {
				t.Fatalf("malformed child warning path = %v, want root parent", warning.Path)
			}
		}
	}
	if !found {
		t.Fatalf("malformed child warning missing: %+v", result.Warnings)
	}
}

func TestBodyProjectionDoesNotFailForUnselectedEncryptedAlternative(t *testing.T) {
	result := parseProjectionFixture(t, "alternative-skips-encrypted.eml")
	if result.Status != ParseOK || result.ErrorCode != "" || result.Message.TextBody != "safe fallback" {
		t.Fatalf("result = status %s/%s, text %q, warnings %+v", result.Status, result.ErrorCode, result.Message.TextBody, result.Warnings)
	}
	parts := projectedPartsByPath(result.Parts)
	if part := parts["0.1.0"]; part.Role != RoleEncrypted {
		t.Fatalf("encrypted control part = %+v, want role %s", part, RoleEncrypted)
	}
	for _, warning := range result.Warnings {
		if warning.Code == "unsupported_encrypted_body" {
			t.Fatalf("unselected encrypted warning leaked into result: %+v", result.Warnings)
		}
	}
}

func parseProjectionFixture(t *testing.T, name string) *ParseResult {
	t.Helper()
	path := filepath.Join("testdata", "body_projection", name)
	result, err := ParseFile(path, Options{
		Mailbox:       "inbox@example.test",
		MaildirBase:   filepath.Dir(path),
		ProjectorMode: ProjectorEnforce,
	})
	if err != nil {
		t.Fatalf("ParseFile(%s) error = %v", name, err)
	}
	return result
}

func projectedPartsByPath(parts []ProjectedPart) map[string]ProjectedPart {
	result := make(map[string]ProjectedPart, len(parts))
	for _, part := range parts {
		result[partPathString(part.Path)] = part
	}
	return result
}

func assertProjectedPart(t *testing.T, parts map[string]ProjectedPart, path string, role PartRole, externalIndex *int) {
	t.Helper()
	part, ok := parts[path]
	if !ok {
		t.Fatalf("part %s missing from %+v", path, parts)
	}
	if part.Role != role || !reflect.DeepEqual(part.ExternalIndex, externalIndex) {
		t.Fatalf("part %s = role %s index %v, want %s/%v", path, part.Role, part.ExternalIndex, role, externalIndex)
	}
}

func intPointer(value int) *int {
	return &value
}

func TestBodyProjectionHonorsRelatedStart(t *testing.T) {
	result, err := ParseFile(filepath.Join("testdata", "body_projection", "related-start.eml"), Options{
		Mailbox:       "inbox@example.test",
		MaildirBase:   filepath.Join("testdata", "body_projection"),
		ProjectorMode: ProjectorEnforce,
	})
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	if result.Message.HTMLBody != "<p>selected root</p>" {
		t.Fatalf("HTMLBody = %q, want related start root", result.Message.HTMLBody)
	}
}
