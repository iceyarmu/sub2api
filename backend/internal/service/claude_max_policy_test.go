//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newClaudeMaxPolicyTestService(t *testing.T, action, effort string) *GatewayService {
	t.Helper()
	settings := DefaultClaudeMaxPolicySettings()
	settings.Action, settings.DowngradeEffort = action, effort
	repo := &openAIFastPolicyRepoStub{values: map[string]string{}}
	settingService := NewSettingService(repo, &config.Config{})
	require.NoError(t, settingService.SetClaudeMaxPolicySettings(context.Background(), settings))
	return &GatewayService{settingService: settingService, cfg: &config.Config{}, tlsFPProfileService: &TLSFingerprintProfileService{}}
}

func TestClaudeMaxPolicySettings(t *testing.T) {
	ctx := context.Background()
	repo := &openAIFastPolicyRepoStub{values: map[string]string{}}
	svc := NewSettingService(repo, &config.Config{})
	settings, err := svc.GetClaudeMaxPolicySettings(ctx)
	require.NoError(t, err)
	require.Equal(t, DefaultClaudeMaxPolicySettings(), settings)
	settings.Action, settings.ErrorMessage = BetaPolicyActionBlock, "请降低推理档位"
	require.NoError(t, svc.SetClaudeMaxPolicySettings(ctx, settings))
	stored, err := svc.GetClaudeMaxPolicySettings(ctx)
	require.NoError(t, err)
	require.Equal(t, settings, stored)
	for _, tc := range []ClaudeMaxPolicySettings{
		{Action: "filter", Scope: "all", DowngradeEffort: "high"},
		{Action: "pass", Scope: "unknown", DowngradeEffort: "high"},
		{Action: "downgrade", Scope: "all", DowngradeEffort: "max"},
		{Action: "downgrade", Scope: "all"},
	} {
		require.Error(t, svc.SetClaudeMaxPolicySettings(ctx, &tc))
	}
	stored, err = svc.GetClaudeMaxPolicySettings(ctx)
	require.NoError(t, err)
	require.Equal(t, settings, stored, "invalid writes must not replace the saved policy")
	require.Error(t, svc.SetClaudeMaxPolicySettings(ctx, nil))
	repo.values[SettingKeyClaudeMaxPolicySettings] = `{broken`
	_, err = svc.GetClaudeMaxPolicySettings(ctx)
	require.Error(t, err, "corrupt configuration must not silently disable the policy")
}

func TestClaudeMaxPolicyActions(t *testing.T) {
	account := &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}
	body := []byte(`{ "model":"claude-opus-4-6", "output_config":{"effort":"max","format":{"type":"json_schema"}},"max_tokens":321,"thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"max"},{"role":"system","output_config":{"effort":"max"}},{"role":"system","output_config":{"effort":"medium"}}] }`)
	pass := newClaudeMaxPolicyTestService(t, "pass", "high")
	got, err := pass.applyClaudeMaxPolicyToBody(context.Background(), account, body)
	require.NoError(t, err)
	require.Equal(t, body, got, "pass must be a byte-for-byte no-op")
	for _, target := range []string{"low", "medium", "high", "xhigh"} {
		svc := newClaudeMaxPolicyTestService(t, "downgrade", target)
		got, err = svc.applyClaudeMaxPolicyToBody(context.Background(), account, body)
		require.NoError(t, err)
		require.Equal(t, target, gjson.GetBytes(got, "output_config.effort").String())
		require.Equal(t, target, gjson.GetBytes(got, "messages.1.output_config.effort").String())
		require.Equal(t, "medium", gjson.GetBytes(got, "messages.2.output_config.effort").String())
		for _, path := range []string{"model", "max_tokens", "thinking", "output_config.format", "messages.0"} {
			require.Equal(t, gjson.GetBytes(body, path).Raw, gjson.GetBytes(got, path).Raw)
		}
	}
	block := newClaudeMaxPolicyTestService(t, "block", "high")
	_, err = block.applyClaudeMaxPolicyToBody(context.Background(), account, body)
	var blocked *ClaudeMaxBlockedError
	require.ErrorAs(t, err, &blocked)
	require.Equal(t, "Claude Max effort is not allowed", blocked.Message)
	settings := &ClaudeMaxPolicySettings{Action: "block", Scope: "all", ErrorMessage: "请降低推理档位"}
	require.NoError(t, block.settingService.SetClaudeMaxPolicySettings(context.Background(), settings))
	_, err = block.applyClaudeMaxPolicyToBody(context.Background(), account, []byte(`{"messages":[{"output_config":{"effort":"MAX"}}]}`))
	require.ErrorAs(t, err, &blocked)
	require.Equal(t, settings.ErrorMessage, blocked.Message)
	for _, body := range []string{`{}`, `{"output_config":{"effort":"xhigh"}}`, `{"output_config":{"effort":123}}`, `{"messages":[{"content":"max"}]}`} {
		got, err := block.applyClaudeMaxPolicyToBody(context.Background(), account, []byte(body))
		require.NoError(t, err)
		require.Equal(t, body, string(got))
	}
}

func TestClaudeMaxPolicyScopes(t *testing.T) {
	ctx := context.Background()
	svc := newClaudeMaxPolicyTestService(t, "block", "high")
	for _, scope := range []string{"all", "oauth", "apikey", "bedrock"} {
		settings := &ClaudeMaxPolicySettings{Action: "block", Scope: scope}
		require.NoError(t, svc.settingService.SetClaudeMaxPolicySettings(ctx, settings))
		for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey, AccountTypeServiceAccount, AccountTypeBedrock} {
			account := &Account{Platform: PlatformAnthropic, Type: accountType}
			_, err := svc.applyClaudeMaxPolicyToBody(ctx, account, []byte(`{"output_config":{"effort":"max"}}`))
			matches := scope == "all" || (scope == "oauth" && account.IsOAuth()) || (scope == "bedrock" && account.IsBedrock()) || (scope == "apikey" && !account.IsOAuth() && !account.IsBedrock())
			require.Equal(t, matches, err != nil, "%s/%s", scope, accountType)
		}
	}
	for _, account := range []*Account{nil, {Platform: PlatformOpenAI, Type: AccountTypeAPIKey}} {
		_, err := svc.applyClaudeMaxPolicyToBody(ctx, account, []byte(`{"output_config":{"effort":"max"}}`))
		require.NoError(t, err)
	}
}

func TestClaudeMaxPolicyRequestBuilders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"apikey", "passthrough", "oauth", "vertex"} {
		for _, action := range []string{"pass", "block", "downgrade"} {
			t.Run(path+"/"+action, func(t *testing.T) {
				svc := newClaudeMaxPolicyTestService(t, action, "medium")
				account := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}
				if path == "oauth" {
					account.Type = AccountTypeOAuth
				}
				if path == "vertex" {
					account = newVertexServiceAccount(1)
				}
				body := []byte(`{"model":"claude-opus-4-6","max_tokens":100,"output_config":{"effort":"max"},"messages":[{"role":"user","content":"hello"}]}`)
				c := newVertexBetaTestContext(t, "")
				var req *http.Request
				var forwarded []byte
				var err error
				if path == "passthrough" {
					req, forwarded, err = svc.buildUpstreamRequestAnthropicAPIKeyPassthrough(context.Background(), c, account, body, "token")
				} else {
					req, forwarded, err = svc.buildUpstreamRequest(context.Background(), c, account, body, "token", "apikey", "claude-opus-4-6", false, false)
				}
				if action == "block" {
					var blocked *ClaudeMaxBlockedError
					require.ErrorAs(t, err, &blocked)
					require.Nil(t, req)
					return
				}
				require.NoError(t, err)
				wire, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				want := "max"
				if action == "downgrade" {
					want = "medium"
				}
				require.Equal(t, want, gjson.GetBytes(wire, "output_config.effort").String())
				require.Equal(t, want, gjson.GetBytes(forwarded, "output_config.effort").String())
			})
		}
	}
}

func TestClaudeMaxPolicyBedrock(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range []string{"claude-sonnet-5-5", "claude-opus-4-6"} {
		for _, action := range []string{"pass", "block", "downgrade"} {
			svc := newClaudeMaxPolicyTestService(t, action, "high")
			body := []byte(`{"model":"` + model + `","output_config":{"effort":"max"},"messages":[{"role":"user","content":"hello"}]}`)
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
			require.NoError(t, err)
			account := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeBedrock, Credentials: map[string]any{"auth_mode": "apikey", "api_key": "token", "aws_region": "us-east-1"}}
			upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"type":"message","content":[],"usage":{"input_tokens":1,"output_tokens":1}}`))}}}
			svc.httpUpstream = upstream
			_, err = svc.forwardBedrock(context.Background(), newVertexBetaTestContext(t, ""), account, parsed, time.Now())
			if action == "block" {
				var blocked *ClaudeMaxBlockedError
				require.ErrorAs(t, err, &blocked)
				require.Empty(t, upstream.requestBodies)
			} else {
				require.NoError(t, err)
				require.Len(t, upstream.requestBodies, 1)
				if action == "pass" {
					require.Equal(t, body, parsed.Body.Bytes(), "pass must not rewrite the parsed request")
					require.Equal(t, "max", parsed.OutputEffort)
					continue
				}
				wantEffort := "high"
				if model == "claude-opus-4-6" {
					wantEffort = ""
				}
				require.Equal(t, wantEffort, gjson.GetBytes(upstream.requestBodies[0], "output_config.effort").String())
				require.Equal(t, wantEffort, parsed.OutputEffort, "billing must match the supported wire effort")
				require.Equal(t, model, parsed.Model)
				require.Equal(t, wantEffort, gjson.GetBytes(parsed.Body.Bytes(), "output_config.effort").String())
			}
		}
	}
}

func TestClaudeMaxPolicyMessagesForwarding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		for _, action := range []string{"block", "downgrade"} {
			t.Run(action+"/passthrough="+strconv.FormatBool(passthrough), func(t *testing.T) {
				svc := newClaudeMaxPolicyTestService(t, action, "medium")
				svc.rateLimitService = &RateLimitService{}
				body := []byte(`{"model":"claude-opus-4-6","max_tokens":1024,"output_config":{"effort":"max"},"messages":[{"role":"user","content":"hello"}]}`)
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
				require.NoError(t, err)
				account := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "token"}, Extra: map[string]any{"anthropic_passthrough": passthrough}}
				upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"msg_1","type":"message","content":[],"usage":{"input_tokens":1,"output_tokens":1}}`))}}}
				svc.httpUpstream = upstream
				_, err = svc.Forward(context.Background(), newVertexBetaTestContext(t, ""), account, parsed)
				if action == "block" {
					var blocked *ClaudeMaxBlockedError
					require.ErrorAs(t, err, &blocked)
					require.Empty(t, upstream.requestBodies)
					return
				}
				require.NoError(t, err)
				require.Len(t, upstream.requestBodies, 1)
				require.Equal(t, "medium", gjson.GetBytes(upstream.requestBodies[0], "output_config.effort").String())
				require.Equal(t, "medium", parsed.OutputEffort, "usage recording must see the downgraded effort")
			})
		}
	}
}

func TestClaudeMaxPolicyCompatForwarding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"responses", "chat_completions"} {
		for _, stream := range []bool{false, true} {
			for _, action := range []string{"block", "downgrade"} {
				t.Run(endpoint+"/"+action+"/stream="+strconv.FormatBool(stream), func(t *testing.T) {
					svc := newClaudeMaxPolicyTestService(t, action, "high")
					request := map[string]any{"model": "claude-fable-5-1", "stream": stream}
					if endpoint == "responses" {
						request["input"] = "hello"
						request["reasoning"] = map[string]string{"effort": "xhigh"}
					} else {
						request["messages"] = []map[string]string{{"role": "user", "content": "hello"}}
						request["reasoning_effort"] = "xhigh"
					}
					body, err := json.Marshal(request)
					require.NoError(t, err)
					upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(namespaceToolAnthropicStream()))}}}
					svc.httpUpstream = upstream
					account := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-key"}}
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, strings.NewReader(string(body)))
					var result *ForwardResult
					if endpoint == "responses" {
						result, err = svc.ForwardAsResponses(context.Background(), c, account, body, nil)
					} else {
						result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, nil)
					}
					if action == "block" {
						var blocked *ClaudeMaxBlockedError
						require.True(t, errors.As(err, &blocked))
						require.Empty(t, upstream.requestBodies)
						return
					}
					require.NoError(t, err)
					require.Len(t, upstream.requestBodies, 1)
					require.Equal(t, "high", gjson.GetBytes(upstream.requestBodies[0], "output_config.effort").String())
					require.Equal(t, "high", *result.ReasoningEffort)
				})
			}
		}
	}
}
