package filterdecision

import (
	"fmt"
	"strconv"
)

const (
	EngineModeConfigKey     = "filter.engine_mode"
	AutoQuarantineConfigKey = "filter.auto_quarantine_enabled"
	EngineModeLegacy        = "legacy"
	EngineModeDualShadow    = "dual_shadow"
	EngineModeDualFilter    = "dual_filter"
)

func ValidateConfig(_, next map[string]string) error {
	if value, exists := next[EngineModeConfigKey]; exists && !ValidMode(value) {
		return fmt.Errorf("%s must be legacy, dual_shadow or dual_filter", EngineModeConfigKey)
	}
	if value, exists := next[AutoQuarantineConfigKey]; exists {
		if _, err := strconv.ParseBool(value); err != nil {
			return fmt.Errorf("%s must be a boolean", AutoQuarantineConfigKey)
		}
	}
	return nil
}

func ValidMode(value string) bool {
	return value == EngineModeLegacy || value == EngineModeDualShadow || value == EngineModeDualFilter
}
