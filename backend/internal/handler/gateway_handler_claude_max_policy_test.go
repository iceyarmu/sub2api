//go:build unit

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGatewayClaudeMaxPolicyBlockResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []struct {
		path string
		body string
		call func(*GatewayHandler, *gin.Context)
	}{
		{"/v1/messages", `{"model":"claude-opus-4-6","max_tokens":1024,"output_config":{"effort":"max"},"messages":[{"role":"user","content":"hello"}]}`, (*GatewayHandler).Messages},
		{"/v1/responses", `{"model":"claude-opus-4-6","reasoning":{"effort":"max"},"input":"hello"}`, (*GatewayHandler).Responses},
		{"/v1/chat/completions", `{"model":"claude-opus-4-6","reasoning_effort":"max","messages":[{"role":"user","content":"hello"}]}`, (*GatewayHandler).ChatCompletions},
	} {
		for _, heartbeat := range []bool{false, true} {
			t.Run(endpoint.path+"/heartbeat="+strconv.FormatBool(heartbeat), func(t *testing.T) {
				cfg := &config.Config{RunMode: config.RunModeSimple}
				group := &service.Group{ID: 1, Hydrated: true, Platform: service.PlatformAnthropic, Status: service.StatusActive}
				account := &service.Account{ID: 2, Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "test"}}
				repo := &toggleSettingRepo{values: map[string]string{}}
				settings := service.NewSettingService(repo, cfg)
				require.NoError(t, settings.SetClaudeMaxPolicySettings(context.Background(), &service.ClaudeMaxPolicySettings{Action: "block", Scope: "all", ErrorMessage: "请使用较低档位"}))
				gateway := service.NewGatewayService(
					nil, &fakeGroupRepo{group: group}, nil, nil, nil, nil, nil, nil, cfg,
					service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, nil, nil, nil),
					nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, settings, nil, nil, nil, nil, nil, nil,
				)
				billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
				t.Cleanup(billing.Stop)
				h := &GatewayHandler{
					gatewayService: gateway, billingCacheService: billing, cfg: cfg, maxAccountSwitches: 1,
					concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(&fakeConcurrencyCache{}), SSEPingFormatClaude, 0),
				}
				apiKey := &service.APIKey{ID: 3, UserID: 4, GroupID: &group.ID, Group: group, Status: service.StatusActive, User: &service.User{ID: 4, Concurrency: 10, Balance: 100}}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				ctx := context.WithValue(context.Background(), ctxkey.Group, group)
				c.Request = httptest.NewRequest(http.MethodPost, endpoint.path, strings.NewReader(endpoint.body)).WithContext(ctx)
				c.Request.Header.Set("Content-Type", "application/json")
				c.Set(string(middleware.ContextKeyAPIKey), apiKey)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 10})
				if heartbeat {
					c.Header("Content-Type", "text/event-stream")
					_, err := c.Writer.WriteString(":\n\n")
					require.NoError(t, err)
					c.Writer.Flush()
				}
				// No upstream client is configured: any forwarding would panic.
				endpoint.call(h, c)
				if heartbeat {
					require.Equal(t, http.StatusOK, recorder.Code)
					eventData := strings.TrimPrefix(recorder.Body.String(), ":\n\n")
					if endpoint.path == "/v1/responses" {
						require.Contains(t, eventData, "event: response.failed\n")
						require.Contains(t, eventData, "请使用较低档位")
					} else {
						require.True(t, strings.HasPrefix(eventData, "data: "), eventData)
						require.Equal(t, "请使用较低档位", gjson.Get(strings.TrimSpace(strings.TrimPrefix(eventData, "data: ")), "error.message").String())
					}
					return
				}
				require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
				require.Equal(t, "请使用较低档位", gjson.Get(recorder.Body.String(), "error.message").String())
			})
		}
	}
}

func TestGatewayClaudeMaxPolicyCanceledStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	ctx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	c.Header("Content-Type", "text/event-stream")
	_, err := c.Writer.WriteString(":\n\n")
	require.NoError(t, err)
	c.Writer.Flush()
	cancel()
	h := &GatewayHandler{}
	require.True(t, h.handleClaudeMaxPolicyError(c, &service.ClaudeMaxBlockedError{Message: "blocked"}, true, h.responsesErrorResponse))
	require.Equal(t, ":\n\n", recorder.Body.String(), "do not write to a disconnected client")
}
