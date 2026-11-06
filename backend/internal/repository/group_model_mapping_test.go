package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGroupModelMappingPersistenceAndAuth(t *testing.T) {
	keys, client := newAPIKeyRepoSQLite(t)
	groups := newGroupRepositoryWithSQL(client, keys.sql)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "group-mapping@test.com")
	group := &service.Group{Name: "mapped", Platform: service.PlatformOpenAI, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 1, ModelMapping: map[string]string{"alias": "gpt-5.4"}}
	require.NoError(t, groups.Create(ctx, group))
	key := &service.APIKey{UserID: user.ID, Key: "sk-group-mapping", Name: "mapping", GroupID: &group.ID, Status: service.StatusActive}
	require.NoError(t, keys.Create(ctx, key))
	loaded, err := groups.GetByID(ctx, group.ID)
	require.NoError(t, err)
	require.Equal(t, group.ModelMapping, loaded.ModelMapping)
	authenticated, err := keys.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.Equal(t, group.ModelMapping, authenticated.Group.ModelMapping)
	group.ModelMapping = map[string]string{"alias": "gpt-5.5"}
	require.NoError(t, groups.Update(ctx, group))
	authenticated, err = keys.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.Equal(t, group.ModelMapping, authenticated.Group.ModelMapping)
	group.ModelMapping = map[string]string{}
	require.NoError(t, groups.Update(ctx, group))
	loaded, err = groups.GetByID(ctx, group.ID)
	require.NoError(t, err)
	require.Empty(t, loaded.ModelMapping)
}
