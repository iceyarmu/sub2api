package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIResponsesWebSocket_GroupMappingSkipsAccountMappingPerTurn(t *testing.T) {
	for _, first := range []string{"alias", "gpt-5.4"} {
		t.Run(first, func(t *testing.T) {
			second := "gpt-5.4"
			if first == second {
				second = "alias"
			}
			got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
				ingressMode:         service.OpenAIWSIngressModeCtxPool,
				firstPayload:        `{"type":"response.create","model":"` + first + `","stream":false}`,
				secondPayload:       `{"type":"response.create","model":"` + second + `","stream":false}`,
				group:               &service.Group{ID: 4201, Platform: service.PlatformOpenAI, ModelMapping: map[string]string{"alias": "gpt-5.4"}},
				accountModelMapping: map[string]any{"alias": "gpt-5.5", "gpt-5.4": "gpt-5.5"},
			})
			require.Len(t, got.upstreamPayloads, 2)
			for i, model := range []string{first, second} {
				want := "gpt-5.5"
				if model == "alias" {
					want = "gpt-5.4"
				}
				require.Equal(t, want, gjson.GetBytes(got.upstreamPayloads[i], "model").String())
				require.Equal(t, want, *got.logs[i].UpstreamModel)
			}
		})
	}
}

func TestOpenAIResponsesWebSocket_GroupMappingChangeRechecksModelSupport(t *testing.T) {
	accountMapping := map[string]any{"gpt-5.4": "gpt-5.5"}
	runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		ingressMode:         service.OpenAIWSIngressModeCtxPool,
		firstPayload:        `{"type":"response.create","model":"alias","stream":false}`,
		secondPayload:       `{"type":"response.create","model":"gpt-5.4","stream":false}`,
		group:               &service.Group{ID: 4201, Platform: service.PlatformOpenAI, ModelMapping: map[string]string{"alias": "gpt-5.4"}},
		accountModelMapping: accountMapping,
		afterFirstUpstreamRequest: func(_ *service.ChannelService) error {
			// Keep a nonempty whitelist, but withdraw support for the next turn's model.
			accountMapping["other"] = "gpt-5.5"
			delete(accountMapping, "gpt-5.4")
			return nil
		},
		secondTurnCloseExpected: true,
		closeReason:             "model switch requires reconnect",
	})
}
