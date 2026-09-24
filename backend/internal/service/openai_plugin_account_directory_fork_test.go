package service

import (
	"context"
	"testing"
	"time"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"github.com/stretchr/testify/require"
)

type pluginDirectoryAccounts struct {
	AccountRepository
	accounts []Account
}

func (r *pluginDirectoryAccounts) ListByPlatform(context.Context, string) ([]Account, error) {
	return r.accounts, nil
}

func (r *pluginDirectoryAccounts) GetByID(_ context.Context, id int64) (*Account, error) {
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			return &r.accounts[i], nil
		}
	}
	return nil, nil
}

func TestPluginDirectoryDoesNotResolveInactiveOrOutOfScopeCredentials(t *testing.T) {
	parent := int64(1)
	repo := &pluginDirectoryAccounts{accounts: []Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusDisabled},
		{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusError},
		{ID: 4, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive},
		{ID: 5, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, ParentAccountID: &parent},
		{ID: 6, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive},
		{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeSetupToken, Status: StatusActive},
	}}
	svc := &OpenAIGatewayService{accountRepo: repo}
	scope := newPluginAccountScope(pluginAccountScopeEntry{Platform: PlatformOpenAI, AccountType: AccountTypeOAuth})
	ids, err := svc.ListPluginAccounts(context.Background(), scope, PlatformOpenAI, AccountTypeOAuth)
	require.NoError(t, err)
	require.Len(t, ids, 1)
	require.Equal(t, int64(1), ids[0].ID)
	for _, id := range []int64{2, 3, 4, 5, 6, 7, 99} {
		identity, err := svc.ResolvePluginOutboundIdentity(context.Background(), scope, id)
		require.NoError(t, err)
		require.Nil(t, identity)
	}
	ids, err = svc.ListPluginAccounts(context.Background(), scope, PlatformAnthropic, "")
	require.NoError(t, err)
	require.Empty(t, ids)
}

type deadlinePluginKV struct {
	PluginKVStore
	deadline time.Time
}

func (s *deadlinePluginKV) List(ctx context.Context, _, _, _ string, _ int) ([]string, error) {
	s.deadline, _ = ctx.Deadline()
	return nil, nil
}

func TestPluginKVListHasBoundedHostBudget(t *testing.T) {
	store := &deadlinePluginKV{}
	server := newPluginHostServiceServer("test.plugin", store, nil, PluginAccountScope{})
	before := time.Now()
	_, err := server.KVList(context.Background(), &pluginv1.KVListRequest{Namespace: "state"})
	require.NoError(t, err)
	require.False(t, store.deadline.IsZero())
	require.WithinDuration(t, before.Add(pluginHostServiceTimeout), store.deadline, time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	_, err = server.KVList(ctx, &pluginv1.KVListRequest{Namespace: "state"})
	require.NoError(t, err)
	want, _ := ctx.Deadline()
	require.Equal(t, want, store.deadline, "a shorter caller deadline must not be extended")
}
