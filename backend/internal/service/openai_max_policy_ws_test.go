//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type openAIMaxPolicyWSConn struct{ *stagedPassthroughConn }

func (c *openAIMaxPolicyWSConn) WriteJSON(ctx context.Context, value any) error {
	var payload []byte
	switch v := value.(type) {
	case []byte:
		payload = v
	case json.RawMessage:
		payload = v
	default:
		var err error
		payload, err = json.Marshal(value)
		if err != nil {
			return err
		}
	}
	return c.WriteFrame(ctx, coderws.MessageText, payload)
}

func TestOpenAIMaxPolicyWebSocketTurns(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough} {
		for _, scenario := range []string{"downgrade", "block_first", "block_followup", "mapped_downgrade", "mapped_block_first", "mapped_block_followup"} {
			t.Run(mode+"/"+scenario, func(t *testing.T) {
				mapped := strings.HasPrefix(scenario, "mapped_")
				scenario := strings.TrimPrefix(scenario, "mapped_")
				requestedEffort := "max"
				if mapped {
					requestedEffort = "xhigh"
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				cfg := passthroughLifecycleConfig()
				cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 5
				cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds = 5
				cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
				cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
				cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
				upstream := &openAIMaxPolicyWSConn{newStagedPassthroughConn()}
				svc := newPassthroughLifecycleService(cfg, upstream.stagedPassthroughConn)
				svc.openaiWSPassthroughDialer = &stagedPassthroughDialer{conn: upstream}
				if mode == OpenAIWSIngressModeCtxPool {
					pool := newOpenAIWSConnPool(cfg)
					pool.setClientDialerForTest(&stagedPassthroughDialer{conn: upstream})
					svc.openaiWSPool = pool
					defer pool.Close()
				}
				action := "block"
				if scenario == "downgrade" {
					action = "downgrade"
				}
				svc.settingService = newOpenAIMaxPolicyTestSettings(t, action, "all", "medium")
				account := passthroughLifecycleAccount()
				account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
				results := make(chan *OpenAIForwardResult, 4)
				server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(*gin.Context) *OpenAIWSIngressHooks {
					hooks := &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, _ error) {
						if result != nil {
							results <- result
						}
					}}
					if mapped {
						hooks.ReasoningEffortMappings = []ReasoningEffortMapping{{From: "xhigh", To: "max"}}
					}
					return hooks
				})
				defer server.Close()
				effort := requestedEffort
				if scenario == "block_followup" {
					effort = "high"
				}
				client := dialPassthroughLifecycleClientWithPayload(t, server, fmt.Sprintf(`{"type":"response.create","model":"gpt-5.6-sol","input":"hello","reasoning":{"effort":%q}}`, effort))
				defer client.CloseNow()
				for turn := 1; turn <= 2; turn++ {
					blocked := scenario == "block_first" || scenario == "block_followup" && turn == 2
					if blocked {
						event, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
						require.NoError(t, err)
						require.Equal(t, "error", gjson.GetBytes(event, "type").String())
						require.Equal(t, "请降低 OpenAI 推理档位", gjson.GetBytes(event, "error.message").String())
						select {
						case payload := <-upstream.writes:
							t.Fatalf("blocked request reached upstream: %s", payload)
						default:
						}
						break
					}
					wire := requirePassthroughUpstreamWrite(t, upstream.stagedPassthroughConn, 3*time.Second)
					want := "medium"
					if scenario == "block_followup" {
						want = "high"
					}
					require.Equal(t, want, gjson.GetBytes(wire, "reasoning.effort").String())
					upstream.Send(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_%d","model":"gpt-5.6-sol","usage":{"input_tokens":1,"output_tokens":1}}}`, turn))
					event, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
					require.NoError(t, err)
					require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())
					select {
					case result := <-results:
						require.NotNil(t, result.ReasoningEffort)
						require.Equal(t, want, *result.ReasoningEffort)
						if scenario == "downgrade" {
							require.NotNil(t, result.RequestedReasoningEffort)
							require.Equal(t, requestedEffort, *result.RequestedReasoningEffort)
						}
					case <-time.After(3 * time.Second):
						t.Fatal("missing turn usage")
					}
					if turn == 1 {
						writeCtx, stop := context.WithTimeout(ctx, 3*time.Second)
						err = client.Write(writeCtx, coderws.MessageText, []byte(fmt.Sprintf(`{"type":"response.create","model":"gpt-5.6-sol","input":"again","reasoning":{"effort":%q}}`, requestedEffort)))
						stop()
						require.NoError(t, err)
					}
				}
				_ = client.Close(coderws.StatusNormalClosure, "done")
				select {
				case err := <-serverErr:
					if scenario == "downgrade" {
						require.NoError(t, err)
					} else {
						require.Error(t, err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("WS session did not terminate")
				}
			})
		}
	}
}

func TestOpenAIMaxPolicyWebSocketSessionAndControlFrames(t *testing.T) {
	svc := &OpenAIGatewayService{settingService: newOpenAIMaxPolicyTestSettings(t, "downgrade", "all", "low")}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	for _, tc := range []struct{ body, path string }{
		{`{"type":"response.create","response":{"reasoning":{"effort":"max"}}}`, "response.reasoning.effort"},
		{`{"type":"session.update","session":{"reasoning":{"effort":"max"}}}`, "session.reasoning.effort"},
	} {
		out, err := svc.applyOpenAIMaxPolicyForWS(context.Background(), nil, nil, account, []byte(tc.body))
		require.NoError(t, err)
		require.Equal(t, "low", gjson.GetBytes(out, tc.path).String())
	}
	body := []byte(`{"type":"conversation.item.create","reasoning":{"effort":"max"}}`)
	out, err := svc.applyOpenAIMaxPolicyForWS(context.Background(), nil, nil, account, body)
	require.NoError(t, err)
	require.Equal(t, body, out)
}

func TestOpenAIMaxPolicyPassthroughPreservesLegacyGroupCap(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := passthroughLifecycleConfig()
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 5
	cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds = 5
	upstream := newStagedPassthroughConn()
	svc := newPassthroughLifecycleService(cfg, upstream)
	svc.settingService = newOpenAIMaxPolicyTestSettings(t, "pass", "all", "high")
	account := passthroughLifecycleAccount()
	account.Type = AccountTypeOAuth
	account.Credentials = map[string]any{"access_token": "test-token"}
	account.Extra["openai_oauth_responses_websockets_v2_mode"] = OpenAIWSIngressModePassthrough
	server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(*gin.Context) *OpenAIWSIngressHooks {
		return &OpenAIWSIngressHooks{MaxReasoningEffort: "medium"}
	})
	defer server.Close()
	client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"gpt-5.6-sol","input":"hello","reasoning":{"effort":"high"}}`)
	defer client.CloseNow()
	for turn := 1; turn <= 2; turn++ {
		wire := requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
		require.Equal(t, "medium", gjson.GetBytes(wire, "reasoning.effort").String(), "turn %d", turn)
		upstream.Send(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_%d","model":"gpt-5.6-sol","usage":{"input_tokens":1,"output_tokens":1}}}`, turn))
		_, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
		require.NoError(t, err)
		if turn == 1 {
			writeCtx, stop := context.WithTimeout(ctx, 3*time.Second)
			err = client.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.6-sol","input":"again","reasoning":{"mode":"pro"}}`))
			stop()
			require.NoError(t, err)
		}
	}
	_ = client.Close(coderws.StatusNormalClosure, "done")
	select {
	case err := <-serverErr:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("WS session did not terminate")
	}
}
