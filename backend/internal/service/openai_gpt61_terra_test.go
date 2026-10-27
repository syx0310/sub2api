//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestGPT61TerraCatalogAndRouting(t *testing.T) {
	const model = openai.GPT61TerraModelID
	body, err := BuildCodexModelsManifest([]string{model})
	require.NoError(t, err)
	entry := decodeCodexManifestModels(t, body)[0]
	for key, want := range map[string]any{
		"default_reasoning_level": "low", "multi_agent_reasoning_effort": "xhigh",
		"multi_agent_version": "v2", "tool_mode": "code_mode_only", "minimal_client_version": "0.153.0",
		"use_responses_lite": true, "prefer_websockets": true, "supports_reasoning_effort_updates": true,
		"supports_image_detail_original": true, "node_repl_auto_review_required": true,
	} {
		require.Equal(t, want, entry[key], key)
	}
	require.EqualValues(t, 500000, entry["context_window"])
	require.EqualValues(t, 872000, entry["max_context_window"])
	require.Equal(t, []any{"text", "image"}, entry["input_modalities"])
	require.Equal(t, `["low","medium","high","xhigh","max","ultra"]`, gjson.GetBytes(body, "models.0.supported_reasoning_levels.#.effort").Raw)
	require.JSONEq(t, gjson.GetBytes(openai.CodexGPT61TerraMetadata, "model_messages").Raw, gjson.GetBytes(body, "models.0.model_messages").Raw)
	require.Contains(t, openai.DefaultModelIDs(), model)
	require.True(t, configuredCodexSupportsPriorityServiceTier(model))
	require.False(t, openAIModelSupportsPublicUltrafast(model))
	for _, alias := range []string{model, "openai/gpt-6.1-terra", "GPT_6.1_TERRA"} {
		require.True(t, isOpenAIGPT6Model(alias), alias)
		require.Equal(t, model, normalizeKnownOpenAICodexModel(alias))
	}
	for _, invalid := range []string{"gpt-6-terra", "gpt-6.1", "gpt-6.1-terra-pro", "gpt-6.1-terra-codex", "gpt-6.1-terra-2026-10-26"} {
		require.False(t, isOpenAIGPT6Model(invalid))
		require.Nil(t, (&PricingService{}).GetModelPricing(invalid))
	}
	account := Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		ModelIDsSyncedAt: time.Now().UTC().Format(time.RFC3339), ModelIDs: []string{model},
	})
	require.Equal(t, []string{model}, openAIConfiguredAndObservedCodexModelIDsForGroup([]Account{account}, &Group{}))
	require.False(t, openAISubscriptionModelLongContextExempt(&account, model), "Terra does not inherit Sol's subscription exemption")
}

func TestGPT61TerraAPIKeyCatalogPreservesCapabilities(t *testing.T) {
	account := Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{"alias": openai.GPT61TerraModelID}}}
	body := convertOpenAIModelListToCodexManifestForAccount([]byte(`{"data":[{"id":"alias"}]}`), &account)
	converted, err := applySyncedAPIKeyCodexModelMetadata(body, &account, true)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(converted, "models.0.use_responses_lite").Bool())
	require.Equal(t, "xhigh", gjson.GetBytes(converted, "models.0.multi_agent_reasoning_effort").String())
	native := []byte(`{"models":[{"slug":"gpt-6.1-terra","context_window":300000,"supports_reasoning_effort_updates":false,"multi_agent_reasoning_effort":null,"future_capability":{"enabled":true}}]}`)
	preserved, err := applySyncedAPIKeyCodexModelMetadata(native, &account, false)
	require.NoError(t, err)
	require.EqualValues(t, 300000, gjson.GetBytes(preserved, "models.0.context_window").Int())
	require.False(t, gjson.GetBytes(preserved, "models.0.supports_reasoning_effort_updates").Bool())
	require.Equal(t, gjson.Null, gjson.GetBytes(preserved, "models.0.multi_agent_reasoning_effort").Type)
	require.True(t, gjson.GetBytes(preserved, "models.0.future_capability.enabled").Bool())
}

func TestGPT61TerraEUFastAndLunaCurrentContract(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, DefaultOpenAIFastPolicySettings())
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"compute_residency": "EU"}}
	group := &Group{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	for _, model := range []string{openai.GPT61TerraModelID, openai.GPT6LunaModelID} {
		require.False(t, openAIGroupForcesFast(ctx, account, model))
		out, err := svc.applyOpenAIFastPolicyToBody(ctx, account, model, []byte(fmt.Sprintf(`{"model":%q,"service_tier":"fast"}`, model)))
		require.NoError(t, err)
		require.Equal(t, "priority", gjson.GetBytes(out, "service_tier").String())
		group.ForceOpenAIFast = true
		require.True(t, openAIGroupForcesFast(ctx, account, model))
		group.ForceOpenAIFast = false
	}
}

func TestGPT61TerraCacheWriteTTLForward(t *testing.T) {
	for _, route := range []string{"apikey", "oauth", "passthrough", "thirdparty"} {
		for _, ttl := range []string{"30m", "12h", ""} {
			t.Run(route+"/"+ttl, func(t *testing.T) {
				s := newAstraOAuthSetup(t, route == "passthrough")
				if route == "apikey" || route == "thirdparty" {
					s.account.Type = AccountTypeAPIKey
					s.account.Credentials = map[string]any{"api_key": "test", "base_url": "https://api.openai.com/v1"}
					if route == "thirdparty" {
						s.account.Credentials["base_url"] = "https://compatible.example/v1"
					}
					s.account.Extra["openai_responses_supported"] = true
				}
				s.upstream.resp = &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(codexCompletedSSE(`{"id":"resp_ttl","model":"gpt-6.1-terra","output":[],"usage":{"input_tokens":100,"output_tokens":1}}`)))}
				body := []byte(fmt.Sprintf(`{"model":"gpt-6.1-terra","stream":false,"input":"hello","prompt_cache_options":{"ttl":%q}}`, ttl))
				result, err := s.svc.Forward(context.Background(), s.c, s.account, body)
				require.NoError(t, err)
				require.Equal(t, route == "apikey" && ttl == "12h", result.OpenAICacheWrite12h)
				require.Equal(t, route == "apikey", gjson.GetBytes(s.upstream.lastBody, "prompt_cache_options").Exists())
			})
		}
	}
	meta := newOpenAIWSPassthroughUsageMeta(openai.GPT61TerraModelID, nil)
	meta.initFromFirstFrame([]byte(`{"model":"gpt-6.1-terra","prompt_cache_options":{"ttl":"12h"}}`), openai.GPT61TerraModelID)
	require.Equal(t, "12h", optionalStringValue(meta.cacheWriteTTL.Load()))
	meta.updateFromResponseCreate([]byte(`{"model":"gpt-6.1-terra"}`), openai.GPT61TerraModelID, openai.GPT61TerraModelID)
	require.Empty(t, optionalStringValue(meta.cacheWriteTTL.Load()), "a new create must not inherit the previous TTL")
}

func TestGPT61TerraPricingAndTTL(t *testing.T) {
	const model = openai.GPT61TerraModelID
	svc := NewBillingService(&config.Config{}, nil)
	price, err := svc.GetModelPricing(model)
	require.NoError(t, err)
	require.InDelta(t, 1e-6, price.InputPricePerToken, 1e-15)
	require.InDelta(t, 0.05e-6, price.CacheReadPricePerToken, 1e-15)
	require.InDelta(t, 5e-6, price.OutputPricePerToken, 1e-15)
	for _, total := range []int{272000, 272001} {
		for _, tier := range []string{"default", "priority", "flex"} {
			for _, twelve := range []bool{false, true} {
				tokens := UsageTokens{InputTokens: total - 200000, CacheCreationTokens: 100000, CacheReadTokens: 100000, OutputTokens: 1000, OpenAICacheWrite12h: twelve}
				cost, err := svc.CalculateCostWithServiceTier(model, tokens, 1, tier)
				require.NoError(t, err)
				inScale, outScale, writeScale := 1.0, 1.0, 1.0
				if total > 272000 {
					inScale, outScale = 2, 1.5
				}
				if twelve {
					writeScale = 2
				}
				tierScale := map[string]float64{"default": 1, "priority": 2, "flex": 0.5}[tier]
				require.InDelta(t, 100000*1.25e-6*inScale*writeScale*tierScale, cost.CacheCreationCost, 1e-12)
				require.InDelta(t, 100000*0.05e-6*inScale*tierScale, cost.CacheReadCost, 1e-12)
				require.InDelta(t, 1000*5e-6*outScale*tierScale, cost.OutputCost, 1e-12)
				require.Equal(t, total > 272000, cost.LongContextBillingApplied)
			}
		}
	}
	for _, custom := range []float64{0, 7e-6} {
		p, err := svc.GetModelPricingWithChannel(model, &ChannelModelPricing{CacheWritePrice: &custom})
		require.NoError(t, err)
		cost := svc.computeTokenBreakdown(p, UsageTokens{CacheCreationTokens: 100, OpenAICacheWrite12h: true}, 1, "default", false)
		require.InDelta(t, 100*custom, cost.CacheCreationCost, 1e-12)
	}
	custom := &LiteLLMModelPricing{InputCostPerToken: 123e-6, CacheCreationInputTokenCostExplicit: true, CacheCreationInputTokenCost: 0}
	prices := &PricingService{pricingData: map[string]*LiteLLMModelPricing{model: custom}}
	require.Same(t, custom, prices.GetModelPricing(model))
	dynamic := NewBillingService(&config.Config{}, prices)
	zeroCost, err := dynamic.CalculateCostWithServiceTier(model, UsageTokens{CacheCreationTokens: 100, OpenAICacheWrite12h: true}, 1, "priority")
	require.NoError(t, err)
	require.Zero(t, zeroCost.CacheCreationCost)
	var catalog map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(openai.CodexGPT61TerraMetadata, &catalog))
	require.Contains(t, catalog, "model_messages")
}

func TestGPT61TerraChatTTLUsesFinalRequest(t *testing.T) {
	for _, raw := range []bool{false, true} {
		t.Run(fmt.Sprintf("raw=%t", raw), func(t *testing.T) {
			response := codexCompletedSSE(`{"id":"resp_ttl","model":"gpt-6.1-terra","output":[],"usage":{"input_tokens":100,"output_tokens":1}}`)
			contentType := "text/event-stream"
			if raw {
				response = `{"id":"chatcmpl_ttl","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":1}}`
				contentType = "application/json"
			}
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response))}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			account := &Account{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "base_url": "https://api.openai.com/v1", "model_mapping": map[string]any{"alias": openai.GPT61TerraModelID}}, Extra: map[string]any{"openai_responses_supported": !raw}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, []byte(`{"model":"alias","stream":false,"messages":[{"role":"user","content":"hello"}],"prompt_cache_options":{"ttl":"12h"}}`), "", "")
			require.NoError(t, err)
			require.Equal(t, "12h", gjson.GetBytes(upstream.lastBody, "prompt_cache_options.ttl").String())
			require.True(t, result.OpenAICacheWrite12h)
		})
	}
}

func TestGPT61TerraRegionalAPIContracts(t *testing.T) {
	for _, host := range []string{"api.openai.com", "us.api.openai.com", "eu.api.openai.com", "eu.api.openai.com.example", "compatible.example"} {
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://" + host + "/v1"}}
		official := host == "api.openai.com" || host == "us.api.openai.com" || host == "eu.api.openai.com"
		require.Equal(t, official, openAIUses12hCacheWriteBody(account, openai.GPT61TerraModelID, []byte(`{"prompt_cache_options":{"ttl":"12h"}}`)))
		_, err := prepareGPT6ChatUpstreamBody(account, openai.GPT61TerraModelID, []byte(`{"tools":[{"type":"function","function":{"name":"echo"}}]}`))
		require.Equal(t, official, err != nil, "Responses-only tool contract must include regional APIs: %s", host)
	}
}

func TestGPT61TerraBackgroundCompactionClientReplay(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, lite := range []bool{false, true} {
			t.Run(fmt.Sprintf("passthrough=%t/lite=%t", passthrough, lite), func(t *testing.T) {
				s := newAstraOAuthSetup(t, passthrough)
				SetOpenAIClientTransport(s.c, OpenAIClientTransportHTTP)
				if lite {
					s.c.Request.Header.Set(responsesLiteHeader, "true")
				}
				body := oauthWebSearchCompactionClientBody(t, "native", lite)
				var err error
				for path, value := range map[string]any{
					"model":   openai.GPT61TerraModelID,
					"include": []string{"reasoning.encrypted_content", "output.encrypted_content"},
					"stream_options.reasoning_summary_delivery":     "sequential_cutoff",
					"client_metadata." + openAIWSTurnMetadataHeader: `{"request_kind":"compaction","compaction_phase":"background","thread_id":"child","future":{"keep":true}}`,
				} {
					body, err = sjson.SetBytes(body, path, value)
					require.NoError(t, err)
				}
				s.upstream.resp = &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(codexCompletedSSE(`{"id":"resp_terra_compact","model":"gpt-6.1-terra","output":[{"type":"compaction","encrypted_content":"new-summary"}],"usage":{"input_tokens":1,"output_tokens":1}}`)))}
				_, err = s.svc.Forward(context.Background(), s.c, s.account, body)
				require.NoError(t, err)
				require.Len(t, s.upstream.requests, 1)
				forwarded := s.upstream.lastBody
				assertOAuthWebSearchCompactionForward(t, body, forwarded, lite, true)
				require.Equal(t, openai.GPT61TerraModelID, gjson.GetBytes(forwarded, "model").String())
				require.Equal(t, "sequential_cutoff", gjson.GetBytes(forwarded, "stream_options.reasoning_summary_delivery").String())
				require.Contains(t, gjson.GetBytes(forwarded, "include").Raw, "output.encrypted_content")
				require.True(t, gjson.Get(gjson.GetBytes(forwarded, "client_metadata."+openAIWSTurnMetadataHeader).String(), "future.keep").Bool())
				require.False(t, gjson.GetBytes(forwarded, "previous_response_id").Exists())
			})
		}
	}
}
