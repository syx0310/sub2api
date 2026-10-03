package service

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func sizedOAuthCatalog(size int) []byte {
	prefix, suffix := `{"models":[{"slug":"catalog-test","description":"`, `"}]}`
	return []byte(prefix + strings.Repeat("x", size-len(prefix)-len(suffix)) + suffix)
}

func TestOAuthCatalogCacheKeepsOneBodyWithinSizeLimit(t *testing.T) {
	for _, tc := range []struct {
		name        string
		size, calls int
	}{
		{"600 KiB", 600 << 10, 1},
		{"exactly 1 MiB", openAIModelsCacheBodyLimit, 1},
		{"over 1 MiB", openAIModelsCacheBodyLimit + 1, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := sizedOAuthCatalog(tc.size)
			_, calls := newCodexModelsOAuthCacheServer(t, string(body))
			svc := &OpenAIGatewayService{}
			account := newCodexModelsTestAccount()
			fresh, stale := 120, 600
			ctx := WithCodexModelsCachePolicy(t.Context(), &Group{Platform: PlatformOpenAI, CodexModelsManifestConfig: GroupCodexModelsManifestConfig{OAuthCacheTTLSeconds: &fresh, OAuthCacheStaleTTLSeconds: &stale}})
			first, err := svc.FetchCodexModelsManifest(ctx, account, "0.160.0", "")
			require.NoError(t, err)
			require.True(t, bytes.Equal(body, first.Body), "OAuth catalog must remain byte-for-byte verbatim")
			require.Nil(t, first.upstreamSourceBody)
			first.Body[0] = '!' // Client-side transformation cannot modify the shared cached body.
			second, err := svc.FetchCodexModelsManifest(ctx, account, "0.160.0", "")
			require.NoError(t, err)
			require.True(t, bytes.Equal(body, second.Body))
			require.EqualValues(t, tc.calls, calls.Load())
			svc.openAIModelsCache.mu.Lock()
			defer svc.openAIModelsCache.mu.Unlock()
			if tc.calls == 1 {
				require.Len(t, svc.openAIModelsCache.entries, 1)
				for _, entry := range svc.openAIModelsCache.entries {
					require.Nil(t, entry.manifest.upstreamSourceBody)
					require.Equal(t, tc.size, len(entry.manifest.Body))
					require.Equal(t, 480*time.Second, entry.staleUntil.Sub(entry.expiresAt))
				}
			} else {
				require.Empty(t, svc.openAIModelsCache.entries, "capacity guard still applies")
			}
		})
	}
}

func TestOAuthLargeCatalogCacheRevalidatesWithETag(t *testing.T) {
	body := sizedOAuthCatalog(600 << 10)
	var calls atomic.Int32
	seenETag := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		w.Header().Set("ETag", `"catalog-etag"`)
		if call > 1 {
			seenETag <- r.Header.Get("If-None-Match")
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	original := chatgptCodexModelsURL
	chatgptCodexModelsURL = server.URL
	t.Cleanup(func() { chatgptCodexModelsURL = original })
	svc := &OpenAIGatewayService{}
	account := newCodexModelsTestAccount()
	first, err := svc.FetchCodexModelsManifest(t.Context(), account, "0.160.0", "")
	require.NoError(t, err)
	require.Equal(t, `"catalog-etag"`, first.ETag)
	// Move only this test's entry into its stale window, without waiting a minute.
	svc.openAIModelsCache.mu.Lock()
	require.Len(t, svc.openAIModelsCache.entries, 1)
	for key, entry := range svc.openAIModelsCache.entries {
		entry.expiresAt = time.Now().Add(-time.Second)
		svc.openAIModelsCache.entries[key] = entry
	}
	svc.openAIModelsCache.mu.Unlock()
	stale, err := svc.FetchCodexModelsManifest(t.Context(), account, "0.160.0", "")
	require.NoError(t, err)
	require.True(t, bytes.Equal(body, stale.Body))
	select {
	case etag := <-seenETag:
		require.Equal(t, `"catalog-etag"`, etag)
	case <-time.After(2 * time.Second):
		t.Fatal("background revalidation did not run")
	}
	require.Eventually(t, func() bool {
		svc.openAIModelsCache.mu.Lock()
		defer svc.openAIModelsCache.mu.Unlock()
		for _, entry := range svc.openAIModelsCache.entries {
			return entry.expiresAt.After(time.Now())
		}
		return false
	}, 2*time.Second, time.Millisecond)
	again, err := svc.FetchCodexModelsManifest(t.Context(), account, "0.160.0", "")
	require.NoError(t, err)
	require.True(t, bytes.Equal(body, again.Body))
	notModified, err := svc.FetchCodexModelsManifest(t.Context(), account, "0.160.0", `"catalog-etag"`)
	require.NoError(t, err)
	require.True(t, notModified.NotModified)
	require.EqualValues(t, 2, calls.Load(), "the second bodyless 304 must renew the cache")
}

func TestAPIKeyCatalogRetainsPristineSourceAndCombinedSizeLimit(t *testing.T) {
	for _, body := range []string{`{"models":[{"slug":"test-api-model","unknown":"keep"}]}`, `{"data":[{"id":"test-api-model"}]}`} {
		svc := newCodexModelsAPIKeyTestService(&codexModelsHTTPUpstreamStub{do: func(*http.Request, string, int64, int) (*http.Response, error) {
			return ordinaryModelsUpstreamResponse(body), nil
		}})
		account := newCodexModelsAPIKeyTestAccount("https://example.invalid/v1")
		response, err := svc.FetchCodexModelsManifest(t.Context(), account, "0.160.0", "")
		require.NoError(t, err)
		require.Equal(t, body, string(response.upstreamSourceBody))
		require.NoError(t, svc.CompleteAPIKeyCodexModelsManifestForClient(response, account))
		require.Contains(t, string(response.Body), "test-api-model")
	}
	cache := &openAIModelsCache{}
	cache.set("too-large", &OpenAIModelsResponse{Body: make([]byte, 600<<10), upstreamSourceBody: make([]byte, 600<<10)}, time.Now())
	require.Empty(t, cache.entries, "API key's two distinct bodies still share the 1 MiB budget")
}
