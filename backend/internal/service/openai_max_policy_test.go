//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newOpenAIMaxPolicyTestSettings(t *testing.T, action, scope, effort string) *SettingService {
	t.Helper()
	svc := NewSettingService(&openAIFastPolicyRepoStub{values: map[string]string{}}, &config.Config{})
	require.NoError(t, svc.SetOpenAIMaxPolicySettings(context.Background(), &OpenAIMaxPolicySettings{
		Action: action, Scope: scope, DowngradeEffort: effort, ErrorMessage: "请降低 OpenAI 推理档位",
	}))
	return svc
}

func TestOpenAIMaxPolicySettings(t *testing.T) {
	ctx := context.Background()
	repo := &openAIFastPolicyRepoStub{values: map[string]string{}}
	svc := NewSettingService(repo, &config.Config{})
	got, err := svc.GetOpenAIMaxPolicySettings(ctx)
	require.NoError(t, err)
	require.Equal(t, DefaultOpenAIMaxPolicySettings(), got)
	for _, settings := range []*OpenAIMaxPolicySettings{
		nil, {Action: "filter", Scope: "all"}, {Action: "pass", Scope: "bedrock"},
		{Action: "downgrade", Scope: "all"}, {Action: "downgrade", Scope: "all", DowngradeEffort: "max"},
	} {
		require.Error(t, svc.SetOpenAIMaxPolicySettings(ctx, settings))
	}
	require.Empty(t, repo.values)
	repo.values[SettingKeyOpenAIMaxPolicySettings] = `{broken`
	_, err = svc.GetOpenAIMaxPolicySettings(ctx)
	require.Error(t, err)
}

func TestOpenAIMaxPolicyActionsAndScopes(t *testing.T) {
	ctx := context.Background()
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	body := []byte(`{ "reasoning":{"effort":"max","summary":"auto"},"reasoning_effort":"MAX","output_config":{"effort":"max"},"input":"max", "service_tier":"priority" }`)
	for _, action := range []string{"pass", "block", "downgrade"} {
		for _, effort := range []string{"low", "medium", "high", "xhigh"} {
			svc := &OpenAIGatewayService{settingService: newOpenAIMaxPolicyTestSettings(t, action, "all", effort)}
			got, err := svc.applyOpenAIMaxPolicyToBody(ctx, account, body)
			if action == "block" {
				var blocked *OpenAIMaxBlockedError
				require.ErrorAs(t, err, &blocked)
				require.Equal(t, "请降低 OpenAI 推理档位", blocked.Message)
				continue
			}
			require.NoError(t, err)
			if action == "pass" {
				require.Equal(t, body, got)
				continue
			}
			for _, path := range []string{"reasoning.effort", "reasoning_effort", "output_config.effort"} {
				require.Equal(t, effort, gjson.GetBytes(got, path).String())
			}
			for _, path := range []string{"input", "service_tier", "reasoning.summary"} {
				require.Equal(t, gjson.GetBytes(body, path).Raw, gjson.GetBytes(got, path).Raw)
			}
		}
	}
	for _, scope := range []string{"all", "oauth", "apikey"} {
		svc := &OpenAIGatewayService{settingService: newOpenAIMaxPolicyTestSettings(t, "block", scope, "high")}
		for _, typ := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken} {
			account.Type = typ
			_, err := svc.applyOpenAIMaxPolicyToBody(ctx, account, body)
			wantBlock := scope == "all" || (scope == "oauth" && typ != AccountTypeAPIKey) || (scope == "apikey" && typ == AccountTypeAPIKey)
			require.Equal(t, wantBlock, err != nil, "%s/%s", scope, typ)
		}
		account.Type = AccountTypeAPIKey
		for _, body := range []string{`{}`, `{"reasoning":{"effort":"xhigh"}}`, `{"reasoning":{"effort":"high"}}`, `{"input":"max"}`} {
			got, err := svc.applyOpenAIMaxPolicyToBody(ctx, account, []byte(body))
			require.NoError(t, err)
			require.Equal(t, body, string(got))
		}
		_, err := svc.applyOpenAIMaxPolicyToBody(ctx, &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}, body)
		require.NoError(t, err)
	}
}

type openAIMaxPolicyEndpoint struct {
	path, body string
	call       func(*OpenAIGatewayService, context.Context, *gin.Context, *Account, []byte) (*OpenAIForwardResult, error)
}

func openAIMaxPolicyEndpoints() []openAIMaxPolicyEndpoint {
	return []openAIMaxPolicyEndpoint{
		{"/v1/responses", `{"model":"gpt-5.6-sol","input":"hello","reasoning":{"effort":"max"}}`, (*OpenAIGatewayService).Forward},
		{"/v1/chat/completions", `{"model":"gpt-5.6-sol","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"max"}`, func(s *OpenAIGatewayService, ctx context.Context, c *gin.Context, a *Account, b []byte) (*OpenAIForwardResult, error) {
			return s.ForwardAsChatCompletions(ctx, c, a, b, "", "")
		}},
		{"/v1/messages", `{"model":"gpt-5.6-sol","messages":[{"role":"user","content":"hello"}],"max_tokens":1024,"output_config":{"effort":"max"}}`, func(s *OpenAIGatewayService, ctx context.Context, c *gin.Context, a *Account, b []byte) (*OpenAIForwardResult, error) {
			return s.ForwardAsAnthropic(ctx, c, a, b, "", "")
		}},
	}
}

func TestOpenAIMaxPolicyHTTPBlock(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, ep := range openAIMaxPolicyEndpoints() {
		for _, heartbeat := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/heartbeat=%t", ep.path, heartbeat), func(t *testing.T) {
				svc := &OpenAIGatewayService{settingService: newOpenAIMaxPolicyTestSettings(t, "block", "all", "high")}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, ep.path, strings.NewReader(ep.body))
				if heartbeat {
					c.Header("Content-Type", "text/event-stream")
					_, _ = c.Writer.WriteString(":\n\n")
					c.Writer.Flush()
				}
				_, err := ep.call(svc, context.Background(), c, &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, []byte(ep.body))
				var blocked *OpenAIMaxBlockedError
				require.ErrorAs(t, err, &blocked)
				require.Contains(t, rec.Body.String(), blocked.Message)
				require.True(t, IsResponseCommitted(c))
				if heartbeat {
					require.Equal(t, http.StatusOK, rec.Code)
					if ep.path == "/v1/responses" {
						require.Contains(t, rec.Body.String(), "event: response.failed")
					} else {
						require.Contains(t, rec.Body.String(), "data: ")
					}
				} else {
					require.Equal(t, http.StatusBadRequest, rec.Code)
					require.Equal(t, blocked.Message, gjson.Get(rec.Body.String(), "error.message").String())
				}
			})
		}
	}
}

func TestOpenAIMaxPolicyHTTPDowngrade(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, ep := range openAIMaxPolicyEndpoints() {
		for _, passthrough := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/passthrough=%t", ep.path, passthrough), func(t *testing.T) {
				response := `{"id":"resp_test","model":"gpt-5.6-sol","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
				contentType := "application/json"
				if ep.path != "/v1/responses" {
					response = "data: {\"type\":\"response.completed\",\"response\":" + response + "}\n\ndata: [DONE]\n\n"
					contentType = "text/event-stream"
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(response))}}
				svc := newOpenAIImageGenerationControlTestService(upstream)
				svc.settingService = newOpenAIMaxPolicyTestSettings(t, "downgrade", "all", "medium")
				c, _ := newOpenAIImageGenerationControlTestContext(true, "unit-test")
				c.Request = httptest.NewRequest(http.MethodPost, ep.path, strings.NewReader(ep.body))
				account := newOpenAIImageGenerationControlTestAccount()
				account.Extra = map[string]any{"openai_passthrough": passthrough, "openai_responses_supported": true}
				result, err := ep.call(svc, context.Background(), c, account, []byte(ep.body))
				require.NoError(t, err)
				require.Equal(t, "medium", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
				require.NotNil(t, result.ReasoningEffort)
				require.Equal(t, "medium", *result.ReasoningEffort)
			})
		}
	}
}

func TestOpenAIMaxPolicyRawChatDowngrade(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"sol","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"max","stream":false}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"id":"chatcmpl_max","object":"chat.completion","model":"gpt-5.6-sol","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)),
	}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream,
		settingService: newOpenAIMaxPolicyTestSettings(t, "downgrade", "all", "medium")}
	account := rawChatCompletionsTestAccount()
	account.Extra = map[string]any{"openai_responses_supported": false}
	account.Credentials["model_mapping"] = map[string]any{"sol": "gpt-5.6-sol"}
	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.Equal(t, "medium", gjson.GetBytes(upstream.lastBody, "reasoning_effort").String())
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "medium", *result.ReasoningEffort)
}

func TestOpenAIMaxPolicyMessagesGroupMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, scenario := range []struct {
		action  string
		rawChat bool
	}{{"block", false}, {"downgrade", false}, {"block", true}, {"downgrade", true}} {
		t.Run(fmt.Sprintf("%s/raw_chat=%t", scenario.action, scenario.rawChat), func(t *testing.T) {
			action := scenario.action
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"model\":\"gpt-5.6-sol\",\"output\":[]}}\n\n")),
			}}
			account := newOpenAIImageGenerationControlTestAccount()
			effortPath := "reasoning.effort"
			if scenario.rawChat {
				account.Extra = map[string]any{"openai_responses_supported": false}
				upstream.resp.Header.Set("Content-Type", "application/json")
				upstream.resp.Body = io.NopCloser(strings.NewReader(`{"id":"chatcmpl_test","model":"gpt-5.6-sol","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
				effortPath = "reasoning_effort"
			}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			svc.settingService = newOpenAIMaxPolicyTestSettings(t, action, "all", "medium")
			c, rec := newOpenAIImageGenerationControlTestContext(true, "unit-test")
			body := []byte(`{"model":"gpt-5.6-sol","messages":[{"role":"user","content":"hello"}],"max_tokens":1024,"output_config":{"effort":"medium"}}`)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(body)))
			ctx := WithOpenAIReasoningEffortPolicy(context.Background(), "", []ReasoningEffortMapping{{From: "medium", To: "max"}}, "")
			result, err := svc.ForwardAsAnthropic(ctx, c, account, body, "", "")
			if action == "block" {
				var blocked *OpenAIMaxBlockedError
				require.ErrorAs(t, err, &blocked)
				require.Equal(t, http.StatusBadRequest, rec.Code)
				require.Empty(t, upstream.lastBody)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "medium", gjson.GetBytes(upstream.lastBody, effortPath).String())
			require.NotNil(t, result.ReasoningEffort)
			require.Equal(t, "medium", *result.ReasoningEffort)
		})
	}
}
