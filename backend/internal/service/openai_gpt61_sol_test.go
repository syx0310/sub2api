package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPT61SolCatalogAndRouting(t *testing.T) {
	t.Parallel()
	const model = openai.GPT61SolModelID
	body, err := BuildCodexModelsManifest([]string{model})
	require.NoError(t, err)
	entry := decodeCodexManifestModels(t, body)[0]
	require.Equal(t, "low", entry["default_reasoning_level"])
	require.Equal(t, "xhigh", entry["multi_agent_reasoning_effort"])
	require.Equal(t, "v2", entry["multi_agent_version"])
	require.EqualValues(t, 272000, entry["context_window"])
	require.EqualValues(t, 872000, entry["max_context_window"])
	require.Equal(t, true, entry["supports_reasoning_effort_updates"])
	require.Equal(t, true, entry["use_responses_lite"])
	require.Equal(t, true, entry["prefer_websockets"])
	require.Equal(t, true, entry["node_repl_auto_review_required"])
	require.Equal(t, "0.153.0", entry["minimal_client_version"])
	require.Nil(t, entry["default_service_tier"])
	require.Equal(t, `["low","medium","high","xhigh","max","ultra"]`, gjson.GetBytes(body, "models.0.supported_reasoning_levels.#.effort").Raw)
	require.Contains(t, openai.DefaultModelIDs(), model)
	require.True(t, configuredCodexSupportsPriorityServiceTier(model))
	require.False(t, configuredCodexSupportsUltrafastServiceTier(model))
	require.Equal(t, model, normalizeKnownOpenAICodexModel("openai/"+model))
	for _, invalid := range []string{"gpt-6.1", "gpt-6.1-sol-pro", "gpt-6.1-sol-codex", "gpt-6.1-sol-2026-09-30"} {
		require.False(t, isOpenAIGPT6Model(invalid))
		require.Empty(t, normalizeKnownOpenAICodexModel(invalid))
		require.Nil(t, (&PricingService{}).GetModelPricing(invalid))
	}

	account := Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		ModelIDsSyncedAt: time.Now().UTC().Format(time.RFC3339), ModelIDs: []string{model, "gpt-6-sol"},
	})
	require.True(t, account.IsModelSupported(model))
	require.Contains(t, openAIConfiguredAndObservedCodexModelIDsForGroup([]Account{account}, &Group{}), model)
	require.Equal(t, []string{model}, openAIConfiguredAndObservedCodexModelIDsForGroup([]Account{account}, &Group{
		ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{model}},
	}))
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		ModelIDsSyncedAt: time.Now().UTC().Format(time.RFC3339), ModelIDs: []string{"gpt-6-sol"},
	})
	require.False(t, account.IsModelSupported(model), "old Sol access does not establish 6.1 access")
}

func TestGPT61SolAPIKeyCatalogPreservesNativeMetadata(t *testing.T) {
	t.Parallel()
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"base_url": "https://api.openai.com/v1", "model_mapping": map[string]any{"alias": openai.GPT61SolModelID},
	}}
	converted := convertOpenAIModelListToCodexManifestForAccount([]byte(`{"data":[{"id":"alias"}]}`), account)
	converted, err := applySyncedAPIKeyCodexModelMetadata(converted, account, true)
	require.NoError(t, err)
	entry := decodeCodexManifestModels(t, converted)[0]
	require.Equal(t, "alias", entry["slug"])
	require.Equal(t, "low", entry["default_reasoning_level"])
	require.Equal(t, "xhigh", entry["multi_agent_reasoning_effort"])
	require.Equal(t, false, entry["use_responses_lite"])
	require.EqualValues(t, 872000, entry["max_context_window"])
	// Explicit upstream declarations take precedence over offline defaults.
	native := []byte(`{"models":[{"slug":"gpt-6.1-sol","supports_reasoning_effort_updates":false,"multi_agent_reasoning_effort":null,"default_service_tier":null,"context_window":300000,"max_context_window":900000}]}`)
	preserved, err := applySyncedAPIKeyCodexModelMetadata(native, account, false)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(preserved, "models.0.supports_reasoning_effort_updates").Bool())
	require.EqualValues(t, 300000, gjson.GetBytes(preserved, "models.0.context_window").Int())
	require.Equal(t, gjson.Null, gjson.GetBytes(preserved, "models.0.multi_agent_reasoning_effort").Type)
	require.Equal(t, gjson.Null, gjson.GetBytes(preserved, "models.0.default_service_tier").Type)
}

func TestGPT61SolPricingOverrides(t *testing.T) {
	t.Parallel()
	const model = openai.GPT61SolModelID
	custom := &LiteLLMModelPricing{InputCostPerToken: 123e-6, OutputCostPerToken: 456e-6}
	pricing := &PricingService{pricingData: map[string]*LiteLLMModelPricing{model: custom}}
	require.Same(t, custom, pricing.GetModelPricing(model))
	require.NotSame(t, openAIGPT6SolFallbackPricing, openAIGPT61SolFallbackPricing)
	svc := NewBillingService(&config.Config{}, nil)
	zero, read := 0.0, 7e-6
	price, err := svc.GetModelPricingWithChannel(model, &ChannelModelPricing{CacheWritePrice: &zero, CacheReadPrice: &read})
	require.NoError(t, err)
	require.Zero(t, price.CacheCreationPricePerToken)
	require.InDelta(t, read, price.CacheReadPricePerToken, 1e-12)
}

func TestGPT61SolEUOnlyExcludesFast(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, &OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{{
		ServiceTier: "all", Action: OpenAIFastPolicyActionForcePriority, Scope: BetaPolicyScopeAll,
	}}})
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"compute_residency": "EU"}}
	ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, ForceOpenAIFast: true})
	require.False(t, openAIGroupForcesFast(ctx, account, openai.GPT61SolModelID))
	for _, tier := range []string{"priority", "fast", "flex", "auto", "default"} {
		body := []byte(fmt.Sprintf(`{"model":"gpt-6.1-sol","service_tier":%q}`, tier))
		out, err := svc.applyOpenAIFastPolicyToBody(ctx, account, openai.GPT61SolModelID, body)
		require.NoError(t, err)
		if tier == "priority" || tier == "fast" {
			require.False(t, gjson.GetBytes(out, "service_tier").Exists())
		} else {
			require.Equal(t, tier, gjson.GetBytes(out, "service_tier").String())
		}
	}
}
