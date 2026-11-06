//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGroupModelMappingAdminLifecycle(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := &adminServiceImpl{groupRepo: repo, cfg: &config.Config{RunMode: config.RunModeSimple}}
	group, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name: "mapped", Platform: PlatformOpenAI, ModelMapping: map[string]string{" alias-* ": " gpt-5.4 "},
	})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"alias-*": "gpt-5.4"}, repo.created.ModelMapping)
	require.False(t, group.AllowMessagesDispatch)
	repo.getByID = group
	_, err = svc.UpdateGroup(context.Background(), group.ID, &UpdateGroupInput{Name: "renamed"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"alias-*": "gpt-5.4"}, repo.updated.ModelMapping)
	_, err = svc.UpdateGroup(context.Background(), group.ID, &UpdateGroupInput{ModelMapping: map[string]string{}})
	require.NoError(t, err)
	require.Empty(t, repo.updated.ModelMapping)
	for _, bad := range []map[string]string{{"": "gpt-5.4"}, {"alias": " "}, {"a*b": "gpt-5.4"}, {"alias": "gpt-*"}, {"alias": "a", " alias ": "b"}} {
		_, err := svc.UpdateGroup(context.Background(), group.ID, &UpdateGroupInput{ModelMapping: bad})
		require.Error(t, err)
	}
}

func TestGroupModelMappingResolution(t *testing.T) {
	group := &Group{ID: 7, Platform: PlatformOpenAI, ModelMapping: map[string]string{
		"alias": "gpt-5.4", "alias-*": "gpt-5.5", "alias-long-*": "gpt-5.5-pro", "same": "same",
	}}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	base := ChannelMappingResult{MappedModel: "channel-target", Mapped: true, ChannelID: 12}
	for _, tc := range []struct{ model, want string }{
		{"alias", "gpt-5.4"}, {"alias-x", "gpt-5.5"}, {"alias-long-x", "gpt-5.5-pro"},
		{"same", "same"}, {"unmatched", "channel-target"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			got := applyGroupModelMapping(ctx, group.ID, tc.model, base)
			require.Equal(t, tc.want, got.MappedModel)
			require.Equal(t, base.ChannelID, got.ChannelID)
		})
	}
	require.Equal(t, base, applyGroupModelMapping(ctx, 8, "alias", base))
	require.Equal(t, base, applyGroupModelMapping(context.Background(), group.ID, "alias", base))
	group.Platform = PlatformAnthropic
	require.Equal(t, base, applyGroupModelMapping(ctx, group.ID, "alias", base))
	group.Platform = PlatformOpenAI
	openai := &OpenAIGatewayService{}
	gateway := &GatewayService{}
	for _, resolve := range []func(context.Context, *int64, string) (ChannelMappingResult, bool){openai.ResolveChannelMappingAndRestrict, gateway.ResolveChannelMappingAndRestrict} {
		got, restricted := resolve(ctx, &group.ID, "alias")
		require.Equal(t, "gpt-5.4", got.MappedModel)
		require.True(t, got.Mapped)
		require.False(t, restricted)
		got, _ = resolve(ctx, nil, "alias")
		require.Equal(t, "alias", got.MappedModel)
	}
	channel := NewChannelService(&mockChannelRepository{}, nil, nil, nil, nil)
	got := channel.ResolveChannelMapping(ctx, group.ID, "alias")
	require.Equal(t, "gpt-5.4", got.MappedModel)
}

func TestGroupModelMappingAuthSnapshot(t *testing.T) {
	svc := NewAPIKeyService(nil, nil, nil, nil, nil, nil, &config.Config{})
	groupID := int64(7)
	key := &APIKey{ID: 1, UserID: 2, GroupID: &groupID, Key: "test", Status: StatusActive,
		User:  &User{ID: 2, Status: StatusActive},
		Group: &Group{ID: groupID, Platform: PlatformOpenAI, Status: StatusActive, ModelMapping: map[string]string{"alias": "gpt-5.4"}},
	}
	data, err := json.Marshal(svc.snapshotFromAPIKey(context.Background(), key))
	require.NoError(t, err)
	var snapshot APIKeyAuthSnapshot
	require.NoError(t, json.Unmarshal(data, &snapshot))
	restored := svc.snapshotToAPIKey(key.Key, &snapshot)
	require.Equal(t, key.Group.ModelMapping, restored.Group.ModelMapping)
}

func TestGroupModelMappingForwardResponsesAndChat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"responses", "chat", "native-chat"} {
		for _, tc := range []struct {
			requested, want string
			matched         bool
		}{
			{"alias", "gpt-5.4", true},
			{"alias-other", "gpt-5.4", true},
			{"gpt-5.4", "gpt-5.4", true},
			{"unmatched", "gpt-5.5", false},
		} {
			t.Run(endpoint+"/"+tc.requested, func(t *testing.T) {
				group := &Group{ID: 7, Platform: PlatformOpenAI, ModelMapping: map[string]string{"alias": "gpt-5.4", "alias-*": "gpt-5.4", "gpt-5.4": "gpt-5.4"}}
				ctx := context.WithValue(context.Background(), ctxkey.Group, group)
				ctx = context.WithValue(ctx, ctxkey.Model, tc.requested)
				response := `{"id":"resp_test","object":"response","model":"` + tc.want + `","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`
				contentType := "application/json"
				if endpoint == "chat" {
					response = "data: {\"type\":\"response.completed\",\"response\":" + response + "}\n\n"
					contentType = "text/event-stream"
				} else if endpoint == "native-chat" {
					response = `{"id":"chat_test","object":"chat.completion","model":"` + tc.want + `","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(response))}}
				svc := &OpenAIGatewayService{cfg: &config.Config{RunMode: config.RunModeSimple}, httpUpstream: upstream}
				account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
					Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://example.com", "model_mapping": map[string]any{"*": "gpt-5.5"}},
					Extra:       map[string]any{"openai_responses_supported": endpoint != "native-chat"},
				}
				path := "/v1/responses"
				body := []byte(`{"model":"` + tc.requested + `","stream":false,"input":"hello"}`)
				if endpoint != "responses" {
					path = "/v1/chat/completions"
					body = []byte(`{"model":"` + tc.requested + `","stream":false,"messages":[{"role":"user","content":"hello"}]}`)
				}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, path, nil).WithContext(ctx)
				SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
				mapping, _ := svc.ResolveChannelMappingAndRestrict(ctx, &group.ID, tc.requested)
				require.Equal(t, tc.matched, mapping.Mapped)
				body = svc.ReplaceModelInBody(body, mapping.MappedModel)
				var result *OpenAIForwardResult
				var err error
				if endpoint != "responses" {
					result, err = svc.ForwardAsChatCompletions(ctx, c, account, body, "", "")
				} else {
					result, err = svc.Forward(ctx, c, account, body)
				}
				require.NoError(t, err)
				require.Equal(t, tc.want, gjson.GetBytes(upstream.lastBody, "model").String())
				require.Equal(t, tc.want, result.UpstreamModel)
				require.Equal(t, tc.want, result.BillingModel)
				require.Equal(t, "gpt-5.5", account.GetMappedModel("gpt-5.4"), "request must not mutate shared account state")
			})
		}
	}
}

func TestGroupModelMappingOverridesChannelOnlyOnMatch(t *testing.T) {
	group := &Group{ID: 7, Platform: PlatformOpenAI, ModelMapping: map[string]string{"alias": "gpt-5.4"}}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	channel := Channel{ID: 12, Status: StatusActive, GroupIDs: []int64{7, 8}, BillingModelSource: BillingModelSourceRequested,
		ModelMapping: map[string]map[string]string{PlatformOpenAI: {"alias": "channel-target", "fallback": "channel-fallback"}},
	}
	channels := newTestChannelService(makeStandardRepo(channel, map[int64]string{7: PlatformOpenAI, 8: PlatformOpenAI}))
	svc := &OpenAIGatewayService{channelService: channels}
	mapped, _ := svc.ResolveChannelMappingAndRestrict(ctx, &group.ID, "alias")
	require.Equal(t, "gpt-5.4", mapped.MappedModel)
	require.Equal(t, int64(12), mapped.ChannelID)
	require.Equal(t, BillingModelSourceRequested, mapped.BillingModelSource)
	mapped, _ = svc.ResolveChannelMappingAndRestrict(ctx, &group.ID, "fallback")
	require.Equal(t, "channel-fallback", mapped.MappedModel)
	otherGroupID := int64(8)
	mapped, _ = svc.ResolveChannelMappingAndRestrict(ctx, &otherGroupID, "alias")
	require.Equal(t, "channel-target", mapped.MappedModel)
	cloned := cloneGroupForDuplicate(group, "copy")
	require.Equal(t, group.ModelMapping, cloned.ModelMapping)
	cloned.ModelMapping["alias"] = "changed"
	require.Equal(t, "gpt-5.4", group.ModelMapping["alias"])
}

func TestGroupModelMappingPricingRestrictionUsesOriginalRequest(t *testing.T) {
	for _, billingSource := range []string{BillingModelSourceRequested, BillingModelSourceChannelMapped} {
		for _, pricedModel := range []string{"alias", "gpt-5.4", "gpt-5.5"} {
			t.Run(billingSource+"/"+pricedModel, func(t *testing.T) {
				group := &Group{ID: 7, Platform: PlatformOpenAI, ModelMapping: map[string]string{"alias": "gpt-5.4", "gpt-5.4": "gpt-5.5"}}
				ctx := context.WithValue(context.Background(), ctxkey.Group, group)
				// HTTP handlers retain the client model in context and schedule the mapped model.
				ctx = context.WithValue(ctx, ctxkey.Model, "alias")
				channel := Channel{ID: 12, Status: StatusActive, GroupIDs: []int64{7}, RestrictModels: true, BillingModelSource: billingSource,
					ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{pricedModel}}},
				}
				channels := newTestChannelService(makeStandardRepo(channel, map[int64]string{7: PlatformOpenAI}))
				openai := &OpenAIGatewayService{channelService: channels}
				gateway := &GatewayService{channelService: channels}
				mapping, _ := openai.ResolveChannelMappingAndRestrict(ctx, &group.ID, "alias")
				require.Equal(t, "gpt-5.4", mapping.MappedModel)
				expectedBillingModel := "gpt-5.4"
				if billingSource == BillingModelSourceRequested {
					expectedBillingModel = "alias"
				}
				for _, check := range []func(context.Context, *int64, string) bool{openai.checkChannelPricingRestriction, gateway.checkChannelPricingRestriction} {
					require.Equal(t, pricedModel != expectedBillingModel, check(ctx, &group.ID, mapping.MappedModel), "restriction must use the same model as forwarding/billing, without mapping twice")
					// Direct requests for the target still apply their own mapping; do not reverse-map by target alone.
					directCtx := context.WithValue(ctx, ctxkey.Model, "gpt-5.4")
					directBillingModel := "gpt-5.5"
					if billingSource == BillingModelSourceRequested {
						directBillingModel = "gpt-5.4"
					}
					require.Equal(t, pricedModel != directBillingModel, check(directCtx, &group.ID, "gpt-5.5"))
				}
			})
		}
	}
}

func TestGroupModelMappingAccountViewIsIsolated(t *testing.T) {
	group := &Group{ID: 7, Platform: PlatformOpenAI, ModelMapping: map[string]string{"alias-*": "gpt-5.4", "gpt-5.4": "gpt-5.4"}}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"model_mapping":         map[string]any{"gpt-*": "gpt-5.5"},
		"compact_model_mapping": map[string]any{"*": "gpt-5.5-compact"},
	}}
	for _, original := range []string{"alias-test", "gpt-5.4"} {
		view := AccountForGroupRequestedModel(ctx, account, original)
		require.Equal(t, "gpt-5.4", view.GetMappedModel("gpt-5.4"))
		require.Equal(t, "gpt-5.4", resolveOpenAIForwardModel(view, "gpt-5.4", "messages-default"))
		require.Equal(t, "gpt-5.4", resolveOpenAIAccountUpstreamModelForRequest(view, "gpt-5.4", true))
		require.True(t, view.IsModelSupported("gpt-5.4"))
		require.False(t, view.IsModelSupported("unsupported"), "account whitelist remains enforced")
		// A later WS turn or a separate request that misses the group rule resumes account mapping.
		missed := AccountForGroupRequestedModel(ctx, view, "unmatched")
		require.Equal(t, "gpt-5.5", missed.GetMappedModel("gpt-5.4"))
		require.Equal(t, "gpt-5.5", account.GetMappedModel("gpt-5.4"), "shared account must remain unchanged")
	}
}

func TestGroupModelMappingSchedulerUsesFinalTarget(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	group := &Group{ID: 7, Platform: PlatformOpenAI, ModelMapping: map[string]string{"alias": "gpt-5.4"}}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	ctx = context.WithValue(ctx, ctxkey.Model, "alias")
	account := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.4": "gpt-5.5"}},
	}
	channel := Channel{ID: 12, Status: StatusActive, GroupIDs: []int64{7}, RestrictModels: true, BillingModelSource: BillingModelSourceUpstream,
		ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"gpt-5.4"}}},
	}
	svc := &OpenAIGatewayService{cfg: &config.Config{RunMode: config.RunModeSimple}, accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{account}},
		channelService: newTestChannelService(makeStandardRepo(channel, map[int64]string{7: PlatformOpenAI})), cache: &schedulerTestGatewayCache{},
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}
	selection, _, err := svc.SelectAccountWithScheduler(ctx, &group.ID, "", "", "gpt-5.4", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err, "group target must not be rejected based on the retired account-mapped target")
	require.Equal(t, "gpt-5.4", selection.Account.GetMappedModel("gpt-5.4"))
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	require.Equal(t, "gpt-5.5", account.GetMappedModel("gpt-5.4"))
	unmatched := context.WithValue(ctx, ctxkey.Model, "gpt-5.4")
	_, _, err = svc.SelectAccountWithScheduler(unmatched, &group.ID, "", "", "gpt-5.4", nil, OpenAIUpstreamTransportAny, false)
	require.Error(t, err, "unmatched requests still apply the original account mapping and restrictions")
}
