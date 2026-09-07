package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIGatewayForwardNormalizesOfficialGPT6AstraRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
	}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
	account := &Account{
		ID:       6,
		Name:     "official-openai",
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://api.openai.com/v1",
		},
		Extra: map[string]any{"openai_responses_supported": true},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	body := []byte(`{
		"model":"gpt-6-astra","stream":false,
		"temperature":0.2,"top_p":0.9,"top_logprobs":5,
		"include":["reasoning.encrypted_content","message.output_text.logprobs"],
		"reasoning":{"effort":"none"},
		"prompt_cache_options":{"ttl":"30m"},
		"truncation":"auto",
		"context_management":[{"type":"compaction","compact_threshold":200000}],
		"input":[
			{"type":"configuration_update","reasoning":{"effort":"high"}},
			{"role":"user","content":"analyze"}
		]
	}`)

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "low", *result.ReasoningEffort)
	out := upstream.lastBody
	require.Equal(t, "gpt-6-astra", gjson.GetBytes(out, "model").String())
	require.False(t, gjson.GetBytes(out, "temperature").Exists())
	require.False(t, gjson.GetBytes(out, "top_p").Exists())
	require.False(t, gjson.GetBytes(out, "top_logprobs").Exists())
	require.Equal(t, "low", gjson.GetBytes(out, "reasoning.effort").String())
	require.Equal(t, "30m", gjson.GetBytes(out, "prompt_cache_options.ttl").String())
	require.False(t, gjson.GetBytes(out, "truncation").Exists())
	require.False(t, gjson.GetBytes(out, "context_management").Exists())
	require.Equal(t, "reasoning.encrypted_content", gjson.GetBytes(out, "include.0").String())
	require.Len(t, gjson.GetBytes(out, "include").Array(), 1)
	require.Equal(t, "configuration_update", gjson.GetBytes(out, "input.0.type").String())
	require.Equal(t, "high", gjson.GetBytes(out, "input.0.reasoning.effort").String())
}

func TestOpenAIGatewayForwardStripsGPT6AstraPromptCacheOptionsForCompatibleProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
	}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
	account := &Account{
		ID:       7,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://compatible.example/v1",
		},
		Extra: map[string]any{"openai_responses_supported": true},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	_, err := svc.Forward(context.Background(), c, account, []byte(
		`{"model":"gpt-6-astra","stream":false,"prompt_cache_options":{"ttl":"30m"},"input":"hello"}`,
	))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_options").Exists())
}

func TestAstraCatalogKeepsCodexDefaultsWhenAPIMetadataIsSynced(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"base_url":      "https://api.openai.com/v1",
		"model_mapping": map[string]any{"my-astra": "gpt-6-astra"},
	}}
	reasoning := true
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{
		"gpt-6-astra": {
			Reasoning: &reasoning, DefaultReasoningLevel: "medium",
			SupportedReasoningLevels: []string{"low", "medium", "high", "xhigh", "max"},
			ContextWindow:            1_050_000, InputModalities: []string{"text", "image"},
		},
	}})
	for _, slug := range []string{"gpt-6-astra", "my-astra"} {
		source, err := json.Marshal(map[string]any{"data": []any{map[string]any{"id": slug}}})
		require.NoError(t, err)
		converted := convertOpenAIModelListToCodexManifestForAccount(source, account)
		body, err := applySyncedAPIKeyCodexModelMetadata(converted, account, true)
		require.NoError(t, err)
		model := decodeCodexManifestModels(t, body)[0]
		require.Equal(t, slug, model["slug"])
		require.EqualValues(t, 272_000, model["context_window"])
		require.EqualValues(t, 872_000, model["max_context_window"])
		require.Equal(t, "low", model["default_reasoning_level"])
		require.Equal(t, "xhigh", model["multi_agent_reasoning_effort"])
		require.Equal(t, true, model["supports_experimental_context"])
		require.Equal(t, "code_mode_only", model["tool_mode"])
		require.Equal(t, false, model["use_responses_lite"])
		require.Len(t, model["supported_reasoning_levels"], 6)
	}

	// A native Codex manifest owns its existing client-specific fields.
	native := []byte(`{"models":[{"slug":"gpt-6-astra","context_window":300000,"max_context_window":900000,"default_reasoning_level":"high","supported_reasoning_levels":[{"effort":"high"}]}]}`)
	preserved, err := applySyncedAPIKeyCodexModelMetadata(native, account, false)
	require.NoError(t, err)
	model := decodeCodexManifestModels(t, preserved)[0]
	require.EqualValues(t, 300_000, model["context_window"])
	require.EqualValues(t, 900_000, model["max_context_window"])
	require.Equal(t, "high", model["default_reasoning_level"])
}

func TestAstraRoutingIgnoresLegacyCapabilityOnlySnapshots(t *testing.T) {
	now := time.Now().UTC()
	for _, raw := range []any{
		UpstreamModelMetadataSnapshot{SyncedAt: now.Format(time.RFC3339), ModelIDs: []string{"gpt-5.6-sol"}},
		map[string]any{"synced_at": now.Format(time.RFC3339), "models": map[string]any{"gpt-5.6-sol": map[string]any{}}},
	} {
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{UpstreamModelMetadataExtraKey: raw}}
		_, known := account.UpstreamModelCatalogSupports("gpt-6-astra", now)
		require.False(t, known)
		require.True(t, account.IsModelSupported("gpt-6-astra"))
	}
}
