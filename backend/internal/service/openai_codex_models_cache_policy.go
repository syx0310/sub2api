package service

import (
	"context"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const maxCodexModelsCacheSeconds = 86400

type openAIModelsCachePolicy struct {
	freshTTL time.Duration
	staleTTL time.Duration
}

type codexModelsCachePolicyContextKey struct{}

func codexModelsCacheSeconds(cfg GroupCodexModelsManifestConfig) (int, int) {
	fresh, stale := int(openAIModelsCacheTTL/time.Second), int(openAIModelsCacheStaleTTL/time.Second)
	if cfg.OAuthCacheTTLSeconds != nil {
		fresh = *cfg.OAuthCacheTTLSeconds
	}
	if cfg.OAuthCacheStaleTTLSeconds != nil {
		stale = *cfg.OAuthCacheStaleTTLSeconds
	}
	return fresh, stale
}

func validateCodexModelsCachePolicy(cfg GroupCodexModelsManifestConfig) error {
	fresh, stale := codexModelsCacheSeconds(cfg)
	if fresh < 0 || stale < fresh || stale > maxCodexModelsCacheSeconds {
		return infraerrors.BadRequest("INVALID_CODEX_MODELS_MANIFEST_CONFIG", "OAuth catalog cache times must satisfy 0 <= fresh TTL <= maximum age <= 86400 seconds")
	}
	return nil
}

// WithCodexModelsCachePolicy carries group settings through pinned discovery,
// scheduled discovery and fallback without changing account-level probe callers.
// FetchCodexModelsManifest applies this context only to OAuth upstreams.
func WithCodexModelsCachePolicy(ctx context.Context, group *Group) context.Context {
	policy := effectiveOpenAIModelsCachePolicy(nil)
	if group != nil && group.Platform == PlatformOpenAI && validateCodexModelsCachePolicy(group.CodexModelsManifestConfig) == nil {
		fresh, stale := codexModelsCacheSeconds(group.CodexModelsManifestConfig)
		policy = openAIModelsCachePolicy{freshTTL: time.Duration(fresh) * time.Second, staleTTL: time.Duration(stale) * time.Second}
	}
	return context.WithValue(ctx, codexModelsCachePolicyContextKey{}, &policy)
}

func effectiveOpenAIModelsCachePolicy(policy *openAIModelsCachePolicy) openAIModelsCachePolicy {
	if policy != nil {
		return *policy
	}
	return openAIModelsCachePolicy{freshTTL: openAIModelsCacheTTL, staleTTL: openAIModelsCacheStaleTTL}
}
