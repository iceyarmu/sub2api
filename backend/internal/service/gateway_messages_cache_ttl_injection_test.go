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

func TestForwardMessages_GlobalCacheTTL1hAddsBreakpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey} {
		for _, enabled := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				for _, claudeCode := range []bool{false, true} {
					for _, tc := range []struct{ name, fields string }{
						{"string", `"messages":[{"role":"user","content":"Hello"}]`},
						{"blocks", `"messages":[{"role":"user","content":[{"type":"text","text":"Hello"}]}]`},
						{"existing", `"messages":[{"role":"user","content":[{"type":"text","text":"Hello","cache_control":{"type":"ephemeral","ttl":"5m"}}]}]`},
						{"limit", `"system":[{"type":"text","text":"one","cache_control":{"type":"ephemeral","ttl":"5m"}},{"type":"text","text":"two","cache_control":{"type":"ephemeral","ttl":"5m"}},{"type":"text","text":"three","cache_control":{"type":"ephemeral","ttl":"5m"}}],"tools":[{"name":"probe","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral","ttl":"5m"}}],"messages":[{"role":"user","content":"Hello"}]`},
					} {
						t.Run(fmt.Sprintf("%s/enabled=%t/stream=%t/claudeCode=%t/%s", accountType, enabled, stream, claudeCode, tc.name), func(t *testing.T) {
							resetGatewayForwardingSettingsCacheForTest(t)
							cfg := &config.Config{}
							response := `{"id":"msg_test","type":"message","role":"assistant","model":"claude-haiku-4-5-20251001","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
							if stream {
								response = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":" + response + "}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
							}
							upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(response))}}
							svc := &GatewayService{cfg: cfg, httpUpstream: upstream, rateLimitService: &RateLimitService{},
								settingService: NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{
									SettingKeyEnableAnthropicCacheTTL1hInjection:     fmt.Sprint(enabled),
									SettingKeyEnableClaudeOAuthSystemPromptInjection: "false",
									SettingKeyRewriteMessageCacheControl:             "false",
								}}, cfg),
							}
							account := &Account{ID: 1, Platform: PlatformAnthropic, Type: accountType,
								Credentials: map[string]any{"access_token": "test-token", "api_key": "test-key"},
							}
							body := []byte(fmt.Sprintf(`{"model":"claude-haiku-4-5-20251001","max_tokens":32,"stream":%t,%s}`, stream, tc.fields))
							parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
							require.NoError(t, err)
							ctx := SetClaudeCodeClient(context.Background(), claudeCode)
							c, _ := gin.CreateTestContext(httptest.NewRecorder())
							c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(body))).WithContext(ctx)
							_, err = svc.Forward(ctx, c, account, parsed)
							require.NoError(t, err)
							_, messages, tools, system := collectCacheControlPaths(upstream.lastBody)
							paths := append(append(messages, tools...), system...)
							require.LessOrEqual(t, len(paths), maxCacheControlBlocks)
							wantTTL := "5m"
							if enabled && accountType != AccountTypeAPIKey {
								require.Contains(t, messages, "messages.0.content.0.cache_control")
								wantTTL = "1h"
							} else if tc.name != "existing" {
								require.Empty(t, messages)
							}
							for _, path := range paths {
								require.Equal(t, "ephemeral", gjson.GetBytes(upstream.lastBody, path+".type").String())
								require.Equal(t, wantTTL, gjson.GetBytes(upstream.lastBody, path+".ttl").String(), path)
							}
						})
					}
				}
			}
		}
	}
}
