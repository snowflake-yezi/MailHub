package filterdecision

import "testing"

func TestValidateConfigKeepsLegacySafeDefaults(t *testing.T) {
	for _, mode := range []string{EngineModeLegacy, EngineModeDualShadow, EngineModeDualFilter} {
		if err := ValidateConfig(nil, map[string]string{EngineModeConfigKey: mode, AutoQuarantineConfigKey: "false"}); err != nil {
			t.Fatalf("mode %s: %v", mode, err)
		}
	}
	for _, values := range []map[string]string{{EngineModeConfigKey: "active"}, {AutoQuarantineConfigKey: "yes"}} {
		if err := ValidateConfig(nil, values); err == nil {
			t.Fatalf("invalid config accepted: %#v", values)
		}
	}
}
