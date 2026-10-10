//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestForwardAsChatCompletions_GlobalCacheTTL1hInjection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey} {
		for _, enabled := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/enabled=%t/stream=%t", accountType, enabled, stream), func(t *testing.T) {
					resetGatewayForwardingSettingsCacheForTest(t)
					cfg := &config.Config{}
					upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
						Body:       io.NopCloser(strings.NewReader(namespaceToolAnthropicStream())),
					}}
					svc := &GatewayService{cfg: cfg, httpUpstream: upstream,
						settingService: NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{
							SettingKeyEnableAnthropicCacheTTL1hInjection: fmt.Sprint(enabled),
							SettingKeyRewriteMessageCacheControl:         "false",
						}}, cfg),
					}
					account := &Account{ID: 1, Platform: PlatformAnthropic, Type: accountType,
						Credentials: map[string]any{"access_token": "test-token", "api_key": "test-key"},
					}
					body := []byte(fmt.Sprintf(`{"model":"claude-haiku-4-5-20251001","stream":%t,"max_tokens":32,
						"messages":[{"role":"system","content":"Be brief."},{"role":"user","content":"Hello"}],
						"tools":[{"type":"function","function":{"name":"probe","parameters":{"type":"object"}}}]}`, stream))
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))
					_, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, nil)
					require.NoError(t, err)
					require.Equal(t, "/v1/messages", upstream.lastReq.URL.Path)
					_, messages, tools, system := collectCacheControlPaths(upstream.lastBody)
					paths := append(append(tools, system...), messages...)
					if accountType == AccountTypeAPIKey {
						require.Empty(t, paths, "OAuth TTL injection must not add API-key cache breakpoints")
						return
					}
					require.NotEmpty(t, tools, "OAuth mimicry must provide a tool cache breakpoint")
					require.NotEmpty(t, system, "OAuth mimicry must provide a system cache breakpoint")
					wantTTL := "5m"
					if enabled {
						wantTTL = "1h"
						require.NotEmpty(t, messages, "1h injection must also cache the conversation without client cache hints")
					}
					for _, path := range paths {
						require.Equal(t, wantTTL, gjson.GetBytes(upstream.lastBody, path+".ttl").String(), path)
					}
				})
			}
		}
	}
}

func TestForwardAsChatCompletions_CacheTTL1hWithoutOtherCacheInjection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, enabled := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("enabled=%t/stream=%t", enabled, stream), func(t *testing.T) {
				resetGatewayForwardingSettingsCacheForTest(t)
				cfg := &config.Config{}
				upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader(namespaceToolAnthropicStream())),
				}}
				svc := &GatewayService{cfg: cfg, httpUpstream: upstream,
					settingService: NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{
						SettingKeyEnableAnthropicCacheTTL1hInjection:     fmt.Sprint(enabled),
						SettingKeyEnableClaudeOAuthSystemPromptInjection: "false",
						SettingKeyRewriteMessageCacheControl:             "false",
					}}, cfg),
				}
				account := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeSetupToken,
					Credentials: map[string]any{"access_token": "test-token"},
				}
				body := []byte(fmt.Sprintf(`{"model":"claude-haiku-4-5-20251001","stream":%t,"messages":[{"role":"user","content":"Hello"}]}`, stream))
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))
				_, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, nil)
				require.NoError(t, err)
				_, messages, tools, system := collectCacheControlPaths(upstream.lastBody)
				require.Empty(t, tools)
				require.Empty(t, system)
				if enabled {
					require.Equal(t, []string{"messages.0.content.0.cache_control"}, messages)
					require.Equal(t, "ephemeral", gjson.GetBytes(upstream.lastBody, messages[0]+".type").String())
					require.Equal(t, "1h", gjson.GetBytes(upstream.lastBody, messages[0]+".ttl").String())
				} else {
					require.Empty(t, messages)
				}
			})
		}
	}
}

func TestHandleCCBufferedFromAnthropic_ToolArgumentsAreValidJSON(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_tool","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4.5","usage":{"input_tokens":10}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"Paris\"}"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`,
		``,
	}, "\n")))}

	_, err := (&GatewayService{}).handleCCBufferedFromAnthropic(resp, c, "gpt-5", "claude-sonnet-4.5", nil, time.Now())
	require.NoError(t, err)

	var body struct {
		Choices []struct {
			Message struct {
				ToolCalls []struct {
					Function struct {
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Choices, 1)
	require.Len(t, body.Choices[0].Message.ToolCalls, 1)
	args := body.Choices[0].Message.ToolCalls[0].Function.Arguments
	require.JSONEq(t, `{"city":"Paris"}`, args)
}

func TestExtractCCReasoningEffortFromBody(t *testing.T) {
	t.Parallel()

	t.Run("nested reasoning.effort", func(t *testing.T) {
		got := extractCCReasoningEffortFromBody([]byte(`{"reasoning":{"effort":"HIGH"}}`))
		require.NotNil(t, got)
		require.Equal(t, "high", *got)
	})

	t.Run("flat reasoning_effort", func(t *testing.T) {
		got := extractCCReasoningEffortFromBody([]byte(`{"reasoning_effort":"x-high"}`))
		require.NotNil(t, got)
		require.Equal(t, "xhigh", *got)
	})

	t.Run("DeepSeek max", func(t *testing.T) {
		got := extractCCReasoningEffortFromBody([]byte(`{"model":"deepseek-v4-flash","reasoning_effort":"Max"}`))
		require.NotNil(t, got)
		require.Equal(t, "max", *got)
	})

	t.Run("mapped Kimi alias max", func(t *testing.T) {
		got := extractCCReasoningEffortFromBody(
			[]byte(`{"model":"public-alias","reasoning_effort":"max"}`),
			"kimi-k3",
			"public-alias",
		)
		require.NotNil(t, got)
		require.Equal(t, "max", *got)
	})

	t.Run("legacy model max", func(t *testing.T) {
		got := extractCCReasoningEffortFromBody([]byte(`{"model":"gpt-5.5","reasoning_effort":"max"}`))
		require.NotNil(t, got)
		require.Equal(t, "xhigh", *got)
	})

	t.Run("missing effort", func(t *testing.T) {
		require.Nil(t, extractCCReasoningEffortFromBody([]byte(`{"model":"gpt-5"}`)))
	})
}

func TestHandleCCBufferedFromAnthropic_PreservesMessageStartCacheUsageAndReasoning(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	reasoningEffort := "high"
	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_cc_buffered"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4.5","stop_reason":"","usage":{"input_tokens":12,"cache_read_input_tokens":9,"cache_creation_input_tokens":3}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"hello"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`,
			``,
		}, "\n"))),
	}

	svc := &GatewayService{}
	result, err := svc.handleCCBufferedFromAnthropic(resp, c, "gpt-5", "claude-sonnet-4.5", &reasoningEffort, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	require.Equal(t, 9, result.Usage.CacheReadInputTokens)
	require.Equal(t, 3, result.Usage.CacheCreationInputTokens)
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "high", *result.ReasoningEffort)
}

// Kimi 等 Anthropic 兼容上游返回 SSE 紧凑格式（冒号后无空格），CC 桥此前按
// "event: " / "data: " 严格匹配会丢弃全部事件，最终报 "Upstream stream ended
// without a response"（#4653 同根因；#4657 只修了 /v1/responses 桥）。
func TestHandleCCBufferedFromAnthropic_CompactSSEFormat(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_cc_buffered_compact"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event:message_start`,
			`data:{"type":"message_start","message":{"id":"msg_c1","type":"message","role":"assistant","content":[],"model":"k3","stop_reason":"","usage":{"input_tokens":15,"cache_read_input_tokens":5,"cache_creation_input_tokens":2}}}`,
			``,
			`event:content_block_start`,
			`data:{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"OK"}}`,
			``,
			`event:message_delta`,
			`data:{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
			``,
		}, "\n"))),
	}

	svc := &GatewayService{}
	result, err := svc.handleCCBufferedFromAnthropic(resp, c, "k3", "k3", nil, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 15, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 5, result.Usage.CacheReadInputTokens)
	require.Equal(t, 2, result.Usage.CacheCreationInputTokens)
	require.Contains(t, rec.Body.String(), `"OK"`, "紧凑格式事件必须被解析并产出响应内容")
}

func TestHandleCCStreamingFromAnthropic_CompactSSEFormat(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_cc_stream_compact"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event:message_start`,
			`data:{"type":"message_start","message":{"id":"msg_c2","type":"message","role":"assistant","content":[],"model":"k3","stop_reason":"","usage":{"input_tokens":21,"cache_read_input_tokens":6,"cache_creation_input_tokens":1}}}`,
			``,
			`event:content_block_start`,
			`data:{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"OK"}}`,
			``,
			`event:message_delta`,
			`data:{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`,
			``,
			`event:message_stop`,
			`data:{"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	svc := &GatewayService{}
	result, err := svc.handleCCStreamingFromAnthropic(resp, c, "k3", "k3", nil, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 21, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Equal(t, 6, result.Usage.CacheReadInputTokens)
	require.Equal(t, 1, result.Usage.CacheCreationInputTokens)
	require.Contains(t, rec.Body.String(), `[DONE]`)
}

func TestHandleCCStreamingFromAnthropic_PreservesMessageStartCacheUsageAndReasoning(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	reasoningEffort := "medium"
	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_cc_stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4.5","stop_reason":"","usage":{"input_tokens":20,"cache_read_input_tokens":11,"cache_creation_input_tokens":4}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"hello"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":8}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	svc := &GatewayService{}
	result, err := svc.handleCCStreamingFromAnthropic(resp, c, "gpt-5", "claude-sonnet-4.5", &reasoningEffort, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 20, result.Usage.InputTokens)
	require.Equal(t, 8, result.Usage.OutputTokens)
	require.Equal(t, 11, result.Usage.CacheReadInputTokens)
	require.Equal(t, 4, result.Usage.CacheCreationInputTokens)
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "medium", *result.ReasoningEffort)
	require.Contains(t, rec.Body.String(), `[DONE]`)
}
