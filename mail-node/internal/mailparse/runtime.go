package mailparse

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
)

const (
	BodyProjectorModeConfigKey = "mime.body_projector_mode"
	MaxMessageBytesConfigKey   = "mime.max_message_bytes"
	MinMessageBytes            = 1024 * 1024
	MaxMessageBytes            = 1024 * 1024 * 1024
)

type RuntimeConfig struct {
	Mode            ProjectorMode
	MaxMessageBytes int64
}

var currentRuntimeConfig = func() atomic.Value {
	var value atomic.Value
	value.Store(RuntimeConfig{Mode: ProjectorLegacy, MaxMessageBytes: defaultMaxMessageBytes})
	return value
}()

func ConfigureRuntime(mode ProjectorMode, maxMessageBytes int64) error {
	config := RuntimeConfig{Mode: mode, MaxMessageBytes: maxMessageBytes}
	if err := validateRuntimeConfig(config); err != nil {
		return err
	}
	currentRuntimeConfig.Store(config)
	return nil
}

func ConfigureFromConfig(values map[string]string) error {
	config, err := runtimeConfigFromMap(values)
	if err != nil {
		return err
	}
	return ConfigureRuntime(config.Mode, config.MaxMessageBytes)
}

func ValidateConfig(_ map[string]string, next map[string]string) error {
	_, err := runtimeConfigFromMap(next)
	return err
}

func CurrentRuntimeConfig() RuntimeConfig {
	return currentRuntimeConfig.Load().(RuntimeConfig)
}

func runtimeOptions() Options {
	config := CurrentRuntimeConfig()
	return Options{
		ProjectorMode: config.Mode,
		Limits: Limits{
			MaxMessageBytes: config.MaxMessageBytes,
		},
	}
}

func runtimeConfigFromMap(values map[string]string) (RuntimeConfig, error) {
	config := RuntimeConfig{Mode: ProjectorLegacy, MaxMessageBytes: defaultMaxMessageBytes}
	if value := strings.TrimSpace(values[BodyProjectorModeConfigKey]); value != "" {
		config.Mode = ProjectorMode(value)
	}
	if value := strings.TrimSpace(values[MaxMessageBytesConfigKey]); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return RuntimeConfig{}, fmt.Errorf("%s must be an integer: %w", MaxMessageBytesConfigKey, err)
		}
		config.MaxMessageBytes = parsed
	}
	if err := validateRuntimeConfig(config); err != nil {
		return RuntimeConfig{}, err
	}
	return config, nil
}

func validateRuntimeConfig(config RuntimeConfig) error {
	switch config.Mode {
	case ProjectorLegacy, ProjectorShadow, ProjectorEnforce:
	default:
		return fmt.Errorf("%s must be one of legacy, shadow, enforce", BodyProjectorModeConfigKey)
	}
	if config.MaxMessageBytes < MinMessageBytes || config.MaxMessageBytes > MaxMessageBytes {
		return fmt.Errorf("%s must be between %d and %d", MaxMessageBytesConfigKey, MinMessageBytes, MaxMessageBytes)
	}
	return nil
}
