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

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestChatCompletions_ClaudeTemperatureAfterMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, adapter := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey, "native"} {
		for _, stream := range []bool{false, true} {
			for _, tc := range []struct {
				name, requested, target, temperature string
				keepTemperature                      bool
			}{
				{"haiku", "claude-haiku-4-5-20251001", "claude-haiku-4-5-20251001", `,"temperature":0.7`, false},
				{"haiku_default", "claude-haiku-4-5-20251001", "claude-haiku-4-5-20251001", "", false},
				{"mapped_sonnet", "claude-fable-5-1", "claude-sonnet-4-6", `,"temperature":0`, false},
				{"alias_sonnet55", "public-model", "claude-sonnet-5-5", `,"temperature":0.7`, false},
				{"alias_opus55", "public-model", "claude-opus-5-5", `,"temperature":1`, false},
				{"mapped_non_claude", "claude-fable-5-1", "kimi-k3", `,"temperature":0.7`, true},
				{"non_claude", "kimi-k3", "kimi-k3", `,"temperature":0.7`, true},
			} {
				t.Run(fmt.Sprintf("%s/stream=%t/%s", adapter, stream, tc.name), func(t *testing.T) {
					body := []byte(fmt.Sprintf(`{"model":%q%s,"stream":%t,"messages":[{"role":"user","content":"Hello"}]}`, tc.requested, tc.temperature, stream))
					upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
						Body:       io.NopCloser(strings.NewReader(namespaceToolAnthropicStream())),
					}}
					account := &Account{ID: 1, Platform: PlatformAnthropic, Type: adapter,
						Credentials: map[string]any{"access_token": "test-token", "api_key": "test-key"},
					}
					if adapter == "native" {
						account = nativeAnthropicTestAccount()
					}
					account.Credentials["model_mapping"] = map[string]any{tc.requested: tc.target}
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))
					cfg := rawChatCompletionsTestConfig()
					if adapter == "native" {
						svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
						_, err := svc.forwardChatCompletionsViaNativeAnthropic(context.Background(), c, account, body, "")
						require.NoError(t, err)
					} else {
						svc := &GatewayService{cfg: cfg, httpUpstream: upstream}
						_, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, nil)
						require.NoError(t, err)
					}
					require.Equal(t, tc.target, gjson.GetBytes(upstream.lastBody, "model").String())
					temperature := gjson.GetBytes(upstream.lastBody, "temperature")
					require.Equal(t, tc.keepTemperature, temperature.Exists())
					if tc.keepTemperature {
						require.Equal(t, 0.7, temperature.Float())
					}
				})
			}
		}
	}
}
