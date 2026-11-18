//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPT62CatalogAndExactModels(t *testing.T) {
	for model, raw := range map[string][]byte{openai.GPT62SolModelID: openai.CodexGPT62SolMetadata, openai.GPT62AstraModelID: openai.CodexGPT62AstraMetadata} {
		t.Run(model, func(t *testing.T) {
			body, err := BuildCodexModelsManifest([]string{model})
			require.NoError(t, err)
			for _, field := range []string{"context_window", "max_context_window", "minimal_client_version", "supported_reasoning_levels", "default_reasoning_level", "multi_agent_reasoning_effort", "multi_agent_version", "supports_reasoning_effort_updates", "use_responses_lite", "tool_mode", "prefer_websockets", "model_messages"} {
				require.JSONEq(t, gjson.GetBytes(raw, field).Raw, gjson.GetBytes(body, "models.0."+field).Raw, field)
			}
			require.Contains(t, openai.DefaultModelIDs(), model)
			for _, alias := range []string{model, "openai/" + model, strings.ToUpper(strings.ReplaceAll(model, "-", "_")), model + "-high", model + "-openai-compact"} {
				require.Equal(t, model, normalizeKnownOpenAICodexModel(alias))
				require.NotNil(t, (&PricingService{}).GetModelPricing(alias), alias)
			}
			account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.openai.com/v1"}}
			converted, err := completeAPIKeyCodexModelsManifestMetadata([]byte(fmt.Sprintf(`{"models":[{"slug":%q}]}`, model)), true, account)
			require.NoError(t, err)
			require.False(t, gjson.GetBytes(converted, "models.0.use_responses_lite").Bool())
			live := []byte(fmt.Sprintf(`{"models":[{"slug":%q,"supports_reasoning_effort_updates":false,"multi_agent_reasoning_effort":null,"service_tiers":[],"future_field":true,"context_window":300000}]}`, model))
			converted, err = applySyncedAPIKeyCodexModelMetadata(live, account, false)
			require.NoError(t, err)
			require.False(t, gjson.GetBytes(converted, "models.0.supports_reasoning_effort_updates").Bool())
			require.Equal(t, "null", gjson.GetBytes(converted, "models.0.multi_agent_reasoning_effort").Raw)
			require.Equal(t, "[]", gjson.GetBytes(converted, "models.0.service_tiers").Raw)
			require.True(t, gjson.GetBytes(converted, "models.0.future_field").Bool())
			require.EqualValues(t, 300000, gjson.GetBytes(converted, "models.0.context_window").Int())
			account.Type = AccountTypeOAuth
			account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{ModelIDsSyncedAt: time.Now().UTC().Format(time.RFC3339), ModelIDs: []string{model}})
			require.Equal(t, []string{model}, openAIConfiguredAndObservedCodexModelIDsForGroup([]Account{*account}, &Group{}))
		})
	}
	for _, unknown := range []string{"gpt-6.2", "gpt-6.2-terra", "gpt-6.2-luna", "gpt-6.2-sol-pro", "gpt-6.2-astra-2026-11-17"} {
		require.False(t, isOpenAIGPT6Model(unknown))
		require.Empty(t, normalizeKnownOpenAICodexModel(unknown))
		require.Nil(t, (&PricingService{}).GetModelPricing(unknown))
	}
}

func TestGPT62PricingFrozenTimeAndCacheBuckets(t *testing.T) {
	bs := NewBillingService(&config.Config{}, nil)
	for model, inputRate := range map[string]float64{"gpt-6.2-sol": 2e-6, "gpt-6.2-astra": 8e-6} {
		for _, resolver := range []*ModelPricingResolver{nil, NewModelPricingResolver(nil, bs)} {
			for _, total := range []int{272000, 272001} {
				for _, at := range []time.Time{openAIGPT62UltrafastPromotionEnd.Add(-time.Nanosecond), openAIGPT62UltrafastPromotionEnd} {
					for _, tier := range []string{"default", "priority", "ultrafast"} {
						t.Run(fmt.Sprintf("%s/resolver=%t/%d/%s/%s", model, resolver != nil, total, at.Format(time.RFC3339Nano), tier), func(t *testing.T) {
							tokens := UsageTokens{InputTokens: total - 200000, OutputTokens: 1000, CacheReadTokens: 100000, CacheCreationTokens: 100000, CacheCreation30mTokens: 40000, CacheCreation12hTokens: 60000}
							price, err := bs.getModelPricingAt(model, at)
							require.NoError(t, err)
							cost, err := bs.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: model, Tokens: tokens, RateMultiplier: 1, PricingAt: at, ServiceTier: tier, Resolver: resolver, Resolved: &ResolvedPricing{Mode: BillingModeToken, Source: PricingSourceLiteLLM, BasePricing: price, longContextPricingEnabled: true}})
							require.NoError(t, err)
							scale := map[string]float64{"default": 1, "priority": 2, "ultrafast": 3}[tier]
							if tier == "ultrafast" && !at.Before(openAIGPT62UltrafastPromotionEnd) {
								scale = 6
							}
							inScale, outScale := 1.0, 1.0
							if total > 272000 {
								inScale, outScale = 2, 1.5
							}
							require.InDelta(t, float64(tokens.InputTokens)*inputRate*inScale*scale, cost.InputCost, 1e-12)
							require.InDelta(t, 1000*inputRate*5*outScale*scale, cost.OutputCost, 1e-12)
							require.InDelta(t, 100000*inputRate*.05*inScale*scale, cost.CacheReadCost, 1e-12)
							require.InDelta(t, (40000*inputRate*1.25+60000*inputRate*2.5)*inScale*scale, cost.CacheCreationCost, 1e-12)
						})
					}
				}
			}
		}
		for _, custom := range []float64{0, 7e-6} {
			p, err := bs.GetModelPricingWithChannel(model, &ChannelModelPricing{CacheWritePrice: &custom})
			require.NoError(t, err)
			cost := bs.computeTokenBreakdown(p, UsageTokens{CacheCreationTokens: 100, CacheCreation12hTokens: 100}, 1, "default", false)
			require.InDelta(t, 100*custom, cost.CacheCreationCost, 1e-12, "custom flat/free prices remain authoritative")
		}
	}
	for _, old := range []string{"gpt-6-astra", "gpt-6.1-sol"} {
		require.Equal(t, 6.0, openAIUltrafastMultiplierAt(old, openAIGPT62UltrafastPromotionEnd.Add(-time.Hour)))
	}
}

func TestGPT62ForcedTierAccountCapabilities(t *testing.T) {
	group := &Group{ID: 7, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, ForceOpenAIUltrafast: true}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, tc := range []struct {
			typ, base, tiers string
			want             string
		}{
			{AccountTypeOAuth, "https://chatgpt.com", "", ""},
			{AccountTypeOAuth, "https://chatgpt.com", `[{"id":"ultrafast","name":"Ultrafast"}]`, "ultrafast"},
			{AccountTypeAPIKey, "https://api.openai.com/v1", "", "ultrafast"},
			{AccountTypeAPIKey, "https://us.api.openai.com/v1", "", "ultrafast"},
			{AccountTypeAPIKey, "https://eu.api.openai.com/v1", "", ""},
			{AccountTypeAPIKey, "https://api.openai.com/v1", `[]`, ""},
			{AccountTypeAPIKey, "https://api.openai.com/v1", `null`, ""},
			{AccountTypeAPIKey, "https://third.example/v1", "", ""},
			{AccountTypeAPIKey, "https://third.example/v1", `[{"id":"ultrafast","name":"Ultrafast"}]`, "ultrafast"},
		} {
			account := &Account{Platform: PlatformOpenAI, Type: tc.typ, Credentials: map[string]any{"base_url": tc.base, "plan_type": "promax"}}
			if tc.tiers != "" {
				account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{model: {CodexToolCapabilities: map[string]json.RawMessage{"service_tiers": json.RawMessage(tc.tiers)}}}})
			}
			require.Equal(t, tc.want, openAIGroupForcedServiceTier(ctx, account, model), "%s %s %s", model, tc.base, tc.tiers)
		}
	}
}

func TestGPT62ConfigurationAndPrefixHTTPForward(t *testing.T) {
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, route := range []string{"oauth", "passthrough", "apikey", "thirdparty"} {
			t.Run(model+"/"+route, func(t *testing.T) {
				s := newAstraOAuthSetup(t, route == "passthrough")
				if route == "apikey" || route == "thirdparty" {
					s.account.Type = AccountTypeAPIKey
					base := "https://api.openai.com/v1"
					if route == "thirdparty" {
						base = "https://third.example/v1"
					}
					s.account.Credentials = map[string]any{"base_url": base, "api_key": "test"}
					s.account.Extra["openai_responses_supported"] = true
				}
				response := fmt.Sprintf(`{"id":"resp_62","model":%q,"reasoning":{"effort":"low"},"output":[],"input_transformations":[{"type":"reasoning_removed","reason":"prefix_mismatch"}],"usage":{"input_tokens":1,"output_tokens":1}}`, model)
				s.upstream.resp = &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(codexCompletedSSE(response)))}
				body := []byte(fmt.Sprintf(`{"model":%q,"instructions":"client-owned","reasoning":{"effort":"low"},"stream":false,"safety_identifier":"end-user-stable","context_management":[{"type":"compaction","compact_threshold":200000}],"input":[{"type":"additional_tools","id":"at_00112233-4455-4677-8899-aabbccddeeff","tools":[]},{"type":"configuration_update","reasoning":{"effort":"high"}},{"role":"user","content":"analyze"},{"type":"configuration_update","text":{"format":{"type":"text"}}},{"type":"function_call_output","namespace":"agent","name":"browser_info","output":"client context"}]}`, model))
				result, err := s.svc.Forward(context.Background(), s.c, s.account, body)
				require.NoError(t, err)
				require.NotNil(t, result.ReasoningEffort)
				require.Equal(t, "high", *result.ReasoningEffort, "response baseline must not override the active control item")
				require.Equal(t, "low", gjson.GetBytes(s.upstream.lastBody, "reasoning.effort").String())
				require.JSONEq(t, gjson.GetBytes(body, "input").Raw, gjson.GetBytes(s.upstream.lastBody, "input").Raw)
				require.JSONEq(t, gjson.GetBytes(body, "context_management").Raw, gjson.GetBytes(s.upstream.lastBody, "context_management").Raw)
				require.Contains(t, s.rec.Body.String(), "input_transformations")
			})
		}
	}
}

func TestConfigurationFormatOnlyInheritance(t *testing.T) {
	for _, tc := range []struct {
		input, effort string
		present       bool
	}{
		{`[{"type":"configuration_update","reasoning":{"effort":"high"}},{"type":"configuration_update","text":{"format":{"type":"text"}}}]`, "high", true},
		{`[{"type":"configuration_update","text":{"format":{"type":"text"}}}]`, "", false},
		{`[{"type":"configuration_update","reasoning":{"effort":"high"}},{"type":"configuration_update","reasoning":{"effort":null}}]`, "", true},
		{`[{"type":"configuration_update","reasoning":{"effort":"high"}},{"type":"compaction","encrypted_content":"sealed"},{"type":"configuration_update","text":{"format":{"type":"text"}}}]`, "", false},
		{`[{"type":"compaction","encrypted_content":"sealed"},{"type":"configuration_update","reasoning":{"effort":"max"},"text":{"format":{"type":"text"}}}]`, "max", true},
	} {
		body := []byte(`{"input":` + tc.input + `}`)
		_, effort, present := lastOpenAIConfigurationEffort(body)
		require.Equal(t, tc.effort, effort)
		require.Equal(t, tc.present, present)
		var input any
		require.NoError(t, json.Unmarshal([]byte(tc.input), &input))
		mapEffort, mapPresent := lastOpenAIConfigurationEffortMap(input)
		require.Equal(t, effort, mapEffort)
		require.Equal(t, present, mapPresent)
	}
}
