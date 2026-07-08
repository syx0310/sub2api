//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/websearch"
	"github.com/stretchr/testify/require"
)

// --- validateWebSearchConfig ---

func validateWebSearchConfigForTest(cfg *WebSearchEmulationConfig) error {
	return (&SettingService{}).validateWebSearchConfig(cfg)
}

func TestValidateWebSearchConfig_Nil(t *testing.T) {
	require.NoError(t, validateWebSearchConfigForTest(nil))
}

func TestValidateWebSearchConfig_Valid(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Enabled: true,
		Providers: []WebSearchProviderConfig{
			{Type: "brave", QuotaLimit: int64Ptr(1000)},
			{Type: "tavily", QuotaLimit: int64Ptr(500)},
			{Type: "tavily_hikari", APIBaseURL: "https://hikari.example.com/api/tavily", QuotaLimit: int64Ptr(500)},
		},
	}
	require.NoError(t, validateWebSearchConfigForTest(cfg))
}

func TestValidateWebSearchConfig_TooManyProviders(t *testing.T) {
	cfg := &WebSearchEmulationConfig{Providers: make([]WebSearchProviderConfig, 11)}
	for i := range cfg.Providers {
		cfg.Providers[i] = WebSearchProviderConfig{Type: "brave"}
	}
	err := validateWebSearchConfigForTest(cfg)
	require.ErrorContains(t, err, "too many providers")
}

func TestValidateWebSearchConfig_InvalidType(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{{Type: "bing"}},
	}
	require.ErrorContains(t, validateWebSearchConfigForTest(cfg), "invalid type")
}

func TestValidateWebSearchConfig_NegativeQuotaLimit(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{{Type: "brave", QuotaLimit: int64Ptr(-1)}},
	}
	require.ErrorContains(t, validateWebSearchConfigForTest(cfg), "quota_limit must be > 0 or null")
}

func TestValidateWebSearchConfig_DuplicateType(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{
			{Type: "brave"},
			{Type: "brave"},
		},
	}
	require.ErrorContains(t, validateWebSearchConfigForTest(cfg), "duplicate type")
}

func TestValidateWebSearchConfig_NilQuotaLimit(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{{Type: "brave", QuotaLimit: nil}},
	}
	require.NoError(t, validateWebSearchConfigForTest(cfg))
}

func TestValidateWebSearchConfig_TavilyHikariAPIBaseURL(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{{Type: "tavily_hikari", APIBaseURL: "https://hikari.example.com/api/tavily"}},
	}
	require.NoError(t, validateWebSearchConfigForTest(cfg))

	cfg.Providers[0].APIBaseURL = "http://192.168.10.23:3012/api/tavily"
	require.NoError(t, validateWebSearchConfigForTest(cfg))
}

func TestValidateWebSearchConfig_TavilyHikariRequiresAPIBaseURL(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{{Type: "tavily_hikari"}},
	}
	require.ErrorContains(t, validateWebSearchConfigForTest(cfg), "api_base_url is required for tavily_hikari")
}

func TestValidateWebSearchConfig_APIBaseURLOnlySupportedForTavilyHikari(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{{Type: "brave", APIBaseURL: "https://hikari.example.com/api/tavily"}},
	}
	require.ErrorContains(t, validateWebSearchConfigForTest(cfg), "api_base_url is only supported for tavily_hikari")
}

func TestValidateWebSearchConfig_InvalidAPIBaseURL(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{{Type: "tavily_hikari", APIBaseURL: "ftp://hikari.example.com/api/tavily"}},
	}
	require.ErrorContains(t, validateWebSearchConfigForTest(cfg), "api_base_url must use http or https")

	cfg.Providers[0].APIBaseURL = "https://hikari.example.com/api/tavily?token=bad"
	require.ErrorContains(t, validateWebSearchConfigForTest(cfg), "api_base_url must not include query or fragment")

	cfg.Providers[0].APIBaseURL = "http://192.168.10.23:3012/mcp"
	require.ErrorContains(t, validateWebSearchConfigForTest(cfg), "not MCP /mcp")
}

func TestSettingServiceValidateWebSearchConfig_EnforcesURLAllowlist(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{{
			Type:       "tavily_hikari",
			APIBaseURL: "https://blocked.example.com/api/tavily",
		}},
	}
	svc := NewSettingService(nil, &config.Config{Security: config.SecurityConfig{
		URLAllowlist: config.URLAllowlistConfig{
			Enabled:           true,
			UpstreamHosts:     []string{"allowed.example.com"},
			AllowPrivateHosts: true,
		},
	}})
	require.ErrorContains(t, svc.validateWebSearchConfig(cfg), "host is not allowed")
}

// --- parseWebSearchConfigJSON ---

func TestParseWebSearchConfigJSON_ValidJSON(t *testing.T) {
	raw := `{"enabled":true,"providers":[{"type":"tavily_hikari","api_key":"sk-xxx","api_base_url":"https://hikari.example.com/api/tavily"}]}`
	cfg := parseWebSearchConfigJSON(raw)
	require.True(t, cfg.Enabled)
	require.Len(t, cfg.Providers, 1)
	require.Equal(t, "tavily_hikari", cfg.Providers[0].Type)
	require.Equal(t, "https://hikari.example.com/api/tavily", cfg.Providers[0].APIBaseURL)
}

func TestParseWebSearchConfigJSON_EmptyString(t *testing.T) {
	cfg := parseWebSearchConfigJSON("")
	require.False(t, cfg.Enabled)
	require.Empty(t, cfg.Providers)
}

func TestParseWebSearchConfigJSON_InvalidJSON(t *testing.T) {
	cfg := parseWebSearchConfigJSON("not{json")
	require.False(t, cfg.Enabled)
	require.Empty(t, cfg.Providers)
}

func TestParseWebSearchConfigJSON_BackwardCompatibility(t *testing.T) {
	// Old config with priority and quota_refresh_interval should parse without error
	raw := `{"enabled":true,"providers":[{"type":"brave","priority":1,"quota_refresh_interval":"monthly","quota_limit":1000}]}`
	cfg := parseWebSearchConfigJSON(raw)
	require.True(t, cfg.Enabled)
	require.Len(t, cfg.Providers, 1)
	require.Equal(t, int64(1000), *cfg.Providers[0].QuotaLimit)
}

// --- SanitizeWebSearchConfig ---

func TestSanitizeWebSearchConfig_MaskAPIKey(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Enabled: true,
		Providers: []WebSearchProviderConfig{
			{Type: "brave", APIKey: "sk-secret-xxx"},
		},
	}
	out := SanitizeWebSearchConfig(context.Background(), cfg)
	require.Equal(t, "", out.Providers[0].APIKey)
	require.True(t, out.Providers[0].APIKeyConfigured)
}

func TestSanitizeWebSearchConfig_NoAPIKey(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{{Type: "brave", APIKey: ""}},
	}
	out := SanitizeWebSearchConfig(context.Background(), cfg)
	require.Equal(t, "", out.Providers[0].APIKey)
	require.False(t, out.Providers[0].APIKeyConfigured)
}

func TestSanitizeWebSearchConfig_Nil(t *testing.T) {
	require.Nil(t, SanitizeWebSearchConfig(context.Background(), nil))
}

func TestSanitizeWebSearchConfig_PreservesOtherFields(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Enabled: true,
		Providers: []WebSearchProviderConfig{
			{Type: "tavily_hikari", APIKey: "secret", APIBaseURL: "https://hikari.example.com/api/tavily", QuotaLimit: int64Ptr(1000)},
		},
	}
	out := SanitizeWebSearchConfig(context.Background(), cfg)
	require.True(t, out.Enabled)
	require.Equal(t, "https://hikari.example.com/api/tavily", out.Providers[0].APIBaseURL)
	require.Equal(t, int64(1000), *out.Providers[0].QuotaLimit)
}

func TestSanitizeWebSearchConfig_DoesNotMutateOriginal(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{{Type: "brave", APIKey: "secret"}},
	}
	_ = SanitizeWebSearchConfig(context.Background(), cfg)
	require.Equal(t, "secret", cfg.Providers[0].APIKey)
}

// --- PopulateWebSearchUsage ---

func TestPopulateWebSearchUsage_NilInput(t *testing.T) {
	require.Nil(t, PopulateWebSearchUsage(context.Background(), nil))
}

func TestPopulateWebSearchUsage_NoManager_QuotaUsedZero(t *testing.T) {
	// Ensure no global manager is set
	SetWebSearchManager(nil)
	defer SetWebSearchManager(nil)

	cfg := &WebSearchEmulationConfig{
		Enabled: true,
		Providers: []WebSearchProviderConfig{
			{Type: "brave", APIKey: "sk-key", QuotaLimit: int64Ptr(1000)},
		},
	}
	out := PopulateWebSearchUsage(context.Background(), cfg)
	require.NotNil(t, out)
	require.Len(t, out.Providers, 1)
	require.Equal(t, int64(0), out.Providers[0].QuotaUsed)
}

func TestPopulateWebSearchUsage_APIKeyConfigured_True(t *testing.T) {
	SetWebSearchManager(nil)
	defer SetWebSearchManager(nil)

	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{
			{Type: "brave", APIKey: "sk-key"},
		},
	}
	out := PopulateWebSearchUsage(context.Background(), cfg)
	require.True(t, out.Providers[0].APIKeyConfigured)
}

func TestPopulateWebSearchUsage_APIKeyConfigured_False(t *testing.T) {
	SetWebSearchManager(nil)
	defer SetWebSearchManager(nil)

	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{
			{Type: "brave", APIKey: ""},
		},
	}
	out := PopulateWebSearchUsage(context.Background(), cfg)
	require.False(t, out.Providers[0].APIKeyConfigured)
}

func TestPopulateWebSearchUsage_NilQuotaLimit(t *testing.T) {
	SetWebSearchManager(nil)
	defer SetWebSearchManager(nil)

	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{
			{Type: "brave", APIKey: "sk-key", QuotaLimit: nil},
		},
	}
	out := PopulateWebSearchUsage(context.Background(), cfg)
	require.Nil(t, out.Providers[0].QuotaLimit)
}

func TestPopulateWebSearchUsage_NonNilQuotaLimit(t *testing.T) {
	SetWebSearchManager(nil)
	defer SetWebSearchManager(nil)

	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{
			{Type: "brave", APIKey: "sk-key", QuotaLimit: int64Ptr(500)},
		},
	}
	out := PopulateWebSearchUsage(context.Background(), cfg)
	require.NotNil(t, out.Providers[0].QuotaLimit)
	require.Equal(t, int64(500), *out.Providers[0].QuotaLimit)
}

func TestPopulateWebSearchUsage_WithManager_NilRedis(t *testing.T) {
	// Manager with nil Redis returns 0 usage without error
	mgr := websearch.NewManager([]websearch.ProviderConfig{
		{Type: "brave", APIKey: "k"},
	}, nil)
	SetWebSearchManager(mgr)
	defer SetWebSearchManager(nil)

	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{
			{Type: "brave", APIKey: "sk-key", QuotaLimit: int64Ptr(1000)},
		},
	}
	out := PopulateWebSearchUsage(context.Background(), cfg)
	require.Equal(t, int64(0), out.Providers[0].QuotaUsed)
	require.True(t, out.Providers[0].APIKeyConfigured)
}

func TestPopulateWebSearchUsage_DoesNotMutateOriginal(t *testing.T) {
	SetWebSearchManager(nil)
	defer SetWebSearchManager(nil)

	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{
			{Type: "brave", APIKey: "secret", QuotaLimit: int64Ptr(100)},
		},
	}
	_ = PopulateWebSearchUsage(context.Background(), cfg)
	// Original should be unchanged
	require.Equal(t, "secret", cfg.Providers[0].APIKey)
	require.Equal(t, int64(0), cfg.Providers[0].QuotaUsed)
}

// --- ResetWebSearchUsage ---

func TestResetWebSearchUsage_NilManager(t *testing.T) {
	SetWebSearchManager(nil)
	defer SetWebSearchManager(nil)

	err := ResetWebSearchUsage(context.Background(), "brave")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not initialized")
}
