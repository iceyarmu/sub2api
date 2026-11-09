//go:build unit

package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type groupMappingUpstream struct {
	service.HTTPUpstream
	body []byte
	err  error
}

func (u *groupMappingUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.body, _ = io.ReadAll(req.Body)
	if u.err != nil {
		return nil, u.err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"input_tokens":1}`))}, nil
}

func TestGatewayAnthropicGroupModelMappingCountTokens(t *testing.T) {
	testGatewayAnthropicGroupModelMapping(t, "count_tokens", false)
}

func TestGatewayAnthropicGroupModelMappingCountTokensFallback(t *testing.T) {
	testGatewayAnthropicGroupModelMapping(t, "count_tokens", true)
}

func TestGatewayAnthropicGroupModelMappingGeneration(t *testing.T) {
	for _, endpoint := range []string{"messages", "responses", "chat/completions"} {
		for _, fallback := range []bool{false, true} {
			name := endpoint
			if fallback {
				name += "/fallback"
			}
			t.Run(name, func(t *testing.T) { testGatewayAnthropicGroupModelMapping(t, endpoint, fallback) })
		}
	}
}

func testGatewayAnthropicGroupModelMapping(t *testing.T, endpoint string, fallback bool) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	group := &service.Group{ID: 1, Hydrated: true, Platform: service.PlatformAnthropic, Status: service.StatusActive,
		ModelMapping: map[string]string{"alias": "claude-sonnet-4-6", "claude-sonnet-4-6": "must-not-chain"}}
	want := "claude-sonnet-4-6"
	groups := map[int64]*service.Group{group.ID: group}
	if fallback {
		fallbackID := int64(4)
		group.ClaudeCodeOnly, group.FallbackGroupID = true, &fallbackID
		want = "claude-opus-4-6"
		groups[fallbackID] = &service.Group{ID: fallbackID, Hydrated: true, Platform: service.PlatformAnthropic, Status: service.StatusActive,
			ModelMapping: map[string]string{"alias": want, want: "must-not-chain"}}
	}
	account := &service.Account{ID: 2, Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "test", "model_mapping": map[string]any{want: "account-target"}}}
	upstream := &groupMappingUpstream{}
	if endpoint != "count_tokens" {
		upstream.err = errors.New("stop after capturing the upstream request")
	}
	settings := service.NewSettingService(&toggleSettingRepo{values: map[string]string{}}, cfg)
	require.NoError(t, settings.SetClaudeMaxPolicySettings(context.Background(), &service.ClaudeMaxPolicySettings{Action: "downgrade", Scope: "all", DowngradeEffort: "xhigh"}))
	gateway := service.NewGatewayService(
		nil, &groupMapRepo{fakeGroupRepo: &fakeGroupRepo{}, groups: groups}, nil, nil, nil, nil, nil, nil, cfg,
		service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, nil, nil, nil),
		nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, settings, nil, nil, nil, nil, nil, nil,
	)
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	h := &GatewayHandler{gatewayService: gateway, billingCacheService: billing, cfg: cfg,
		concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(&fakeConcurrencyCache{}), SSEPingFormatClaude, 0)}
	apiKey := &service.APIKey{ID: 3, UserID: 4, GroupID: &group.ID, Group: group, Status: service.StatusActive, User: &service.User{ID: 4, Concurrency: 10, Balance: 100}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	body := `{"model":"alias","max_tokens":32,"output_config":{"effort":"max"},"messages":[{"role":"user","content":"hello"}]}`
	path := "/v1/" + endpoint
	if endpoint == "responses" {
		body = `{"model":"alias","reasoning":{"effort":"max"},"input":"hello"}`
	} else if endpoint == "chat/completions" {
		body = `{"model":"alias","reasoning_effort":"max","messages":[{"role":"user","content":"hello"}]}`
	} else if endpoint == "count_tokens" {
		path = "/v1/messages/count_tokens"
	}
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)).WithContext(ctx)
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 10})
	switch endpoint {
	case "responses":
		h.Responses(c)
	case "chat/completions":
		h.ChatCompletions(c)
	case "messages":
		h.Messages(c)
	default:
		h.CountTokens(c)
	}
	if endpoint == "count_tokens" {
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	} else {
		require.Equal(t, "xhigh", gjson.GetBytes(upstream.body, "output_config.effort").String(), recorder.Body.String())
	}
	require.Equal(t, want, gjson.GetBytes(upstream.body, "model").String())
	require.Equal(t, "alias", c.Request.Context().Value(ctxkey.Model))
	require.Equal(t, "account-target", account.GetMappedModel(want))
}
