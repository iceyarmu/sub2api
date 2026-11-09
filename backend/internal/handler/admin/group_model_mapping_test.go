package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupModelMappingSimpleModeAPI(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) { testGroupModelMappingSimpleModeAPI(t, platform) })
	}
}

func testGroupModelMappingSimpleModeAPI(t *testing.T, platform string) {
	svc := newStubAdminService()
	group := service.Group{ID: 7, Name: "mapped", Platform: platform, ModelMapping: map[string]string{"alias": "gpt-5.4"}}
	svc.groups = []service.Group{group}
	router := newSimpleModeGroupRouter(svc)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/groups", `{"name":"mapped","platform":"openai","model_mapping":{"alias":"gpt-5.4"}}`},
		{http.MethodPut, "/groups/7", `{"model_mapping":{"alias":"gpt-5.4"}}`},
		{http.MethodGet, "/groups", ""},
		{http.MethodGet, "/groups/all", ""},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(strings.ReplaceAll(tc.body, `"platform":"openai"`, `"platform":"`+platform+`"`)))
			req.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			require.Equal(t, http.StatusOK, res.Code, res.Body.String())
			if tc.method == http.MethodPost {
				require.Equal(t, group.ModelMapping, svc.createdGroups[0].ModelMapping)
			}
			if tc.method == http.MethodPut {
				require.Equal(t, group.ModelMapping, svc.updatedGroups[0].ModelMapping)
			}
			if tc.method == http.MethodGet {
				require.Contains(t, res.Body.String(), `"model_mapping":{"alias":"gpt-5.4"}`)
			}
		})
	}
	for _, body := range []string{`{"name":"renamed"}`, `{"model_mapping":{}}`} {
		req := httptest.NewRequest(http.MethodPut, "/groups/7", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		require.Equal(t, http.StatusOK, res.Code)
	}
	require.Nil(t, svc.updatedGroups[1].ModelMapping)
	require.NotNil(t, svc.updatedGroups[2].ModelMapping)
	require.Empty(t, svc.updatedGroups[2].ModelMapping)
	simple, err := json.Marshal(groupForSimpleMode(&group))
	require.NoError(t, err)
	require.Contains(t, string(simple), `"model_mapping":{"alias":"gpt-5.4"}`)
	data, err := json.Marshal(dto.GroupFromServiceAdmin(&group))
	require.NoError(t, err)
	require.Contains(t, string(data), `"model_mapping":{"alias":"gpt-5.4"}`)
}
