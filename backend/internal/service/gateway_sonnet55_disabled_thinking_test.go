//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSonnet55DisabledThinkingForwarding(t *testing.T) {
	for _, accountType := range []string{AccountTypeSetupToken, AccountTypeOAuth, AccountTypeAPIKey} {
		for _, endpoint := range []string{"messages", "count_tokens", "messages_passthrough", "count_tokens_passthrough"} {
			if strings.HasSuffix(endpoint, "_passthrough") && accountType != AccountTypeAPIKey {
				continue
			}
			for _, mapping := range []string{"account", "group", "direct", "unmapped", "mapped_away", "short_id", "dated_id"} {
				if (mapping == "short_id" || mapping == "dated_id") && accountType == AccountTypeAPIKey {
					continue
				}
				for _, userAgent := range []string{"claude-cli/2.1.258 (external, cli)", "test-client", "Go-http-client/1.1"} {
					t.Run(accountType+"/"+endpoint+"/"+mapping+"/"+userAgent, func(t *testing.T) {
						ctx := context.Background()
						model, target, thinking := "claude-sonnet-5", "claude-sonnet-5-5", "between_tools"
						account := &Account{ID: 1, Platform: PlatformAnthropic, Type: accountType,
							Credentials: map[string]any{"api_key": "test-key", "access_token": "test-token"},
						}
						if strings.HasSuffix(endpoint, "_passthrough") {
							account.Extra = map[string]any{"anthropic_passthrough": true}
						}
						switch mapping {
						case "account":
							account.Credentials["model_mapping"] = map[string]any{model: target}
						case "group":
							ctx = context.WithValue(ctx, ctxkey.Group, &Group{Platform: PlatformAnthropic, ModelMapping: map[string]string{model: target}})
							ctx = context.WithValue(ctx, ctxkey.Model, model)
							account.Credentials["model_mapping"] = map[string]any{target: model}
							model = target // The handler applies group mapping before forwarding.
						case "direct":
							model = target
						case "unmapped":
							target, thinking = model, "disabled"
						case "mapped_away":
							model, target, thinking = target, model, "disabled"
							account.Credentials["model_mapping"] = map[string]any{model: target}
						case "short_id", "dated_id":
							model = "claude-sonnet-4-5"
							key := model
							if mapping == "dated_id" {
								key = claude.NormalizeModelID(model)
							}
							account.Credentials["model_mapping"] = map[string]any{key: target}
							mimic := userAgent == "test-client" || (userAgent == "Go-http-client/1.1" && strings.HasPrefix(endpoint, "count_tokens"))
							if (mapping == "short_id") == mimic {
								target, thinking = claude.NormalizeModelID(model), "disabled"
							}
						}
						body := []byte(`{"model":"` + model + `","metadata":{"user_id":"{\"device_id\":\"test-device\",\"session_id\":\"test-session\"}"},"max_tokens":100,"thinking":{"type":"disabled"},"output_config":{"effort":"high"},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"signed-history"},{"type":"text","text":"previous answer"}]},{"role":"user","content":"hello"}]}`)
						if userAgent == "Go-http-client/1.1" {
							body, _ = setJSONRawBytes(body, "system", []byte(`[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.258.abc; cc_entrypoint=cli;"}]`))
							require.True(t, systemHasBillingAttributionBlock(body))
						}
						original := bytes.Clone(body)
						response := `{"id":"msg_test","type":"message","role":"assistant","model":"` + target + `","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":2}}`
						if strings.HasPrefix(endpoint, "count_tokens") {
							response = `{"input_tokens":10}`
						}
						upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(response))}}
						svc := newClaudeMaxPolicyTestService(t, BetaPolicyActionPass, "high")
						svc.httpUpstream = upstream
						c, _ := gin.CreateTestContext(httptest.NewRecorder())
						c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx)
						c.Request.Header.Set("User-Agent", userAgent)
						parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
						require.NoError(t, err)
						require.Equal(t, strings.HasPrefix(userAgent, "claude-cli/"), isClaudeCodeClient(userAgent, parsed.MetadataUserID))
						if strings.HasPrefix(endpoint, "count_tokens") {
							err = svc.ForwardCountTokens(ctx, c, account, parsed)
						} else {
							_, err = svc.Forward(ctx, c, account, parsed)
						}
						require.NoError(t, err)
						require.Equal(t, target, gjson.GetBytes(upstream.lastBody, "model").String())
						require.Equal(t, thinking, gjson.GetBytes(upstream.lastBody, "thinking.type").String())
						if thinking == "between_tools" {
							require.Equal(t, "signed-history", gjson.GetBytes(upstream.lastBody, "messages.0.content.0.signature").String())
						}
						require.Equal(t, original, body, "must not mutate the shared request buffer")
					})
				}
			}
		}
	}
}

func TestSonnet55MappedThinkingValidation(t *testing.T) {
	for _, accountType := range []string{AccountTypeSetupToken, AccountTypeOAuth, AccountTypeAPIKey} {
		for _, countTokens := range []bool{false, true} {
			for _, fields := range []string{
				`"thinking":{"type":"disabled"},"output_config":{"effort":"xhigh"}`,
				`"thinking":{"type":"disabled","budget_tokens":1024}`,
				`"thinking":{"type":"disabled"},"tool_choice":{"type":"any"}`,
			} {
				account := &Account{Platform: PlatformAnthropic, Type: accountType, Credentials: map[string]any{
					"model_mapping": map[string]any{"claude-sonnet-5": "claude-sonnet-5-5"},
				}}
				body := []byte(`{"model":"claude-sonnet-5","messages":[{"role":"user","content":"hello"}],` + fields + `}`)
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
				require.NoError(t, err)
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				svc := &GatewayService{} // Invalid settings must be rejected before contacting upstream.
				if countTokens {
					err = svc.ForwardCountTokens(context.Background(), c, account, parsed)
				} else {
					_, err = svc.Forward(context.Background(), c, account, parsed)
				}
				require.Error(t, err)
				require.Equal(t, http.StatusBadRequest, recorder.Code, fields)
			}
		}
	}
}
