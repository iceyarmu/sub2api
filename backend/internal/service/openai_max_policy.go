package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	SettingKeyOpenAIMaxPolicySettings = "openai_max_policy_settings"
	OpenAIMaxPolicyActionDowngrade    = "downgrade"
)

// OpenAIMaxPolicySettings applies to explicit Max effort on OpenAI accounts.
type OpenAIMaxPolicySettings struct {
	Action          string `json:"action"`
	Scope           string `json:"scope"`
	ErrorMessage    string `json:"error_message"`
	DowngradeEffort string `json:"downgrade_effort"`
}

func DefaultOpenAIMaxPolicySettings() *OpenAIMaxPolicySettings {
	return &OpenAIMaxPolicySettings{Action: BetaPolicyActionPass, Scope: BetaPolicyScopeAll, DowngradeEffort: "high"}
}

func (settings *OpenAIMaxPolicySettings) Validate() error {
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	switch settings.Action {
	case BetaPolicyActionPass, BetaPolicyActionBlock, OpenAIMaxPolicyActionDowngrade:
	default:
		return fmt.Errorf("invalid action %q", settings.Action)
	}
	switch settings.Scope {
	case BetaPolicyScopeAll, BetaPolicyScopeOAuth, BetaPolicyScopeAPIKey:
	default:
		return fmt.Errorf("invalid scope %q", settings.Scope)
	}
	switch settings.DowngradeEffort {
	case "low", "medium", "high", "xhigh":
	case "":
		if settings.Action == OpenAIMaxPolicyActionDowngrade {
			return fmt.Errorf("downgrade_effort is required when action is downgrade")
		}
	default:
		return fmt.Errorf("invalid downgrade_effort %q", settings.DowngradeEffort)
	}
	return nil
}

func (s *SettingService) GetOpenAIMaxPolicySettings(ctx context.Context) (*OpenAIMaxPolicySettings, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyOpenAIMaxPolicySettings)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && value == "") {
		return DefaultOpenAIMaxPolicySettings(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("get openai max policy settings: %w", err)
	}
	var settings OpenAIMaxPolicySettings
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return nil, fmt.Errorf("decode openai max policy settings: %w", err)
	}
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	return &settings, nil
}

func (s *SettingService) SetOpenAIMaxPolicySettings(ctx context.Context, settings *OpenAIMaxPolicySettings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, SettingKeyOpenAIMaxPolicySettings, string(data))
}
