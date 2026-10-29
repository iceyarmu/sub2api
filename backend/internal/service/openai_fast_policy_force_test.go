package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestOpenAIFastForcePriorityIgnoresIncomingTier(t *testing.T) {
	for _, matcher := range []string{OpenAIFastTierPriority, OpenAIFastTierAny, OpenAIFastTierMissing, OpenAIFastTierFlex} {
		for _, tier := range []string{"", "default", "auto", "flex", "scale", "fast", "priority", "ultrafast"} {
			t.Run(matcher+"/"+tier, func(t *testing.T) {
				svc := newOpenAIGatewayServiceWithSettings(t, &OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{{
					ServiceTier: matcher, Action: OpenAIFastPolicyActionForcePriority, Scope: BetaPolicyScopeAll,
				}}})
				account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
				body := []byte(`{"model":"gpt-6-astra","input":"hello"}`)
				if tier != "" {
					var err error
					body, err = sjson.SetBytes(body, "service_tier", tier)
					require.NoError(t, err)
				}
				updated, err := svc.applyOpenAIFastPolicyToBody(context.Background(), account, "gpt-6-astra", body)
				require.NoError(t, err)
				require.Equal(t, "priority", gjson.GetBytes(updated, "service_tier").String())
				require.Equal(t, "hello", gjson.GetBytes(updated, "input").String())

				frame, err := sjson.SetBytes(body, "type", "response.create")
				require.NoError(t, err)
				updated, blocked, err := svc.applyOpenAIFastPolicyToWSResponseCreate(context.Background(), account, "gpt-6-astra", frame)
				require.NoError(t, err)
				require.Nil(t, blocked)
				require.Equal(t, "priority", gjson.GetBytes(updated, "service_tier").String())
			})
		}
	}
}

func TestOpenAIFastForcePriorityPreservesRuleBoundaries(t *testing.T) {
	force := OpenAIFastPolicyRule{
		ServiceTier: OpenAIFastTierPriority, Action: OpenAIFastPolicyActionForcePriority,
		Scope: BetaPolicyScopeOAuth, UserIDs: []int64{42}, ModelWhitelist: []string{"gpt-6*"},
		FallbackAction: BetaPolicyActionBlock, FallbackErrorMessage: "blocked model",
	}
	for _, tt := range []struct {
		name, accountType, model, tier, want string
		userID                               int64
		before                               []OpenAIFastPolicyRule
	}{
		{name: "matching user account model", accountType: AccountTypeOAuth, model: "gpt-6-astra", tier: "default", userID: 42, want: OpenAIFastPolicyActionForcePriority},
		{name: "different user", accountType: AccountTypeOAuth, model: "gpt-6-astra", tier: "default", userID: 43, want: BetaPolicyActionPass},
		{name: "different account type", accountType: AccountTypeAPIKey, model: "gpt-6-astra", tier: "default", userID: 42, want: BetaPolicyActionPass},
		{name: "fallback still requires tier match", accountType: AccountTypeOAuth, model: "gpt-5.5", tier: "default", userID: 42, want: BetaPolicyActionPass},
		{name: "matching fallback blocks", accountType: AccountTypeOAuth, model: "gpt-5.5", tier: "priority", userID: 42, want: BetaPolicyActionBlock},
		{name: "earlier matching rule wins", accountType: AccountTypeOAuth, model: "gpt-6-astra", tier: "flex", userID: 42, want: BetaPolicyActionFilter,
			before: []OpenAIFastPolicyRule{{ServiceTier: "flex", Action: BetaPolicyActionFilter, Scope: BetaPolicyScopeAll, UserIDs: []int64{42}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			settings := &OpenAIFastPolicySettings{Rules: append(tt.before, force)}
			action, _ := evaluateOpenAIFastPolicyWithSettings(settings, tt.userID, &Account{Platform: PlatformOpenAI, Type: tt.accountType}, tt.model, tt.tier)
			require.Equal(t, tt.want, action)
		})
	}

	t.Run("fallback force ignores tier only outside target models", func(t *testing.T) {
		rule := force
		rule.Action, rule.FallbackAction = BetaPolicyActionFilter, OpenAIFastPolicyActionForcePriority
		svc := newOpenAIGatewayServiceWithSettings(t, &OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{rule}})
		ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(42))
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
		for _, tier := range []string{OpenAIFastTierMissing, "default", "flex"} {
			action, _ := svc.evaluateOpenAIFastPolicy(ctx, account, "gpt-5.5", tier)
			require.Equal(t, OpenAIFastPolicyActionForcePriority, action)
			action, _ = svc.evaluateOpenAIFastPolicy(ctx, account, "gpt-6-astra", tier)
			require.Equal(t, BetaPolicyActionPass, action)
		}
	})

	t.Run("user exception precedes global force", func(t *testing.T) {
		rule := force
		rule.Scope, rule.UserIDs = BetaPolicyScopeAll, nil
		settings := &OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{rule, {
			ServiceTier: OpenAIFastTierMissing, Action: BetaPolicyActionPass, Scope: BetaPolicyScopeAll, UserIDs: []int64{42},
		}}}
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
		action, _ := evaluateOpenAIFastPolicyWithSettings(settings, 42, account, "gpt-6-astra", OpenAIFastTierMissing)
		require.Equal(t, BetaPolicyActionPass, action)
		action, _ = evaluateOpenAIFastPolicyWithSettings(settings, 43, account, "gpt-6-astra", OpenAIFastTierMissing)
		require.Equal(t, OpenAIFastPolicyActionForcePriority, action)
	})
}

func TestOpenAIFastForcePriorityOnlyTargetsOpenAIAccounts(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, &OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{{
		ServiceTier: OpenAIFastTierPriority, Action: OpenAIFastPolicyActionForcePriority, Scope: BetaPolicyScopeAll,
	}}})
	for _, account := range []*Account{nil, {Platform: PlatformGrok, Type: AccountTypeOAuth}, {Platform: PlatformAnthropic, Type: AccountTypeAPIKey}} {
		for _, tier := range []string{"", "default", "flex", "ultrafast"} {
			body := []byte(`{"model":"other-model","input":"hello"}`)
			if tier != "" {
				var err error
				body, err = sjson.SetBytes(body, "service_tier", tier)
				require.NoError(t, err)
			}
			updated, err := svc.applyOpenAIFastPolicyToBody(context.Background(), account, "other-model", body)
			require.NoError(t, err)
			require.JSONEq(t, string(body), string(updated), "account=%v tier=%s", account, tier)

			frame, err := sjson.SetBytes(body, "type", "response.create")
			require.NoError(t, err)
			updated, blocked, err := svc.applyOpenAIFastPolicyToWSResponseCreate(context.Background(), account, "other-model", frame)
			require.NoError(t, err)
			require.Nil(t, blocked)
			require.JSONEq(t, string(frame), string(updated), "account=%v tier=%s", account, tier)
		}
	}
}

func TestOpenAIFastForcePriorityForwardAndBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		for _, passthrough := range []bool{true, false} {
			for _, tier := range []string{"", "default", "flex"} {
				name := accountType + "/native/" + tier
				if passthrough {
					name = accountType + "/passthrough/" + tier
				}
				t.Run(name, func(t *testing.T) {
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
					c.Request.Header.Set("User-Agent", "codex_cli_rs/0.144.1")
					body := []byte(`{"model":"gpt-6-astra-raw","stream":true,"instructions":"test","input":[{"role":"user","content":"hi"}]}`)
					if tier != "" {
						var err error
						body, err = sjson.SetBytes(body, "service_tier", tier)
						require.NoError(t, err)
					}
					observed := "priority"
					if accountType == AccountTypeOAuth {
						observed = "default"
					}
					upstream := &httpUpstreamRecorder{resp: &http.Response{
						StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
						Body: io.NopCloser(strings.NewReader(`data: {"type":"response.completed","response":{"id":"resp_fast","model":"gpt-6-astra","status":"completed","service_tier":"` + observed + `","output":[],"usage":{"input_tokens":2,"output_tokens":1}}}` + "\n\ndata: [DONE]\n\n")),
					}}
					svc := newOpenAIGatewayServiceWithSettings(t, &OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{{
						ServiceTier: OpenAIFastTierPriority, Action: OpenAIFastPolicyActionForcePriority, Scope: BetaPolicyScopeAll,
						ModelWhitelist: []string{"gpt-6-astra*"},
					}}})
					svc.cfg, svc.httpUpstream = &config.Config{}, upstream
					account := &Account{
						ID: 123, Platform: PlatformOpenAI, Type: accountType, Concurrency: 1,
						Credentials: map[string]any{"api_key": "test-key", "access_token": "test-token", "chatgpt_account_id": "test-account",
							"model_mapping": map[string]any{"gpt-6-astra-raw": "gpt-6-astra"}},
						Extra:  map[string]any{"openai_passthrough": passthrough, "openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModeOff},
						Status: StatusActive, Schedulable: true, RateMultiplier: f64p(1),
					}
					result, err := svc.Forward(context.Background(), c, account, body)
					require.NoError(t, err)
					expectedModel := "gpt-6-astra"
					if passthrough {
						expectedModel = "gpt-6-astra-raw"
					}
					require.Equal(t, expectedModel, gjson.GetBytes(upstream.lastBody, "model").String())
					require.Equal(t, "priority", gjson.GetBytes(upstream.lastBody, "service_tier").String())
					require.NotNil(t, result.ServiceTier)
					require.Equal(t, "priority", *result.ServiceTier)
					require.Equal(t, observed, result.UpstreamResponseServiceTier)
					require.Equal(t, "priority", ApplyOpenAIServiceTierBillingResolution(account, result).Billing)
				})
			}
		}
	}
}
