package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPT6SolLunaCatalogAndAPIKeyOverrides(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		t.Run(model, func(t *testing.T) {
			body, err := BuildCodexModelsManifest([]string{model})
			require.NoError(t, err)
			entry := decodeCodexManifestModels(t, body)[0]
			require.Equal(t, "medium", entry["default_reasoning_level"])
			require.EqualValues(t, 272000, entry["context_window"])
			require.EqualValues(t, 872000, entry["max_context_window"])
			require.Equal(t, true, entry["supports_reasoning_effort_updates"])
			require.Equal(t, true, entry["use_responses_lite"])
			require.Nil(t, entry["default_service_tier"])
			require.Nil(t, entry["multi_agent_reasoning_effort"])
			require.Equal(t, model == "gpt-6-sol", entry["node_repl_auto_review_required"])
			require.Equal(t, "0.155.0", entry["minimal_client_version"])
			require.Equal(t, []any{"text", "image"}, entry["input_modalities"])
			require.Equal(t, map[string]any{"cyber": []any{"standard"}}, entry["available_access_programs"])
			efforts := gjson.GetBytes(body, "models.0.supported_reasoning_levels.#.effort").Array()
			want := []string{"low", "medium", "high", "xhigh", "max"}
			if model == "gpt-6-sol" {
				want = append(want, "ultra")
			}
			var got []string
			for _, effort := range efforts {
				got = append(got, effort.String())
			}
			require.Equal(t, want, got)

			account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
				"base_url": "https://api.openai.com/v1", "model_mapping": map[string]any{"alias": model},
			}}
			reasoning := true
			account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{
				model: {Reasoning: &reasoning, DefaultReasoningLevel: "none", SupportedReasoningLevels: []string{"none", "low"}, ContextWindow: 1050000},
			}})
			converted := convertOpenAIModelListToCodexManifestForAccount([]byte(`{"data":[{"id":"alias"}]}`), account)
			converted, err = applySyncedAPIKeyCodexModelMetadata(converted, account, true)
			require.NoError(t, err)
			entry = decodeCodexManifestModels(t, converted)[0]
			require.Equal(t, "alias", entry["slug"])
			require.Equal(t, "medium", entry["default_reasoning_level"])
			require.EqualValues(t, 272000, entry["context_window"])
			require.EqualValues(t, 872000, entry["max_context_window"])
			require.Equal(t, false, entry["use_responses_lite"])
			require.Equal(t, true, entry["supports_reasoning_effort_updates"])

			// An upstream native catalog owns explicit false/null declarations.
			native := []byte(fmt.Sprintf(`{"models":[{"slug":%q,"supports_reasoning_effort_updates":false,"default_service_tier":null,"context_window":300000,"max_context_window":900000}]}`, model))
			preserved, err := applySyncedAPIKeyCodexModelMetadata(native, account, false)
			require.NoError(t, err)
			require.False(t, gjson.GetBytes(preserved, "models.0.supports_reasoning_effort_updates").Bool())
			require.EqualValues(t, 300000, gjson.GetBytes(preserved, "models.0.context_window").Int())
			require.Equal(t, gjson.Null, gjson.GetBytes(preserved, "models.0.default_service_tier").Type)
		})
	}
}

func TestGPT6SolLunaFreshCatalogAndExactRecognition(t *testing.T) {
	account := Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	now := time.Now().UTC()
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		ModelIDsSyncedAt: now.Format(time.RFC3339), ModelIDs: []string{"gpt-6-sol", "gpt-6-luna"},
	})
	require.Equal(t, []string{"gpt-6-luna", "gpt-6-sol"}, openAIConfiguredAndObservedCodexModelIDsForGroup([]Account{account}, &Group{}))
	require.Equal(t, []string{"gpt-6-luna"}, openAIConfiguredAndObservedCodexModelIDsForGroup([]Account{account}, &Group{
		ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"gpt-6-luna"}},
	}))
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		require.True(t, account.IsModelSupported("openai/"+model))
		require.Equal(t, model, normalizeKnownOpenAICodexModel("openai/"+model))
		require.Contains(t, openai.DefaultModelIDs(), model)
		require.True(t, configuredCodexSupportsPriorityServiceTier(model))
		require.False(t, configuredCodexSupportsUltrafastServiceTier(model))
	}
	for _, model := range []string{"gpt-6", "gpt-6-sol-pro", "gpt-6-sol-codex", "gpt-6-luna-2026-09-22"} {
		require.False(t, isOpenAIGPT6Model(model))
		require.Empty(t, normalizeKnownOpenAICodexModel(model))
		require.Nil(t, (&PricingService{}).GetModelPricing(model))
	}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		ModelIDsSyncedAt: now.Format(time.RFC3339), ModelIDs: []string{"gpt-6-astra"},
	})
	require.False(t, account.IsModelSupported("gpt-6-sol"))
	require.False(t, account.IsModelSupported("gpt-6-luna"))
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		ModelIDsSyncedAt: now.Add(-upstreamModelCatalogRoutingTTL - time.Minute).Format(time.RFC3339), ModelIDs: []string{"gpt-6-astra"},
	})
	require.True(t, account.IsModelSupported("gpt-6-sol"), "stale snapshots are not access evidence")
}

func TestGPT6SolLunaNormalizationAndPro(t *testing.T) {
	for _, model := range []string{"gpt-6-astra", "gpt-6-sol", "gpt-6-luna"} {
		for _, effort := range []string{"none", "minimal", "max", "ultra"} {
			t.Run(model+"/"+effort, func(t *testing.T) {
				body := []byte(fmt.Sprintf(`{"model":%q,"instructions":"client-owned","temperature":0.2,"top_p":0.9,"reasoning":{"mode":"pro","effort":%q},"input":[{"type":"configuration_update","reasoning":{"effort":"none"}},{"type":"function_call_output","call_id":"ctc_client","output":"ok"}]}`, model, effort))
				out, _, err := normalizeOpenAIResponsesWebSocketCompatibilityBody(body, &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, false)
				require.NoError(t, err)
				want := map[string]string{"none": "low", "minimal": "low", "max": "max", "ultra": "max"}[effort]
				if model == "gpt-6-astra" && effort == "ultra" {
					want = "xhigh"
				}
				require.Equal(t, want, gjson.GetBytes(out, "reasoning.effort").String())
				require.Equal(t, "low", gjson.GetBytes(out, "input.0.reasoning.effort").String())
				require.Equal(t, "pro", gjson.GetBytes(out, "reasoning.mode").String())
				require.Equal(t, "client-owned", gjson.GetBytes(out, "instructions").String())
				require.False(t, gjson.GetBytes(out, "temperature").Exists())
				require.False(t, gjson.GetBytes(out, "top_p").Exists())
				require.False(t, gjson.GetBytes(out, "multi_agent").Exists())
				again, changed, err := normalizeOpenAIResponsesWebSocketCompatibilityBody(out, &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, false)
				require.NoError(t, err)
				require.False(t, changed)
				require.Equal(t, out, again)
			})
		}
		body := []byte(fmt.Sprintf(`{"model":%q,"input":[]}`, model))
		out, changed, err := normalizeGPT6RequestBody(body, false)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, body, out, "do not inject effort or instructions")
		request := map[string]any{"model": model}
		applyCodexOAuthTransform(request, true, false)
		require.Equal(t, "", request["instructions"], "keep the existing empty Codex placeholder, not a new prompt")
	}
}

func TestGPT6SolLunaPricingAndLongContext(t *testing.T) {
	catalog, err := os.ReadFile("../../resources/model-pricing/model_prices_and_context_window.json")
	require.NoError(t, err)
	for _, source := range []struct {
		name    string
		pricing *PricingService
	}{
		{"embedded", newStubPricingServiceFromJSON(t, string(catalog))}, {"pricing_fallback", &PricingService{}}, {"billing_fallback", nil},
	} {
		for _, model := range []struct {
			id            string
			input, output float64
		}{{"gpt-6-sol", 2e-6, 10e-6}, {"gpt-6-luna", .1e-6, .5e-6}} {
			t.Run(source.name+"/"+model.id, func(t *testing.T) {
				svc := NewBillingService(&config.Config{}, source.pricing)
				for _, extra := range []int{0, 1} {
					for _, tier := range []struct {
						name  string
						scale float64
					}{{"", 1}, {"priority", 2}, {"flex", .5}} {
						tokens := UsageTokens{InputTokens: 100000 + extra, CacheCreationTokens: 100000, CacheReadTokens: 72000, OutputTokens: 10}
						cost, err := svc.CalculateCostWithServiceTier(model.id, tokens, 1, tier.name)
						require.NoError(t, err)
						require.Equal(t, extra == 1, cost.LongContextBillingApplied)
						inScale, outScale := tier.scale, tier.scale
						if extra == 1 {
							inScale *= 2
							outScale *= 1.5
						}
						require.InDelta(t, float64(tokens.InputTokens)*model.input*inScale, cost.InputCost, 1e-10)
						require.InDelta(t, 100000*model.input*1.25*inScale, cost.CacheCreationCost, 1e-10)
						require.InDelta(t, 72000*model.input*.1*inScale, cost.CacheReadCost, 1e-10)
						require.InDelta(t, 10*model.output*outScale, cost.OutputCost, 1e-10)
					}
				}
			})
		}
	}
	custom := &LiteLLMModelPricing{InputCostPerToken: 123e-6, OutputCostPerToken: 456e-6}
	pricing := &PricingService{pricingData: map[string]*LiteLLMModelPricing{"gpt-6-sol": custom}}
	require.Same(t, custom, pricing.GetModelPricing("gpt-6-sol"), "explicit rates take precedence over fallback")
	missingModels := &PricingService{pricingData: map[string]*LiteLLMModelPricing{"gpt-6": custom}}
	require.InDelta(t, 2e-6, missingModels.GetModelPricing("gpt-6-sol").InputCostPerToken, 1e-12)
	require.InDelta(t, .1e-6, missingModels.GetModelPricing("gpt-6-luna").InputCostPerToken, 1e-12)
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		svc := NewBillingService(&config.Config{}, nil)
		inputPrice, outputPrice, cacheWritePrice, cacheReadPrice := 123e-6, 456e-6, 0.0, 7e-6
		price, err := svc.GetModelPricingWithChannel(model, &ChannelModelPricing{
			InputPrice: &inputPrice, OutputPrice: &outputPrice,
			CacheWritePrice: &cacheWritePrice, CacheReadPrice: &cacheReadPrice,
		})
		require.NoError(t, err)
		require.InDelta(t, 123e-6, price.InputPricePerToken, 1e-12)
		require.InDelta(t, 456e-6, price.OutputPricePerToken, 1e-12)
		require.Zero(t, price.CacheCreationPricePerToken, "explicit zero cache-write price must not become 1.25x")
		require.InDelta(t, 7e-6, price.CacheReadPricePerToken, 1e-12)
	}
}

func TestGPT6SolLunaEUDoesNotForceFast(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, &OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{{
		ServiceTier: "all", Action: OpenAIFastPolicyActionForcePriority, Scope: BetaPolicyScopeAll,
	}}})
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"compute_residency": "EU"}}
	ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, ForceOpenAIFast: true})
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		require.False(t, openAIGroupForcesFast(ctx, account, model))
		for _, tier := range []string{"priority", "fast", "flex", "ultrafast", "auto", "default"} {
			out, err := svc.applyOpenAIFastPolicyToBody(ctx, account, model, []byte(fmt.Sprintf(`{"model":%q,"service_tier":%q}`, model, tier)))
			require.NoError(t, err)
			require.False(t, gjson.GetBytes(out, "service_tier").Exists(), "%s/%s", model, tier)
		}
	}
}

func TestGPT6SolLunaCapabilityFalseSurvivesSync(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{
		"gpt-6-sol": {CodexToolCapabilities: map[string]json.RawMessage{"supports_reasoning_effort_updates": json.RawMessage("false")}},
	}})
	require.JSONEq(t, "false", string(accountCodexToolCapabilities(account, "gpt-6-sol")["supports_reasoning_effort_updates"]))
	merged := intersectUpstreamModelMetadata("alias", []UpstreamModelMetadata{
		{CodexToolCapabilities: map[string]json.RawMessage{"supports_reasoning_effort_updates": json.RawMessage("true")}},
		{CodexToolCapabilities: map[string]json.RawMessage{"supports_reasoning_effort_updates": json.RawMessage("false")}},
	})
	require.JSONEq(t, "false", string(merged.CodexToolCapabilities["supports_reasoning_effort_updates"]))
}

func TestGPT6SolLunaInheritedUltraPolicy(t *testing.T) {
	for _, model := range []string{"gpt-6-astra", "gpt-6-sol", "gpt-6-luna"} {
		value, err := applyOpenAIConfigurationEffortValue("ultra", model, "", nil, "")
		require.NoError(t, err)
		require.Equal(t, "ultra", value, "no policy must not introduce a new inherited update")
		value, err = applyOpenAIConfigurationEffortValue("ultra", model, "max", nil, "")
		require.NoError(t, err)
		want := "max"
		if model == "gpt-6-astra" {
			want = "xhigh"
		}
		require.Equal(t, want, value)
		value, err = applyOpenAIConfigurationEffortValue("ultra", model, "high", nil, "")
		require.NoError(t, err)
		require.Equal(t, "high", value, "explicit admin caps still apply")
	}
}
