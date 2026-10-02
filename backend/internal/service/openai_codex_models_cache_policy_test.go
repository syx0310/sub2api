//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func catalogCacheSeconds(value int) *int { return &value }

func catalogCacheGroup(fresh, stale int) *Group {
	return &Group{ID: 7, Platform: PlatformOpenAI, CodexModelsManifestConfig: GroupCodexModelsManifestConfig{
		OAuthCacheTTLSeconds: catalogCacheSeconds(fresh), OAuthCacheStaleTTLSeconds: catalogCacheSeconds(stale),
	}}
}

func TestCodexModelsGroupCachePolicyValidationAndDefaults(t *testing.T) {
	fresh, stale := codexModelsCacheSeconds(GroupCodexModelsManifestConfig{})
	require.Equal(t, 60, fresh)
	require.Equal(t, 300, stale)
	for _, tc := range []struct {
		fresh, stale int
		valid        bool
	}{
		{0, 0, true}, {0, 30, true}, {15, 120, true}, {300, 300, true}, {600, 86400, true},
		{-1, 300, false}, {60, -1, false}, {301, 300, false}, {60, 86401, false},
	} {
		t.Run(fmt.Sprintf("%d/%d", tc.fresh, tc.stale), func(t *testing.T) {
			group := catalogCacheGroup(tc.fresh, tc.stale)
			repo := &groupRepoStubForAdmin{getByID: &Group{ID: group.ID, Platform: PlatformOpenAI, Status: StatusActive}}
			svc := &adminServiceImpl{groupRepo: repo}
			updated, err := svc.UpdateGroup(t.Context(), group.ID, &UpdateGroupInput{CodexModelsManifestConfig: &group.CodexModelsManifestConfig})
			if !tc.valid {
				require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
				require.Nil(t, repo.updated)
				_, err = svc.CreateGroup(t.Context(), &CreateGroupInput{Name: "catalog", Platform: PlatformOpenAI, RateMultiplier: 1, CodexModelsManifestConfig: group.CodexModelsManifestConfig})
				require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
				return
			}
			require.NoError(t, err)
			require.Equal(t, group.CodexModelsManifestConfig, updated.CodexModelsManifestConfig)
			require.False(t, updated.CodexModelsManifestConfig.Enabled, "cache settings do not require pinned accounts")
		})
	}
}

func TestCodexModelsGroupCachePolicySurvivesPartialUpdateAndGroupCopy(t *testing.T) {
	group := catalogCacheGroup(25, 180)
	repo := &groupRepoStubForAdmin{getByID: group}
	svc := &adminServiceImpl{groupRepo: repo}
	updated, err := svc.UpdateGroup(t.Context(), group.ID, &UpdateGroupInput{CodexModelsManifestConfig: &GroupCodexModelsManifestConfig{}})
	require.NoError(t, err)
	fresh, stale := codexModelsCacheSeconds(updated.CodexModelsManifestConfig)
	require.Equal(t, 25, fresh)
	require.Equal(t, 180, stale)
	group.CodexModelsManifestConfig.Enabled = true
	group.CodexModelsManifestConfig.AccountIDs = []int64{1}
	copy := cloneGroupForDuplicate(group, "test-copy")
	require.False(t, copy.CodexModelsManifestConfig.Enabled)
	require.Empty(t, copy.CodexModelsManifestConfig.AccountIDs)
	require.Equal(t, group.CodexModelsManifestConfig.OAuthCacheTTLSeconds, copy.CodexModelsManifestConfig.OAuthCacheTTLSeconds)
	*copy.CodexModelsManifestConfig.OAuthCacheTTLSeconds = 0
	require.Equal(t, 25, *group.CodexModelsManifestConfig.OAuthCacheTTLSeconds, "group copies must not alias the original configuration")
}

func TestCodexModelsGroupCachePolicyBoundaries(t *testing.T) {
	now := time.Now()
	policy := &openAIModelsCachePolicy{freshTTL: 17 * time.Second, staleTTL: 91 * time.Second}
	for _, tc := range []struct {
		age  time.Duration
		want openAIModelsCacheState
	}{
		{16 * time.Second, openAIModelsCacheFresh}, {17 * time.Second, openAIModelsCacheStale},
		{90 * time.Second, openAIModelsCacheStale}, {91 * time.Second, openAIModelsCacheMiss},
	} {
		cache := &openAIModelsCache{}
		cache.set("catalog", &OpenAIModelsResponse{Body: []byte(`{"models":[]}`)}, now, policy)
		_, state := cache.get("catalog", now.Add(tc.age))
		require.Equal(t, tc.want, state)
	}
	cache := &openAIModelsCache{}
	cache.set("disabled", &OpenAIModelsResponse{}, now, &openAIModelsCachePolicy{})
	require.Empty(t, cache.entries, "disabled cache must not retain response bodies")
}

func TestCodexModelsGroupCachePolicyOAuthIsolationAndAPIKeyUnaffected(t *testing.T) {
	_, calls := newCodexModelsOAuthCacheServer(t, `{"models":[{"slug":"test-oauth-model"}]}`)
	svc := &OpenAIGatewayService{}
	account := newCodexModelsTestAccount()
	shortCtx := WithCodexModelsCachePolicy(t.Context(), catalogCacheGroup(10, 40))
	longCtx := WithCodexModelsCachePolicy(t.Context(), catalogCacheGroup(120, 600))
	for _, ctx := range []context.Context{shortCtx, shortCtx, longCtx, longCtx} {
		_, err := svc.FetchCodexModelsManifest(ctx, account, "0.160.0", "")
		require.NoError(t, err)
	}
	require.EqualValues(t, 2, calls.Load(), "different policy profiles have separate cached values")
	svc.openAIModelsCache.mu.Lock()
	var spans []time.Duration
	for _, entry := range svc.openAIModelsCache.entries {
		spans = append(spans, entry.staleUntil.Sub(entry.expiresAt))
	}
	svc.openAIModelsCache.mu.Unlock()
	require.ElementsMatch(t, []time.Duration{30 * time.Second, 480 * time.Second}, spans)
	for range 2 {
		_, err := svc.FetchCodexModelsManifest(WithCodexModelsCachePolicy(t.Context(), catalogCacheGroup(0, 0)), account, "0.160.0", "")
		require.NoError(t, err)
	}
	require.EqualValues(t, 4, calls.Load())
	var apiCalls atomic.Int32
	api := newCodexModelsAPIKeyTestService(&codexModelsHTTPUpstreamStub{do: func(*http.Request, string, int64, int) (*http.Response, error) {
		apiCalls.Add(1)
		return ordinaryModelsUpstreamResponse(`{"models":[]}`), nil
	}})
	for _, ctx := range []context.Context{shortCtx, longCtx, WithCodexModelsCachePolicy(t.Context(), catalogCacheGroup(0, 0))} {
		_, err := api.FetchCodexModelsManifest(ctx, newCodexModelsAPIKeyTestAccount("https://example.invalid/v1"), "0.160.0", "")
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, apiCalls.Load(), "OAuth group overrides do not change API key catalog caching")
}

func TestCodexModelsGroupCachePolicyRefreshUsesConfiguredWindows(t *testing.T) {
	svc := &OpenAIGatewayService{}
	policy := &openAIModelsCachePolicy{freshTTL: 10 * time.Second, staleTTL: 40 * time.Second}
	request := openAIModelsRequest{accountID: 1, cachePolicy: policy}
	key := buildOpenAIModelsCacheKey(request)
	old := &OpenAIModelsResponse{Body: []byte(`{"models":[]}`), upstreamETag: `"source"`}
	svc.openAIModelsCache.set(key, old, time.Now().Add(-20*time.Second), policy)
	started, release := make(chan struct{}, 1), make(chan struct{})
	fetch := func(ctx context.Context, etag string) (*OpenAIModelsResponse, error) {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if etag != `"source"` {
			return nil, fmt.Errorf("missing upstream ETag: %s", etag)
		}
		return &OpenAIModelsResponse{NotModified: true}, nil
	}
	result, err := svc.fetchCachedOpenAIModels(t.Context(), request, fetch, "")
	require.NoError(t, err)
	require.Equal(t, old.Body, result.Body)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background refresh did not start")
	}
	// Joining the same flight verifies no duplicate refresh and lets the test
	// wait until the 304 has renewed the configured windows, not the defaults.
	joined := svc.refreshCachedOpenAIModels(key, request, fetch)
	close(release)
	require.NoError(t, (<-joined).Err)
	svc.openAIModelsCache.mu.Lock()
	entry := svc.openAIModelsCache.entries[key]
	svc.openAIModelsCache.mu.Unlock()
	require.Equal(t, 30*time.Second, entry.staleUntil.Sub(entry.expiresAt))
	require.WithinDuration(t, time.Now().Add(10*time.Second), entry.expiresAt, time.Second)
	svc.openAIModelsCache.set(key, old, time.Now().Add(-41*time.Second), policy)
	_, err = svc.fetchCachedOpenAIModels(t.Context(), request, func(context.Context, string) (*OpenAIModelsResponse, error) {
		return nil, fmt.Errorf("upstream unavailable")
	}, "")
	require.ErrorContains(t, err, "upstream unavailable", "overdue data must not be returned on a failed synchronous fetch")
}

func TestCodexModelsGroupCachePolicyPinnedDiscovery(t *testing.T) {
	_, calls := newCodexModelsOAuthCacheServer(t, `{"models":[{"slug":"pinned-oauth-model"}]}`)
	account := newCodexModelsTestAccount()
	account.Status = StatusActive
	account.Schedulable = true
	group := catalogCacheGroup(20, 180)
	group.CodexModelsManifestConfig.Enabled = true
	group.CodexModelsManifestConfig.AccountIDs = []int64{account.ID}
	svc := &OpenAIGatewayService{accountRepo: &accountRepoStubForGroupCodexManifest{accounts: []Account{*account}}}
	_, _, err := svc.FetchPinnedCodexModelsManifest(t.Context(), group, CodexCanonicalClientVersion())
	require.NoError(t, err)
	response, _, err := svc.FetchPinnedOpenAIModelsList(t.Context(), group, 1, "")
	require.NoError(t, err)
	require.Contains(t, string(response.Body), "pinned-oauth-model")
	require.EqualValues(t, 1, calls.Load(), "ordinary list and manifest share the same OAuth policy")
	svc.openAIModelsCache.mu.Lock()
	defer svc.openAIModelsCache.mu.Unlock()
	for _, entry := range svc.openAIModelsCache.entries {
		require.Equal(t, 160*time.Second, entry.staleUntil.Sub(entry.expiresAt))
	}
}
