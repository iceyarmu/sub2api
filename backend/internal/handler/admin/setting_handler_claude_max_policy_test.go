package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type claudeMaxSettingsRepoStub struct{ settingHandlerRepoStub }

func (r *claudeMaxSettingsRepoStub) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func TestClaudeMaxPolicySettingsAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &claudeMaxSettingsRepoStub{settingHandlerRepoStub{values: map[string]string{}}}
	h := NewSettingHandler(service.NewSettingService(repo, &config.Config{}), nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.GET("/policy", h.GetClaudeMaxPolicySettings)
	router.PUT("/policy", h.UpdateClaudeMaxPolicySettings)
	request := func(method, body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/policy", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, req)
		return recorder
	}
	res := request(http.MethodGet, "")
	require.Equal(t, http.StatusOK, res.Code)
	require.Equal(t, "pass", gjson.Get(res.Body.String(), "data.action").String())
	for _, body := range []string{
		`{"action":"downgrade","scope":"all","downgrade_effort":"medium","error_message":""}`,
		`{"action":"block","scope":"oauth","downgrade_effort":"high","error_message":"请使用较低档位"}`,
	} {
		res = request(http.MethodPut, body)
		require.Equal(t, http.StatusOK, res.Code, res.Body.String())
		res = request(http.MethodGet, "")
		require.Equal(t, http.StatusOK, res.Code)
		require.JSONEq(t, body, gjson.Get(res.Body.String(), "data").Raw)
	}
	stored := repo.values[service.SettingKeyClaudeMaxPolicySettings]
	for _, body := range []string{
		`{broken`, `{}`, `null`,
		`{"action":"downgrade","scope":"all","downgrade_effort":"max"}`,
		`{"action":"downgrade","scope":"all"}`,
		`{"action":"filter","scope":"all"}`,
		`{"action":"block","scope":"unknown"}`,
	} {
		res = request(http.MethodPut, body)
		require.Equal(t, http.StatusBadRequest, res.Code, body)
		require.Equal(t, stored, repo.values[service.SettingKeyClaudeMaxPolicySettings])
	}
}
