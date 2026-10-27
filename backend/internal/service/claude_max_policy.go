package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	SettingKeyClaudeMaxPolicySettings = "claude_max_policy_settings"
	ClaudeMaxPolicyActionDowngrade    = "downgrade"
)

// ClaudeMaxPolicySettings applies to explicit Max effort on Anthropic accounts.
type ClaudeMaxPolicySettings struct {
	Action          string `json:"action"`
	Scope           string `json:"scope"`
	ErrorMessage    string `json:"error_message"`
	DowngradeEffort string `json:"downgrade_effort"`
}

func DefaultClaudeMaxPolicySettings() *ClaudeMaxPolicySettings {
	return &ClaudeMaxPolicySettings{Action: BetaPolicyActionPass, Scope: BetaPolicyScopeAll, DowngradeEffort: "high"}
}

func (settings *ClaudeMaxPolicySettings) Validate() error {
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	switch settings.Action {
	case BetaPolicyActionPass, BetaPolicyActionBlock, ClaudeMaxPolicyActionDowngrade:
	default:
		return fmt.Errorf("invalid action %q", settings.Action)
	}
	switch settings.Scope {
	case BetaPolicyScopeAll, BetaPolicyScopeOAuth, BetaPolicyScopeAPIKey, BetaPolicyScopeBedrock:
	default:
		return fmt.Errorf("invalid scope %q", settings.Scope)
	}
	switch settings.DowngradeEffort {
	case "low", "medium", "high", "xhigh":
	case "":
		if settings.Action == ClaudeMaxPolicyActionDowngrade {
			return fmt.Errorf("downgrade_effort is required when action is downgrade")
		}
	default:
		return fmt.Errorf("invalid downgrade_effort %q", settings.DowngradeEffort)
	}
	return nil
}

func (s *SettingService) GetClaudeMaxPolicySettings(ctx context.Context) (*ClaudeMaxPolicySettings, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyClaudeMaxPolicySettings)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && value == "") {
		return DefaultClaudeMaxPolicySettings(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("get claude max policy settings: %w", err)
	}
	var settings ClaudeMaxPolicySettings
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return nil, fmt.Errorf("decode claude max policy settings: %w", err)
	}
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	return &settings, nil
}

func (s *SettingService) SetClaudeMaxPolicySettings(ctx context.Context, settings *ClaudeMaxPolicySettings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, SettingKeyClaudeMaxPolicySettings, string(data))
}

// ClaudeMaxBlockedError is a local policy denial, not an upstream failure.
type ClaudeMaxBlockedError struct{ Message string }

func (e *ClaudeMaxBlockedError) Error() string { return e.Message }

func (s *GatewayService) applyClaudeMaxPolicyToBody(ctx context.Context, account *Account, body []byte) ([]byte, error) {
	if account == nil || account.Platform != PlatformAnthropic || s.settingService == nil {
		return body, nil
	}
	isMax := func(effort gjson.Result) bool {
		return effort.Type == gjson.String && strings.EqualFold(strings.TrimSpace(effort.String()), "max")
	}
	var paths []string
	if isMax(gjson.GetBytes(body, "output_config.effort")) {
		paths = append(paths, "output_config.effort")
	}
	// Mid-conversation effort overrides must not bypass the top-level policy.
	gjson.GetBytes(body, "messages").ForEach(func(index, message gjson.Result) bool {
		if isMax(message.Get("output_config.effort")) {
			paths = append(paths, "messages."+strconv.FormatInt(index.Int(), 10)+".output_config.effort")
		}
		return true
	})
	if len(paths) == 0 {
		return body, nil
	}
	settings, err := s.settingService.GetClaudeMaxPolicySettings(ctx)
	if err != nil {
		return nil, err
	}
	if !betaPolicyScopeMatches(settings.Scope, account.IsOAuth(), account.IsBedrock()) {
		return body, nil
	}
	switch settings.Action {
	case BetaPolicyActionBlock:
		message := strings.TrimSpace(settings.ErrorMessage)
		if message == "" {
			message = "Claude Max effort is not allowed"
		}
		return nil, &ClaudeMaxBlockedError{Message: message}
	case ClaudeMaxPolicyActionDowngrade:
		for _, path := range paths {
			body, err = sjson.SetBytes(body, path, settings.DowngradeEffort)
			if err != nil {
				return nil, fmt.Errorf("downgrade claude max effort: %w", err)
			}
		}
	}
	return body, nil
}

// syncClaudeMaxPolicyBedrockBody keeps the request model/stream metadata while
// reflecting whether Bedrock actually retained the downgraded effort. Only
// policy rewrites need synchronization; pass-through requests remain untouched.
func syncClaudeMaxPolicyBedrockBody(parsed *ParsedRequest, policyBody, wireBody []byte) error {
	if bytes.Equal(parsed.Body.Bytes(), policyBody) {
		return nil
	}
	var err error
	if effort := gjson.GetBytes(wireBody, "output_config.effort"); effort.Exists() {
		policyBody, err = sjson.SetRawBytes(policyBody, "output_config.effort", []byte(effort.Raw))
	} else {
		policyBody, err = sjson.DeleteBytes(policyBody, "output_config.effort")
	}
	if err != nil {
		return fmt.Errorf("sync claude max policy effort: %w", err)
	}
	return parsed.ReplaceBody(policyBody)
}
