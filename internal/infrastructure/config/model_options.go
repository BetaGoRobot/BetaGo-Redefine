package config

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// ModelOptions contains supported per-model request overrides. Empty fields
// preserve the caller's existing behavior.
type ModelOptions struct {
	ServiceTier     string `json:"service_tier,omitempty" yaml:"service_tier,omitempty" toml:"service_tier,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty" yaml:"reasoning_effort,omitempty" toml:"reasoning_effort,omitempty"`
}

type ModelOptionsMap map[string]ModelOptions

func (options ModelOptionsMap) Validate() error {
	for modelID, option := range options {
		if modelID == "" || modelID != strings.TrimSpace(modelID) {
			return fmt.Errorf("model ID must be non-empty without surrounding whitespace")
		}
		switch option.ServiceTier {
		case "", "auto", "default", "fast", "flex":
		default:
			return fmt.Errorf("model %q: invalid service_tier %q", modelID, option.ServiceTier)
		}
		switch option.ReasoningEffort {
		case "", "minimal", "low", "medium", "high":
		default:
			return fmt.Errorf("model %q: invalid reasoning_effort %q", modelID, option.ReasoningEffort)
		}
	}
	return nil
}

// ParseModelOptions rejects unknown fields and non-string values, including
// null, so configuration mistakes cannot silently change request behavior.
func ParseModelOptions(value string) (ModelOptionsMap, error) {
	var raw map[string]map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value), &raw); err != nil {
		return nil, fmt.Errorf("invalid model options: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("model options must be an object")
	}
	options := make(ModelOptionsMap, len(raw))
	for modelID, fields := range raw {
		if fields == nil {
			return nil, fmt.Errorf("model %q: options must be an object", modelID)
		}
		var option ModelOptions
		for field, value := range fields {
			var target *string
			switch field {
			case "service_tier":
				target = &option.ServiceTier
			case "reasoning_effort":
				target = &option.ReasoningEffort
			default:
				return nil, fmt.Errorf("model %q: unsupported option %q", modelID, field)
			}
			if len(value) == 0 || value[0] != '"' {
				return nil, fmt.Errorf("model %q: %s must be a string", modelID, field)
			}
			if err := json.Unmarshal(value, target); err != nil {
				return nil, err
			}
		}
		options[modelID] = option
	}
	return options, options.Validate()
}

func modelOptionsFromTOML(data []byte) (ModelOptionsMap, error) {
	// Decode this table without dropping unknown fields; other legacy TOML
	// sections retain their existing permissive decoding behavior.
	var raw struct {
		ArkConfig struct {
			ModelOptions map[string]map[string]any `toml:"model_options"`
		} `toml:"ark_config"`
	}
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw.ArkConfig.ModelOptions == nil {
		return ModelOptionsMap{}, nil
	}
	encoded, err := json.Marshal(raw.ArkConfig.ModelOptions)
	if err != nil {
		return nil, err
	}
	return ParseModelOptions(string(encoded))
}
