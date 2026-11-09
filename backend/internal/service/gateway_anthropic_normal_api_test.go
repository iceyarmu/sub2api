package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAnthropicNormalAPIScope(t *testing.T) {
	for _, tc := range []struct {
		name    string
		account *Account
	}{
		{"nil", nil},
		{"default", &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}},
		{"disabled", &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Extra: map[string]any{"anthropic_normal_api": false}}},
		{"invalid", &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Extra: map[string]any{"anthropic_normal_api": "true"}}},
		{"oauth", &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: map[string]any{"anthropic_normal_api": true}}},
		{"setup_token", &Account{Platform: PlatformAnthropic, Type: AccountTypeSetupToken, Extra: map[string]any{"anthropic_normal_api": true}}},
		{"other_platform", &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"anthropic_normal_api": true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"system":"You are Claude Code, Anthropic's official CLI for Claude."}`)
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
			req.Header.Set("User-Agent", "claude-cli/2.1.258")
			headers := req.Header.Clone()
			originalReader := req.Body
			require.False(t, tc.account.IsAnthropicNormalAPIEnabled())
			require.Equal(t, body, applyAnthropicNormalAPI(tc.account, req, body))
			require.Equal(t, headers, req.Header)
			require.Equal(t, originalReader, req.Body)
		})
	}
}

func TestAnthropicNormalAPIOutboundPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"messages", "messages_passthrough", "count_tokens", "count_tokens_passthrough"} {
		t.Run(path, func(t *testing.T) {
			body := []byte(`{"model":"claude-sonnet-4-5","max_tokens":100,"stream":true,"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.258.abc; cc_entrypoint=cli;"},{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude.\nUse the tools carefully.","cache_control":{"type":"ephemeral"}}],"metadata":{"user_id":"{\"device_id\":\"device\",\"account_uuid\":\"account\",\"session_id\":\"session\"}"},"tools":[{"name":"Read","description":"Read a file","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}],"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"tool_1","name":"Read","input":{"path":"README.md"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_1","content":"Claude Code project instructions"}]}]}`)
			original := bytes.Clone(body)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
			c.Request.Header.Set("User-Agent", "claude-cli/2.1.258")
			c.Request.Header.Set("X-App", "cli")
			c.Request.Header.Set("X-Claude-Code-Session-Id", "session")
			c.Request.Header.Set("Anthropic-Beta", "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14")
			clientHeaders := c.Request.Header.Clone()
			account := &Account{
				Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
				Extra: map[string]any{"anthropic_normal_api": true},
				Credentials: map[string]any{
					"header_override_enabled": true,
					"header_overrides": map[string]any{
						"user-agent": "claude-cli/9.9.9", "x-app": "cli",
						"x-claude-code-agent-id": "agent",
						"anthropic-beta":         "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14",
					},
				},
			}
			svc := &GatewayService{cfg: &config.Config{}}
			var req *http.Request
			var wire []byte
			var err error
			switch path {
			case "messages":
				req, wire, err = svc.buildUpstreamRequest(context.Background(), c, account, body, "upstream-key", "apikey", "claude-sonnet-4-5", true, false)
			case "messages_passthrough":
				req, wire, err = svc.buildUpstreamRequestAnthropicAPIKeyPassthrough(context.Background(), c, account, body, "upstream-key")
			case "count_tokens":
				req, wire, err = svc.buildCountTokensRequest(context.Background(), c, account, body, "upstream-key", "apikey", "claude-sonnet-4-5", false)
			case "count_tokens_passthrough":
				req, err = svc.buildCountTokensRequestAnthropicAPIKeyPassthrough(context.Background(), c, account, body, "upstream-key")
			}
			require.NoError(t, err)
			actual, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			if wire != nil {
				require.Equal(t, wire, actual, "returned body must match the wire body")
			}
			require.Equal(t, int64(len(actual)), req.ContentLength)
			retry, err := req.GetBody()
			require.NoError(t, err)
			retryBody, err := io.ReadAll(retry)
			require.NoError(t, err)
			require.NoError(t, retry.Close())
			require.Equal(t, actual, retryBody)
			require.Equal(t, "sub2api", req.UserAgent())
			require.Empty(t, getHeaderRaw(req.Header, "x-app"))
			require.Empty(t, getHeaderRaw(req.Header, "x-claude-code-session-id"))
			require.Empty(t, getHeaderRaw(req.Header, "x-claude-code-agent-id"))
			require.Equal(t, "upstream-key", getHeaderRaw(req.Header, "x-api-key"))
			require.Equal(t, "interleaved-thinking-2025-05-14", getHeaderRaw(req.Header, "anthropic-beta"))
			require.False(t, gjson.GetBytes(actual, "metadata.user_id").Exists())
			require.Equal(t, "You are a coding assistant.\nUse the tools carefully.", gjson.GetBytes(actual, "system.0.text").String())
			require.Equal(t, "ephemeral", gjson.GetBytes(actual, "system.0.cache_control.type").String())
			require.Len(t, gjson.GetBytes(actual, "system").Array(), 1)
			for _, field := range []string{"tools", "messages"} {
				require.JSONEq(t, gjson.GetBytes(original, field).Raw, gjson.GetBytes(actual, field).Raw)
			}
			require.Equal(t, original, body, "must not mutate shared input on failover")
			require.Equal(t, clientHeaders, c.Request.Header)
			require.Equal(t, actual, applyAnthropicNormalAPI(account, req, actual), "retries must be idempotent")
		})
	}
}

func TestAnthropicNormalAPISystemAndMetadata(t *testing.T) {
	account := &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Extra: map[string]any{"anthropic_normal_api": true}}
	for _, tc := range []struct{ name, body, want string }{
		{"string", `{"system":"You are Claude Code, Anthropic's official CLI for Claude.\nKeep all project rules."}`, `{"system":"You are a coding assistant.\nKeep all project rules."}`},
		{"sdk", `{"system":"You are Claude Code, Anthropic's official CLI for Claude, running within the Claude Agent SDK."}`, `{"system":"You are a coding assistant."}`},
		{"agent_sdk", `{"system":"You are a Claude agent, built on Anthropic's Claude Agent SDK."}`, `{"system":"You are a coding assistant."}`},
		{"explore", `{"system":"You are a file search specialist for Claude Code, Anthropic's official CLI for Claude."}`, `{"system":"You are a file search specialist."}`},
		{"billing_only", `{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.258; cc_entrypoint=cli;"}]}`, `{}`},
		{"ordinary", `{"system":"Document Claude Code integration.","metadata":{"user_id":"customer-123"}}`, `{"system":"Document Claude Code integration.","metadata":{"user_id":"customer-123"}}`},
		{"no_system", `{"messages":[]}`, `{"messages":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
			req.Header.Set("Anthropic-Beta", "claude-code-20250219,oauth-2025-04-20")
			require.JSONEq(t, tc.want, string(applyAnthropicNormalAPI(account, req, body)))
			require.Empty(t, getHeaderRaw(req.Header, "anthropic-beta"))
		})
	}
}

func TestAnthropicNormalAPIPreservesSystemExamples(t *testing.T) {
	examples := "Document these literals without changing them:\n```text\n" + claudeCodeSystemPrompt + "\nx-anthropic-billing-header: example\n```"
	for _, tc := range []struct{ name, input, want string }{
		{"quoted_task", examples, examples},
		{"identity_then_task", claudeCodeSystemPrompt + "\n" + examples, "You are a coding assistant.\n" + examples},
		{"billing_then_task", "x-anthropic-billing-header: cc_version=2.1.258; cc_entrypoint=cli;\n" + examples, examples},
		{"header_documentation", "x-anthropic-billing-header: is an attribution field, keep this documentation.", "x-anthropic-billing-header: is an attribution field, keep this documentation."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, normalAPISystemText(tc.input))
		})
	}
}

func TestAnthropicNormalAPIPreservesMultipleBetaValues(t *testing.T) {
	account := &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Extra: map[string]any{"anthropic_normal_api": true}}
	body := []byte(`{"messages":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header["anthropic-beta"] = []string{"claude-code-20250219", "fine-grained-tool-streaming-2025-05-14"}
	req.Header["ANTHROPIC-BETA"] = []string{"oauth-2025-04-20", "interleaved-thinking-2025-05-14"}
	applyAnthropicNormalAPI(account, req, body)
	require.NotContains(t, req.Header, "ANTHROPIC-BETA")
	require.ElementsMatch(t, []string{"fine-grained-tool-streaming-2025-05-14", "interleaved-thinking-2025-05-14"}, strings.Split(getHeaderRaw(req.Header, "anthropic-beta"), ","))
}

func TestAnthropicNormalAPIAccountConnectionTest(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			account := newAnthropicAPIKeyAccountForTest()
			account.Extra["anthropic_normal_api"] = enabled
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n")),
			}}
			svc := &AccountTestService{cfg: &config.Config{}, httpUpstream: upstream}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			require.NoError(t, svc.testClaudeAccountConnection(c, account, "claude-haiku-4-5"))
			if enabled {
				require.Equal(t, "sub2api", upstream.lastReq.UserAgent())
				require.Equal(t, "You are a coding assistant.", gjson.GetBytes(upstream.lastBody, "system.0.text").String())
				require.False(t, gjson.GetBytes(upstream.lastBody, "metadata.user_id").Exists())
				require.NotContains(t, getHeaderRaw(upstream.lastReq.Header, "anthropic-beta"), "claude-code-")
			} else {
				require.Contains(t, upstream.lastReq.UserAgent(), "claude-cli/")
				require.Equal(t, claudeCodeSystemPrompt, gjson.GetBytes(upstream.lastBody, "system.0.text").String())
			}
			require.Equal(t, "upstream-anthropic-key", getHeaderRaw(upstream.lastReq.Header, "x-api-key"))
		})
	}
}
