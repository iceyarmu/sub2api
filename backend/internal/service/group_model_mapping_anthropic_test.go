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
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAnthropicGroupModelMappingLifecycle(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := &adminServiceImpl{groupRepo: repo, cfg: &config.Config{RunMode: config.RunModeSimple}}
	group, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name: "mapped", Platform: PlatformAnthropic, ModelMapping: map[string]string{" alias-* ": " claude-sonnet-4-6 "},
	})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"alias-*": "claude-sonnet-4-6"}, repo.created.ModelMapping)
	repo.getByID = group
	_, err = svc.UpdateGroup(context.Background(), group.ID, &UpdateGroupInput{Name: "renamed"})
	require.NoError(t, err)
	require.Equal(t, group.ModelMapping, repo.updated.ModelMapping)
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	mapping := applyGroupModelMapping(ctx, group.ID, "alias-test", ChannelMappingResult{MappedModel: "channel-target", ChannelID: 12})
	require.True(t, mapping.GroupMapped)
	require.Equal(t, "claude-sonnet-4-6", mapping.MappedModel)
	require.Equal(t, int64(12), mapping.ChannelID)
	_, err = svc.UpdateGroup(ctx, group.ID, &UpdateGroupInput{ModelMapping: map[string]string{"bad*pattern": "target"}})
	require.Error(t, err)
	_, err = svc.UpdateGroup(ctx, group.ID, &UpdateGroupInput{ModelMapping: map[string]string{}})
	require.NoError(t, err)
	require.Empty(t, repo.updated.ModelMapping)
}

func TestAnthropicGroupModelMappingForwarding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken} {
		for _, endpoint := range []string{"messages", "passthrough", "responses", "chat", "count_tokens"} {
			if endpoint == "passthrough" && accountType != AccountTypeAPIKey {
				continue
			}
			for _, requested := range []string{"alias", "alias-other", "claude-sonnet-4-6", "unmatched"} {
				t.Run(accountType+"/"+endpoint+"/"+requested, func(t *testing.T) {
					want := "claude-sonnet-4-6"
					if requested == "unmatched" {
						want = "claude-opus-4-6"
						if endpoint == "responses" && accountType != AccountTypeAPIKey {
							want = requested // Preserve the existing OAuth Responses behavior on a miss.
						}
					}
					group := &Group{ID: 7, Platform: PlatformAnthropic, ModelMapping: map[string]string{
						"alias": "claude-sonnet-4-6", "alias-*": "claude-sonnet-4-6", "claude-sonnet-4-6": "claude-sonnet-4-6",
					}}
					ctx := context.WithValue(context.Background(), ctxkey.Group, group)
					ctx = context.WithValue(ctx, ctxkey.Model, requested)
					svc := newClaudeMaxPolicyTestService(t, "downgrade", "xhigh")
					svc.rateLimitService = &RateLimitService{}
					account := &Account{ID: 1, Platform: PlatformAnthropic, Type: accountType, Credentials: map[string]any{
						"api_key": "test-key", "access_token": "test-token", "model_mapping": map[string]any{"*": "claude-opus-4-6"},
					}, Extra: map[string]any{"anthropic_passthrough": endpoint == "passthrough"}}
					body := []byte(fmt.Sprintf(`{"model":%q,"max_tokens":32,"messages":[{"role":"user","content":"hello"}],"output_config":{"effort":"max"}}`, requested))
					if endpoint == "responses" {
						body = []byte(fmt.Sprintf(`{"model":%q,"input":"hello","reasoning":{"effort":"max"}}`, requested))
					} else if endpoint == "chat" {
						body = []byte(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}],"reasoning_effort":"max"}`, requested))
					}
					mapping, _ := svc.ResolveChannelMappingAndRestrict(ctx, &group.ID, requested)
					body = svc.ReplaceModelInBody(body, mapping.MappedModel)
					response := `{"id":"msg_test","type":"message","role":"assistant","model":"` + want + `","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
					if endpoint == "responses" || endpoint == "chat" {
						response = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":" + response + "}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
					} else if endpoint == "count_tokens" {
						response = `{"input_tokens":1}`
					}
					upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(response))}}
					svc.httpUpstream = upstream
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx)
					parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
					require.NoError(t, err)
					switch endpoint {
					case "responses":
						_, err = svc.ForwardAsResponses(ctx, c, account, body, nil)
					case "chat":
						_, err = svc.ForwardAsChatCompletions(ctx, c, account, body, nil)
					case "count_tokens":
						err = svc.ForwardCountTokens(ctx, c, account, parsed)
					default:
						_, err = svc.Forward(ctx, c, account, parsed)
					}
					require.NoError(t, err)
					require.Equal(t, want, gjson.GetBytes(upstream.lastBody, "model").String())
					if endpoint != "count_tokens" {
						require.Equal(t, "xhigh", gjson.GetBytes(upstream.lastBody, "output_config.effort").String())
					}
					require.Equal(t, "claude-opus-4-6", account.GetMappedModel("claude-sonnet-4-6"), "shared account must remain unchanged")
				})
			}
		}
	}
}

func TestAnthropicGroupModelMappingSelection(t *testing.T) {
	group := &Group{ID: 7, Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true, ModelMapping: map[string]string{"alias": "claude-sonnet-4-6", "claude-sonnet-4-6": "must-not-chain"}}
	account := Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials:   map[string]any{"model_mapping": map[string]any{"claude-sonnet-4-6": "claude-opus-4-6"}},
		AccountGroups: []AccountGroup{{GroupID: group.ID}},
	}
	channel := Channel{ID: 12, Status: StatusActive, GroupIDs: []int64{7}, RestrictModels: true, BillingModelSource: BillingModelSourceUpstream,
		ModelPricing: []ChannelModelPricing{{Platform: PlatformAnthropic, Models: []string{"claude-sonnet-4-6"}}},
	}
	for _, loadBatch := range []bool{false, true} {
		t.Run(fmt.Sprint(loadBatch), func(t *testing.T) {
			cfg := testConfig()
			cfg.RunMode = config.RunModeSimple
			cfg.Gateway.Scheduling.LoadBatchEnabled = loadBatch
			svc := &GatewayService{cfg: cfg,
				accountRepo: &mockAccountRepoForPlatform{accounts: []Account{account}, accountsByID: map[int64]*Account{1: &account}},
				groupRepo:   &mockGroupRepoForGateway{groups: map[int64]*Group{7: group}}, cache: &mockGatewayCacheForPlatform{},
				concurrencyService: NewConcurrencyService(&mockConcurrencyCache{}),
				channelService:     newTestChannelService(makeStandardRepo(channel, map[int64]string{7: PlatformAnthropic})),
			}
			selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &group.ID, "", "alias", nil, "", 0)
			require.NoError(t, err)
			require.Equal(t, "claude-sonnet-4-6", selection.Account.GetMappedModel("claude-sonnet-4-6"))
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
			require.Equal(t, "claude-opus-4-6", account.GetMappedModel("claude-sonnet-4-6"))
		})
	}
}

func TestAnthropicGroupModelMappingSurvivesProfitRefresh(t *testing.T) {
	group := gatewayProfitTestGroup(7, PlatformAnthropic)
	group.ModelMapping = map[string]string{"alias": "claude-sonnet-4-6"}
	ctx := context.WithValue(gatewayProfitTestContext(group), ctxkey.Model, "alias")
	account := gatewayProfitTestAccount(1, PlatformAntigravity, 0.2, group.ID)
	account.Credentials = map[string]any{"model_mapping": map[string]any{"claude-sonnet-4-6": "claude-opus-4-6"}}
	svc := &GatewayService{schedulerSnapshot: NewSchedulerSnapshotService(&gatewayProfitSnapshotCache{account: &account}, nil, nil, nil, nil)}
	ctx = svc.withGatewayProfitControlGate(ctx, &group.ID)
	selected := accountForGroupModelMapping(ctx, &account)
	latest, vetoed, _ := svc.GatewayProfitControlVetoLatest(ctx, selected)
	require.False(t, vetoed)
	require.Equal(t, "claude-sonnet-4-6", mapAntigravityModel(latest, "claude-sonnet-4-6"))
	require.Equal(t, "claude-opus-4-6", account.GetMappedModel("claude-sonnet-4-6"))
}

func TestAnthropicGroupModelMappingFallbackContext(t *testing.T) {
	for _, tc := range []struct {
		name, fallbackTarget, want string
		claudeCode                 bool
		wantGroupID                int64
	}{
		{"fallback overrides primary", "claude-opus-4-6", "claude-opus-4-6", false, 8},
		{"fallback without mapping resumes account mapping", "", "alias", false, 8},
		{"Claude Code keeps primary", "claude-opus-4-6", "claude-sonnet-4-6", true, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fallback := &Group{ID: 8, Platform: PlatformAnthropic, Hydrated: true, Status: StatusActive}
			if tc.fallbackTarget != "" {
				fallback.ModelMapping = map[string]string{"alias": tc.fallbackTarget}
			}
			primary := &Group{ID: 7, Platform: PlatformAnthropic, Hydrated: true, Status: StatusActive, ClaudeCodeOnly: true,
				FallbackGroupID: &fallback.ID, ModelMapping: map[string]string{"alias": "claude-sonnet-4-6"}}
			ctx := context.WithValue(context.Background(), ctxkey.Group, primary)
			ctx = context.WithValue(ctx, ctxkey.Model, "alias")
			ctx = SetClaudeCodeClient(ctx, tc.claudeCode)
			svc := &GatewayService{groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{7: primary, 8: fallback}}}
			ctx, mapping := svc.ResolveRequestModelMapping(ctx, &primary.ID, "alias")
			require.Equal(t, tc.want, mapping.MappedModel)
			require.Equal(t, tc.wantGroupID, ctx.Value(ctxkey.Group).(*Group).ID)
			account := &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{"alias": "account-target"}}}
			wantAccountModel := tc.want
			if !mapping.GroupMapped {
				wantAccountModel = "account-target"
			}
			require.Equal(t, wantAccountModel, accountForGroupModelMapping(ctx, account).GetMappedModel(mapping.MappedModel))
		})
	}
}
