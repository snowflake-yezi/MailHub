package mailparse

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateConfigRejectsInvalidProjectorValues(t *testing.T) {
	valid := map[string]string{
		BodyProjectorModeConfigKey: string(ProjectorShadow),
		MaxMessageBytesConfigKey:   "26214400",
	}
	if err := ValidateConfig(nil, valid); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	for name, values := range map[string]map[string]string{
		"mode":  {BodyProjectorModeConfigKey: "invalid"},
		"small": {MaxMessageBytesConfigKey: "1048575"},
		"large": {MaxMessageBytesConfigKey: "1073741825"},
		"type":  {MaxMessageBytesConfigKey: "25MiB"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateConfig(nil, values); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

func TestConfigureFromConfigDoesNotReplaceValidRuntimeOnError(t *testing.T) {
	original := CurrentRuntimeConfig()
	t.Cleanup(func() {
		if err := ConfigureRuntime(original.Mode, original.MaxMessageBytes); err != nil {
			t.Fatalf("restore runtime config: %v", err)
		}
	})

	if err := ConfigureRuntime(ProjectorShadow, 32*1024*1024); err != nil {
		t.Fatalf("ConfigureRuntime() error = %v", err)
	}
	if err := ConfigureFromConfig(map[string]string{BodyProjectorModeConfigKey: "broken"}); err == nil {
		t.Fatal("invalid config accepted")
	}
	if got := CurrentRuntimeConfig(); got.Mode != ProjectorShadow || got.MaxMessageBytes != 32*1024*1024 {
		t.Fatalf("runtime config changed after rejection: %+v", got)
	}
}

func TestProjectorModesPreserveLegacyAndExposeShadow(t *testing.T) {
	path := filepath.Join("testdata", "body_projection", "alternative-last.eml")
	base := Options{Mailbox: "inbox@example.test", MaildirBase: filepath.Dir(path)}

	legacy, err := ParseFile(path, base)
	if err != nil {
		t.Fatalf("legacy ParseFile() error = %v", err)
	}
	if legacy.Message.HTMLBody != "<p>stale html</p>" || len(legacy.Parts) == 0 {
		t.Fatalf("legacy result = body %q, parts %d", legacy.Message.HTMLBody, len(legacy.Parts))
	}

	base.ProjectorMode = ProjectorShadow
	shadow, err := ParseFile(path, base)
	if err != nil {
		t.Fatalf("shadow ParseFile() error = %v", err)
	}
	if shadow.Message.HTMLBody != "<p>stale html</p>" || shadow.PrimaryView == nil || len(shadow.Parts) == 0 {
		t.Fatalf("shadow result = body %q, primary %+v, parts %d", shadow.Message.HTMLBody, shadow.PrimaryView, len(shadow.Parts))
	}

	base.ProjectorMode = ProjectorEnforce
	enforced, err := ParseFile(path, base)
	if err != nil {
		t.Fatalf("enforce ParseFile() error = %v", err)
	}
	if enforced.Message.HTMLBody != "<p>selected html</p>" {
		t.Fatalf("enforced HTMLBody = %q", enforced.Message.HTMLBody)
	}
}

func TestParseAttachmentUsesUnifiedParserAndEnforceLimit(t *testing.T) {
	original := CurrentRuntimeConfig()
	t.Cleanup(func() {
		if err := ConfigureRuntime(original.Mode, original.MaxMessageBytes); err != nil {
			t.Fatalf("restore runtime config: %v", err)
		}
	})
	if err := ConfigureRuntime(ProjectorLegacy, defaultMaxMessageBytes); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join("testdata", "body_projection", "mixed-related-alternative.eml")
	part, err := ParseAttachment(fixture, 1)
	if err != nil {
		t.Fatalf("ParseAttachment() error = %v", err)
	}
	if !part.Inline || part.Info.ContentType != "image/png" || len(part.Content) == 0 {
		t.Fatalf("parsed attachment = %+v", part)
	}

	largePath := filepath.Join(t.TempDir(), "large.eml")
	large := strings.Join([]string{
		"Message-ID: <large-attachment@example.test>",
		"Content-Type: application/octet-stream",
		"Content-Disposition: attachment; filename=large.bin",
		"", strings.Repeat("x", MinMessageBytes),
	}, "\r\n")
	if err := os.WriteFile(largePath, []byte(large), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := ConfigureRuntime(ProjectorEnforce, MinMessageBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseAttachment(largePath, 0); !errors.Is(err, ErrMIMETooLarge) {
		t.Fatalf("ParseAttachment() error = %v, want ErrMIMETooLarge", err)
	}
}
