package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type unsupportedUsageAccountRepo struct {
	service.AccountRepository
	account *service.Account
	err     error
}

func (r *unsupportedUsageAccountRepo) GetByID(context.Context, int64) (*service.Account, error) {
	return r.account, r.err
}

func TestAccountUsageHandlerUnsupportedAPIKey(t *testing.T) {
	for _, platform := range []string{service.PlatformAnthropic, service.PlatformOpenAI} {
		for _, query := range []string{"", "?source=active", "?source=active&force=true", "?source=passive"} {
			t.Run(platform+"/"+query, func(t *testing.T) {
				repo := &unsupportedUsageAccountRepo{account: &service.Account{
					ID: 22, Platform: platform, Type: service.AccountTypeAPIKey,
				}}
				handler := &AccountHandler{accountUsageService: service.NewAccountUsageService(
					repo, nil, nil, nil, nil, nil, nil, nil, service.NewUsageCache(), nil, nil,
				)}
				recorder := httptest.NewRecorder()
				ctx, _ := gin.CreateTestContext(recorder)
				ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/22/usage"+query, nil)
				ctx.Params = gin.Params{{Key: "id", Value: "22"}}

				handler.GetUsage(ctx)

				require.Equal(t, http.StatusBadRequest, recorder.Code)
				var body response.Response
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
				require.Equal(t, http.StatusBadRequest, body.Code)
				require.Equal(t, "ACCOUNT_USAGE_NOT_SUPPORTED", body.Reason)
				require.Nil(t, body.Data)
			})
		}
	}
}

func TestAccountUsageHandlerRepositoryFailureRemainsInternalError(t *testing.T) {
	repo := &unsupportedUsageAccountRepo{err: errors.New("database unavailable")}
	handler := &AccountHandler{accountUsageService: service.NewAccountUsageService(
		repo, nil, nil, nil, nil, nil, nil, nil, service.NewUsageCache(), nil, nil,
	)}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/22/usage", nil)
	ctx.Params = gin.Params{{Key: "id", Value: "22"}}

	handler.GetUsage(ctx)

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
}
