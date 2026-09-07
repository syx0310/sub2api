package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newOpenAIWSThreadTestContext(groupID, apiKeyID int64, session, thread string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	c.Request.Header.Set("session-id", session)
	c.Request.Header.Set("thread-id", thread)
	c.Set("api_key", &APIKey{ID: apiKeyID, GroupID: &groupID})
	return c
}

func TestOpenAIWSThreadScopeIdentityAndPrecedence(t *testing.T) {
	canonical := resolveOpenAIWSThreadScope(newOpenAIWSThreadTestContext(7, 11, "root", "child"), nil)
	require.True(t, canonical.explicit)
	for _, tc := range []struct {
		name    string
		headers http.Header
		body    string
	}{
		{"underscore_headers", http.Header{"Session_id": {"root"}, "Thread_id": {"child"}}, ""},
		{"first_frame_metadata", nil, `{"client_metadata":{"session_id":"root","thread_id":"child"}}`},
		{"headers_win", http.Header{"Session-Id": {"root"}, "Thread-Id": {"child"}, "Thread_id": {"other"}}, `{"client_metadata":{"session_id":"wrong","thread_id":"wrong"}}`},
		{"header_session_body_thread", http.Header{"Session-Id": {"root"}}, `{"client_metadata":{"session_id":"wrong","thread_id":"child"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newOpenAIWSThreadTestContext(7, 11, "", "")
			c.Request.Header = tc.headers.Clone()
			originalHeaders := c.Request.Header.Clone()
			body := []byte(tc.body)
			scope := resolveOpenAIWSThreadScope(c, body)
			require.Equal(t, canonical, scope)
			require.Equal(t, tc.body, string(body))
			require.Equal(t, originalHeaders, c.Request.Header, "thread ownership must not rewrite wire identities")
		})
	}
	for _, c := range []*gin.Context{
		newOpenAIWSThreadTestContext(8, 11, "root", "child"),
		newOpenAIWSThreadTestContext(7, 12, "root", "child"),
		newOpenAIWSThreadTestContext(7, 11, "other-root", "child"),
		newOpenAIWSThreadTestContext(7, 11, "root", "parent"),
	} {
		require.NotEqual(t, canonical.hash, resolveOpenAIWSThreadScope(c, nil).hash)
	}

	c := newOpenAIWSThreadTestContext(7, 11, "root", "child")
	scope := resolveOpenAIWSThreadScope(c, nil)
	c.Request.Header.Set("thread-id", "rewritten-thread")
	c.Request.Header.Set("session-id", "rewritten-session")
	require.Equal(t, scope, resolveOpenAIWSThreadScope(c, []byte(`{"model":"gpt-6-astra","prompt_cache_key":"changed","client_metadata":{"thread_id":"other","turn_id":"new"}}`)), "retries and later frames cannot move connection ownership")

	left := newOpenAIWSThreadTestContext(7, 11, "root", "child")
	right := newOpenAIWSThreadTestContext(7, 11, "root", "child")
	left.Request.Header.Set("x-codex-parent-thread-id", "parent-a")
	right.Request.Header.Set("x-codex-parent-thread-id", "parent-b")
	right.Request.Header.Set("x-codex-window-id", "window-b")
	require.Equal(t, resolveOpenAIWSThreadScope(left, []byte(`{"model":"gpt-5.6-sol"}`)), resolveOpenAIWSThreadScope(right, []byte(`{"model":"gpt-6-astra","client_metadata":{"turn_id":"other"}}`)))
}

func TestOpenAIWSThreadScopeAnonymousStateIsConnectionLocal(t *testing.T) {
	first := newOpenAIWSThreadTestContext(7, 11, "root", "")
	second := newOpenAIWSThreadTestContext(7, 11, "root", "")
	body := []byte(`{"prompt_cache_key":"shared","client_metadata":{"parent_thread_id":"parent","thread_id":42}}`)
	a, b := resolveOpenAIWSThreadScope(first, body), resolveOpenAIWSThreadScope(second, body)
	require.False(t, a.explicit)
	require.False(t, b.explicit)
	require.NotEqual(t, a.hash, b.hash)
	require.Equal(t, a, resolveOpenAIWSThreadScope(first, nil))
	require.Empty(t, openAICodexTurnStateSeed(first), "root session alone cannot identify turn-state provenance")
	require.Empty(t, openAIWSPoolThreadScope(first, &Account{ID: 1}), "anonymous HTTP callers retain stateless transport reuse")
	require.Empty(t, resolveOpenAIWSThreadScope(nil, nil).hash)
}

func TestOpenAIWSThreadStateIsolationAndLegacyCaches(t *testing.T) {
	svc := &OpenAIGatewayService{}
	store := svc.getOpenAIWSStateStore()
	account := &Account{ID: 1}
	parent := newOpenAIWSThreadTestContext(7, 11, "root", "parent")
	parentKey := openAIWSThreadStateHash(parent, nil, account)
	store.BindSessionTurnState(7, parentKey, "parent-turn", time.Hour)
	store.BindSessionConn(7, parentKey, "parent-conn", time.Hour)
	store.MarkSessionInvalidEncryptedContent(7, parentKey, []string{"bad-for-parent"}, time.Hour)
	for _, tc := range []struct {
		c       *gin.Context
		account *Account
	}{
		{newOpenAIWSThreadTestContext(7, 11, "root", "child"), account},
		{newOpenAIWSThreadTestContext(7, 12, "root", "parent"), account},
		{newOpenAIWSThreadTestContext(8, 11, "root", "parent"), account},
		{newOpenAIWSThreadTestContext(7, 11, "root", "parent"), &Account{ID: 2}},
	} {
		key := openAIWSThreadStateHash(tc.c, nil, tc.account)
		require.NotEqual(t, parentKey, key)
		_, found := store.GetSessionTurnState(7, key)
		require.False(t, found)
		_, found = store.GetSessionConn(7, key)
		require.False(t, found)
		require.Empty(t, store.GetSessionInvalidEncryptedContentDigests(7, key))
	}
	reconnected := newOpenAIWSThreadTestContext(7, 11, "root", "parent")
	require.Equal(t, parentKey, openAIWSThreadStateHash(reconnected, nil, account))
	legacy := svc.GenerateSessionHash(parent, nil)
	store.BindSessionTurnState(7, legacy, "shared-legacy-state", time.Hour)
	other := newOpenAIWSThreadTestContext(7, 11, "root", "other")
	_, found := store.GetSessionTurnState(7, openAIWSThreadStateHash(other, nil, account))
	require.False(t, found)
	require.Equal(t, legacy, svc.GenerateSessionHash(other, nil), "routing/cache affinity remains shared, independently of thread transport state")
}

func TestOpenAIWSPoolThreadCompatibilityDoesNotDependOnFingerprint(t *testing.T) {
	for _, mode := range []string{"off", "device", "session", "full"} {
		t.Run(mode, func(t *testing.T) {
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{codexFingerprintModeExtraKey: mode, codexFingerprintSeedExtraKey: newCodexFingerprintSeed()}}
			require.Equal(t, codexFingerprintMode(mode), activeCodexFingerprintMode(account))
			parent := openAIWSAcquireRequest{Account: account, WSURL: "wss://example.com/responses", ThreadScope: "parent"}
			child := parent
			child.ThreadScope = "child"
			require.NotEqual(t, normalizeOpenAIWSAcquireCompatibility(parent), normalizeOpenAIWSAcquireCompatibility(child))
			require.False(t, sameOpenAIWSPrewarmTarget(parent, child))
			require.True(t, sameOpenAIWSPrewarmTarget(parent, cloneOpenAIWSAcquireRequest(parent)))
		})
	}
}

func TestOpenAIWSActiveOwnerSkipsCanceledWrites(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	withOpenAIWSActiveOwner(ctx, func() { called = true })
	require.False(t, called)
}
