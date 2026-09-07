//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func openAIWSThreadTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
	return cfg
}

func TestOpenAIWSSessionPreemptRegistryCancelsSameScopedSessionOnly(t *testing.T) {
	var registry openAIWSSessionPreemptRegistry
	key := openAIWSSessionPreemptKey{groupID: 7, apiKeyID: 11, sessionHash: "sess"}
	other := openAIWSSessionPreemptKey{groupID: 7, apiKeyID: 12, sessionHash: "sess"}
	firstCtx, firstCancel := context.WithCancel(context.Background())
	firstCleanup, replaced := registry.Begin(key, firstCancel)
	require.False(t, replaced)
	otherCtx, otherCancel := context.WithCancel(context.Background())
	otherCleanup, replaced := registry.Begin(other, otherCancel)
	require.False(t, replaced)
	secondCtx, secondCancel := context.WithCancel(context.Background())
	secondCleanup, replaced := registry.Begin(key, secondCancel)
	require.True(t, replaced)

	require.ErrorIs(t, firstCtx.Err(), context.Canceled)
	require.NoError(t, otherCtx.Err())
	require.NoError(t, secondCtx.Err())
	firstCleanup()
	require.NoError(t, secondCtx.Err(), "stale cleanup must not remove the replacement")

	secondCleanup()
	otherCleanup()
}

type openAIWSSessionPreemptCacheStub struct {
	GatewayCache
	mu     sync.Mutex
	owners map[string][]byte
}

func (c *openAIWSSessionPreemptCacheStub) key(groupID int64, hash string) string {
	return fmt.Sprintf("%d:%s", groupID, hash)
}

func (c *openAIWSSessionPreemptCacheStub) ClaimOpenAIResponsesSessionWindow(_ context.Context, groupID int64, hash string, owner []byte, _ time.Duration) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.owners == nil {
		c.owners = make(map[string][]byte)
	}
	key := c.key(groupID, hash)
	previous := append([]byte(nil), c.owners[key]...)
	c.owners[key] = append([]byte(nil), owner...)
	return previous, nil
}

func (c *openAIWSSessionPreemptCacheStub) CompareAndRefreshOpenAIResponsesSessionWindow(_ context.Context, groupID int64, hash string, expected []byte, _ time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.owners[c.key(groupID, hash)]) == string(expected), nil
}

func (c *openAIWSSessionPreemptCacheStub) CompareAndDeleteOpenAIResponsesSessionWindow(_ context.Context, groupID int64, hash string, expected []byte) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := c.key(groupID, hash)
	if string(c.owners[key]) != string(expected) {
		return false, nil
	}
	delete(c.owners, key)
	return true, nil
}

func TestOpenAIWSSessionPreemptContextEligibilityAndLocalCancellation(t *testing.T) {
	stateStore := NewOpenAIWSStateStore(nil)
	svc := &OpenAIGatewayService{openaiWSStateStore: stateStore}
	oauth := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	apiKey := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	grok := &Account{ID: 3, Platform: PlatformGrok, Type: AccountTypeOAuth}

	_, cleanup, armed, _ := svc.beginOpenAIWSSessionPreemptContext(context.Background(), apiKey, 7, 11, "sess", false)
	cleanup()
	require.False(t, armed)
	_, cleanup, armed, _ = svc.beginOpenAIWSSessionPreemptContext(context.Background(), grok, 7, 11, "sess", false)
	cleanup()
	require.False(t, armed)
	_, cleanup, armed, _ = svc.beginOpenAIWSSessionPreemptContext(context.Background(), oauth, 7, 11, "sess", true)
	cleanup()
	require.False(t, armed, "HTTP-ingress one-shot must not participate")

	firstCtx, firstCleanup, armed, replaced := svc.beginOpenAIWSSessionPreemptContext(context.Background(), oauth, 7, 11, "sess", false)
	require.True(t, armed)
	require.False(t, replaced)
	stateStore.BindSessionTurnState(7, "sess", "turn-state", time.Hour)
	stateStore.BindSessionConn(7, "sess", "conn-1", time.Hour)
	_, secondCleanup, armed, replaced := svc.beginOpenAIWSSessionPreemptContext(context.Background(), oauth, 7, 11, "sess", false)
	require.True(t, armed)
	require.True(t, replaced)
	require.True(t, isOpenAIWSSessionPreempted(firstCtx))
	require.True(t, IsOpenAIWSSessionPreemptedError(context.Cause(firstCtx)))
	_, turnStateExists := stateStore.GetSessionTurnState(7, "sess")
	_, sessionConnExists := stateStore.GetSessionConn(7, "sess")
	require.True(t, turnStateExists, "the losing callback must not delete shared or replacement state")
	require.True(t, sessionConnExists)
	firstCleanup()
	secondCleanup()
}

func TestOpenAIWSIngressSessionPreemptionSurvivesNestedForwardCleanup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(7)
	newContext := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
		c.Request.Header.Set("thread-id", "thread-1")
		c.Set("api_key", &APIKey{ID: 11, GroupID: &groupID})
		return c
	}

	svc := &OpenAIGatewayService{cfg: openAIWSThreadTestConfig()}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 8}
	firstMessage := []byte(`{"type":"response.create","prompt_cache_key":"session-1","input":"hello"}`)

	firstCtx, firstCleanup, armed := svc.BeginOpenAIWSIngressSessionPreemption(
		context.Background(), newContext(), account, firstMessage,
	)
	require.True(t, armed)
	defer firstCleanup()

	// ProxyResponsesWebSocketFromClient enters the same helper for each upstream
	// attempt. Its cleanup must not release the handler-owned registration.
	nestedCtx, nestedCleanup, armed := svc.BeginOpenAIWSIngressSessionPreemption(
		firstCtx, newContext(), account, firstMessage,
	)
	require.True(t, armed)
	require.Equal(t, firstCtx, nestedCtx)
	nestedCleanup()
	require.NoError(t, firstCtx.Err())

	_, secondCleanup, armed := svc.BeginOpenAIWSIngressSessionPreemption(
		context.Background(), newContext(), account, firstMessage,
	)
	require.True(t, armed)
	defer secondCleanup()
	require.True(t, IsOpenAIWSSessionPreemptedError(context.Cause(firstCtx)))
}

func TestOpenAIWSIngressSessionPreemptionRespectsResolvedMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(7)
	newContext := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
		c.Request.Header.Set("thread-id", "thread-1")
		c.Set("api_key", &APIKey{ID: 11, GroupID: &groupID})
		return c
	}
	newAccount := func(mode string) *Account {
		return &Account{
			ID:          1,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeOAuth,
			Concurrency: 8,
			Extra: map[string]any{
				"openai_oauth_responses_websockets_v2_mode": mode,
			},
		}
	}
	firstMessage := []byte(`{"type":"response.create","prompt_cache_key":"session-1","input":"hello"}`)
	cfg := openAIWSThreadTestConfig()
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
	svc := &OpenAIGatewayService{cfg: cfg}

	passthrough := newAccount(OpenAIWSIngressModePassthrough)
	firstCtx, firstCleanup, armed := svc.BeginOpenAIWSIngressSessionPreemption(
		context.Background(), newContext(), passthrough, firstMessage,
	)
	require.False(t, armed)
	defer firstCleanup()
	secondCtx, secondCleanup, armed := svc.BeginOpenAIWSIngressSessionPreemption(
		context.Background(), newContext(), passthrough, firstMessage,
	)
	require.False(t, armed)
	defer secondCleanup()
	require.NoError(t, firstCtx.Err(), "concurrent passthrough request must remain isolated")
	require.NoError(t, secondCtx.Err())

	ctxPool := newAccount(OpenAIWSIngressModeCtxPool)
	sharedCtx, sharedCleanup, armed := svc.BeginOpenAIWSIngressSessionPreemption(
		context.Background(), newContext(), ctxPool, firstMessage,
	)
	require.True(t, armed)
	defer sharedCleanup()
	_, replacementCleanup, armed := svc.BeginOpenAIWSIngressSessionPreemption(
		context.Background(), newContext(), ctxPool, firstMessage,
	)
	require.True(t, armed)
	defer replacementCleanup()
	require.True(t, IsOpenAIWSSessionPreemptedError(context.Cause(sharedCtx)), "ctx_pool must retain shared-session preemption")
}

func TestOpenAIWSSessionPreemptRemoteClaimAndStaleReleaseAreAtomic(t *testing.T) {
	cache := &openAIWSSessionPreemptCacheStub{}
	svc := &OpenAIGatewayService{cache: cache}
	key := openAIWSSessionPreemptKey{groupID: 7, apiKeyID: 11, sessionHash: "sess"}

	previous, ok := svc.claimOpenAIWSSessionPreemptOwner(context.Background(), key, "owner-a")
	require.True(t, ok)
	require.Empty(t, previous)
	previous, ok = svc.claimOpenAIWSSessionPreemptOwner(context.Background(), key, "owner-b")
	require.True(t, ok)
	require.Equal(t, "owner-a", previous)
	svc.releaseOpenAIWSSessionPreemptOwner(context.Background(), key, "owner-a")

	cache.mu.Lock()
	current := string(cache.owners[cache.key(key.groupID, openAIWSSessionPreemptCacheHash(key.apiKeyID, key.sessionHash))])
	cache.mu.Unlock()
	require.Equal(t, "owner-b", current, "stale cleanup must preserve the replacement owner")
}

func TestNewOpenAIWSSessionPreemptKeyRequiresFullIsolationScope(t *testing.T) {
	_, ok := newOpenAIWSSessionPreemptKey(0, 11, "sess")
	require.False(t, ok)
	_, ok = newOpenAIWSSessionPreemptKey(7, 0, "sess")
	require.False(t, ok)
	_, ok = newOpenAIWSSessionPreemptKey(7, 11, " ")
	require.False(t, ok)
	key, ok := newOpenAIWSSessionPreemptKey(7, 11, " sess ")
	require.True(t, ok)
	require.Equal(t, "sess", key.sessionHash)
}

func TestOpenAIWSIngressParentAndSubagentsDoNotPreemptEachOther(t *testing.T) {
	groupID := int64(7)
	newContext := func(thread string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
		c.Request.Header.Set("session-id", "root-session")
		c.Request.Header.Set("thread-id", thread)
		c.Set("api_key", &APIKey{ID: 11, GroupID: &groupID})
		return c
	}
	svc := &OpenAIGatewayService{cfg: openAIWSThreadTestConfig()}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 8}
	first := []byte(`{"type":"response.create","model":"gpt-5.6-sol","prompt_cache_key":"root-session","input":"hello"}`)
	var contexts []context.Context
	for _, thread := range []string{"parent", "subagent-a", "subagent-b", "guardian"} {
		ctx, cleanup, armed := svc.BeginOpenAIWSIngressSessionPreemption(context.Background(), newContext(thread), account, first)
		require.True(t, armed)
		t.Cleanup(cleanup)
		contexts = append(contexts, ctx)
	}
	for _, ctx := range contexts {
		require.NoError(t, ctx.Err(), "distinct threads sharing a root session must remain connected")
	}
	_, cleanup, armed := svc.BeginOpenAIWSIngressSessionPreemption(context.Background(), newContext("subagent-a"), account, first)
	require.True(t, armed)
	t.Cleanup(cleanup)
	require.True(t, isOpenAIWSSessionPreempted(contexts[1]), "a replacement only cancels its own thread")
	for _, i := range []int{0, 2, 3} {
		require.NoError(t, contexts[i].Err())
	}
}

func TestCodexTurnStateProvenanceDoesNotCrossSubagentThreads(t *testing.T) {
	newContext := func(thread string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		c.Request.Header.Set("session-id", "root-session")
		c.Request.Header.Set("thread-id", thread)
		c.Set("api_key", &APIKey{ID: 11})
		return c
	}
	svc := &OpenAIGatewayService{}
	parent, child := newContext("parent"), newContext("child")
	parentAccount, childAccount := &Account{ID: 1}, &Account{ID: 2}
	svc.noteOpenAICodexTurnStateProvenance(parent, parentAccount)
	svc.noteOpenAICodexTurnStateProvenance(child, childAccount)
	headers := http.Header{}
	headers.Set(openAICodexTurnStateHeader, "parent-turn-state")
	svc.guardOpenAICodexTurnStateEcho(parent, parentAccount, headers)
	require.Equal(t, "parent-turn-state", headers.Get(openAICodexTurnStateHeader))
}

func TestOpenAIWSIngressAnonymousThreadsNeverPreempt(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: openAIWSThreadTestConfig()}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 8}
	for range 3 {
		ctx, cleanup, armed := svc.BeginOpenAIWSIngressSessionPreemption(context.Background(), newOpenAIWSThreadTestContext(7, 11, "root", ""), account, []byte(`{"model":"gpt-5.6-sol","prompt_cache_key":"shared"}`))
		t.Cleanup(cleanup)
		require.False(t, armed)
		require.NoError(t, ctx.Err())
	}
	require.Empty(t, svc.openaiWSSessionPreemptions.active)
}

func TestOpenAIWSIngressEffectiveAstraRelaySkipsAndDisarmsOwner(t *testing.T) {
	for _, routerEnabled := range []bool{false, true} {
		t.Run(fmt.Sprint(routerEnabled), func(t *testing.T) {
			cfg := openAIWSThreadTestConfig()
			cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = routerEnabled
			svc := &OpenAIGatewayService{cfg: cfg}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 8, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
			first := []byte(`{"model":"gpt-5.6-sol","input":"hello"}`)
			c := newOpenAIWSThreadTestContext(7, 11, "root", "parent")
			ctx, cleanup, armed := svc.BeginOpenAIWSIngressSessionPreemption(context.Background(), c, account, first)
			t.Cleanup(cleanup)
			require.True(t, armed)
			// A mapped/current-turn failover can switch the actual route to Astra
			// even though the configured account mode and original model are pooled.
			next, noCleanup, armed := svc.BeginOpenAIWSIngressSessionPreemption(ctx, c, account, first, "gpt-6-astra")
			noCleanup()
			require.False(t, armed)
			require.Equal(t, ctx, next)
			require.NoError(t, ctx.Err())
			require.Empty(t, svc.openaiWSSessionPreemptions.active)
			_, replacementCleanup, armed := svc.BeginOpenAIWSIngressSessionPreemption(context.Background(), newOpenAIWSThreadTestContext(7, 11, "root", "parent"), account, first)
			t.Cleanup(replacementCleanup)
			require.True(t, armed)
			require.NoError(t, ctx.Err(), "the detached relay must not be canceled by a new pooled owner")
			for range 2 {
				relayCtx, relayCleanup, armed := svc.BeginOpenAIWSIngressSessionPreemption(context.Background(), newOpenAIWSThreadTestContext(7, 11, "root", "parent"), account, []byte(`{"model":"gpt-6-astra"}`))
				t.Cleanup(relayCleanup)
				require.False(t, armed)
				require.NoError(t, relayCtx.Err())
			}
		})
	}
}

func TestOpenAIWSIngressReplacementRejectsStaleStateWrites(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: openAIWSThreadTestConfig()}
	store := svc.getOpenAIWSStateStore()
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 8}
	c := newOpenAIWSThreadTestContext(7, 11, "root", "parent")
	first := []byte(`{"model":"gpt-5.6-sol"}`)
	oldCtx, oldCleanup, armed := svc.BeginOpenAIWSIngressSessionPreemption(context.Background(), c, account, first)
	require.True(t, armed)
	t.Cleanup(oldCleanup)
	stateKey := openAIWSThreadStateHash(c, first, account)
	store.BindSessionTurnState(7, stateKey, "old-turn", time.Hour)
	store.BindSessionConn(7, stateKey, "old-conn", time.Hour)

	newCtx, newCleanup, armed := svc.BeginOpenAIWSIngressSessionPreemption(context.Background(), newOpenAIWSThreadTestContext(7, 11, "root", "parent"), account, first)
	t.Cleanup(newCleanup)
	require.True(t, armed)
	require.True(t, isOpenAIWSSessionPreempted(oldCtx))
	_, found := store.GetSessionTurnState(7, stateKey)
	require.False(t, found, "only the replacement clears its own stale cache before forwarding")
	_, found = store.GetSessionConn(7, stateKey)
	require.False(t, found)
	withOpenAIWSActiveOwner(newCtx, func() {
		store.BindSessionTurnState(7, stateKey, "new-turn", time.Hour)
		store.BindSessionConn(7, stateKey, "new-conn", time.Hour)
	})
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			withOpenAIWSActiveOwner(oldCtx, func() {
				store.BindSessionTurnState(7, stateKey, "stale-turn", time.Hour)
				store.BindSessionConn(7, stateKey, "stale-conn", time.Hour)
			})
			oldCleanup()
		})
	}
	wg.Wait()
	turn, _ := store.GetSessionTurnState(7, stateKey)
	conn, _ := store.GetSessionConn(7, stateKey)
	require.Equal(t, "new-turn", turn)
	require.Equal(t, "new-conn", conn)
	require.NoError(t, newCtx.Err())
}

func TestOpenAIWSIngressRemoteOwnersIsolateSiblingThreads(t *testing.T) {
	cache := &openAIWSSessionPreemptCacheStub{}
	svcA := &OpenAIGatewayService{cfg: openAIWSThreadTestConfig(), cache: cache}
	svcB := &OpenAIGatewayService{cfg: openAIWSThreadTestConfig(), cache: cache}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 8}
	first := []byte(`{"model":"gpt-5.6-sol"}`)
	parent, parentCleanup, armed := svcA.BeginOpenAIWSIngressSessionPreemption(context.Background(), newOpenAIWSThreadTestContext(7, 11, "root", "parent"), account, first)
	t.Cleanup(parentCleanup)
	require.True(t, armed)
	child, childCleanup, armed := svcB.BeginOpenAIWSIngressSessionPreemption(context.Background(), newOpenAIWSThreadTestContext(7, 11, "root", "child"), account, first)
	t.Cleanup(childCleanup)
	require.True(t, armed)
	replacement, replacementCleanup, armed := svcB.BeginOpenAIWSIngressSessionPreemption(context.Background(), newOpenAIWSThreadTestContext(7, 11, "root", "parent"), account, first)
	t.Cleanup(replacementCleanup)
	require.True(t, armed)
	require.Eventually(t, func() bool { return isOpenAIWSSessionPreempted(parent) }, 5*time.Second, 10*time.Millisecond)
	parentCleanup()
	require.NoError(t, child.Err())
	require.NoError(t, replacement.Err())
	cache.mu.Lock()
	ownerCount := len(cache.owners)
	cache.mu.Unlock()
	require.Equal(t, 2, ownerCount, "stale remote cleanup must retain both sibling and replacement owners")
}
