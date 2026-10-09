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

func TestClaudeAccountMappingForwarding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey} {
		for _, client := range []string{"claude-code", "third-party"} {
			for _, endpoint := range []string{"messages", "messages_stream", "count_tokens", "account_test"} {
				for _, tc := range []struct {
					name, requested, pattern, target string
				}{
					{"fable_exact", "claude-fable-5-1", "claude-fable-5-1", "claude-sonnet-4-6"},
					{"fable_wildcard", "claude-fable-5-1", "claude-fable-*", "claude-sonnet-4-6"},
				} {
					t.Run(strings.Join([]string{accountType, client, endpoint, tc.name}, "/"), func(t *testing.T) {
						c, _ := gin.CreateTestContext(httptest.NewRecorder())
						c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
						ctx := context.Background()
						if client == "claude-code" {
							ctx = SetClaudeCodeClient(ctx, true)
						}
						c.Request = c.Request.WithContext(ctx)
						body := []byte(fmt.Sprintf(`{"model":%q,"max_tokens":32,"stream":%t,"messages":[{"role":"user","content":"hello"}]}`, tc.requested, endpoint == "messages_stream"))
						parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
						require.NoError(t, err)
						response := `{"id":"msg_test","type":"message","role":"assistant","model":"` + tc.target + `","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
						if endpoint == "count_tokens" {
							response = `{"input_tokens":1}`
						} else if endpoint == "messages_stream" {
							response = "data: {\"type\":\"message_start\",\"message\":" + response + "}\n\ndata: {\"type\":\"message_stop\"}\n\n"
						} else if endpoint == "account_test" {
							response = "data: {\"type\":\"message_stop\"}\n\n"
						}
						upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
							StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
							Body: io.NopCloser(strings.NewReader(response)),
						}}
						mapping := map[string]any{}
						if tc.pattern != "" {
							mapping[tc.pattern] = tc.target
						}
						if tc.name == "fable_exact" {
							mapping[tc.target] = "must-not-map-twice"
						}
						account := &Account{ID: 1, Platform: PlatformAnthropic, Type: accountType, Credentials: map[string]any{
							"access_token": "test-token", "api_key": "test-key",
							"model_mapping": mapping,
						}}
						svc := &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}, httpUpstream: upstream, rateLimitService: &RateLimitService{}}
						require.True(t, svc.isModelSupportedByAccount(account, tc.requested), "configured source must remain schedulable")
						switch endpoint {
						case "messages", "messages_stream":
							result, err := svc.Forward(ctx, c, account, parsed)
							require.NoError(t, err)
							require.Equal(t, tc.target, result.UpstreamModel)
							require.Equal(t, tc.requested, result.Model)
						case "count_tokens":
							require.NoError(t, svc.ForwardCountTokens(ctx, c, account, parsed))
						case "account_test":
							testSvc := &AccountTestService{httpUpstream: upstream, cfg: svc.cfg}
							require.NoError(t, testSvc.testClaudeAccountConnection(c, account, tc.requested))
						}
						require.Equal(t, tc.target, gjson.GetBytes(upstream.lastBody, "model").String(), "wire request must use the configured target")
					})
				}
			}
		}
	}
}
