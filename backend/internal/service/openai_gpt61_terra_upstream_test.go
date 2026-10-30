//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Retain the upstream discovery/capability regressions alongside the fork's
// client-catalog, strict-ID, effort, forwarding and price-policy coverage.
func TestGPT61TerraAccountDiscoveryPreservesCatalog(t *testing.T) {
	for _, catalog := range []string{`{"data":[{"id":"gpt-6.1-terra","owned_by":"openai"}]}`, `{"data":[]}`} {
		gateway := newCodexModelsAPIKeyTestService(&codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
			return ordinaryModelsUpstreamResponse(catalog), nil
		}})
		service := &AccountTestService{openaiGatewayService: gateway}
		account := newCodexModelsAPIKeyTestAccount("https://models.example/v1")
		before, err := gateway.FetchOpenAIModelsList(context.Background(), account)
		require.NoError(t, err)
		models, err := service.FetchOpenAIAccountModels(context.Background(), account)
		require.NoError(t, err)
		if catalog == `{"data":[]}` {
			require.Empty(t, models)
		} else {
			require.Len(t, models, 1)
			require.Equal(t, "gpt-6.1-terra", models[0].ID)
			require.Equal(t, "GPT-6.1 Terra", models[0].DisplayName)
		}
		after, err := gateway.FetchOpenAIModelsList(context.Background(), account)
		require.NoError(t, err)
		require.Equal(t, before.Body, after.Body)
	}
}

func TestGPT61TerraManifestSeparatesAPIAndCodexCapabilities(t *testing.T) {
	for _, native := range []bool{false, true} {
		name := "generic API snapshot"
		if native {
			name = "explicit Codex workflow declaration"
		}
		t.Run(name, func(t *testing.T) {
			account := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			reasoning := true
			metadata := UpstreamModelMetadata{ID: "gpt-6.1-terra", Reasoning: &reasoning, DefaultReasoningLevel: "high", SupportedReasoningLevels: []string{"high"}, InputModalities: []string{"text"}, ContextWindow: 100000}
			if native {
				metadata.CodexToolCapabilities = map[string]json.RawMessage{"multi_agent_version": json.RawMessage(`"v2"`)}
			}
			account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{"gpt-6.1-terra": metadata}})
			service := &GatewayService{accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{1: {account}}}}
			body, err := service.BuildCodexModelsManifestForGroup(context.Background(), &Group{ID: 1, Platform: PlatformOpenAI}, "", []string{"gpt-6.1-terra"})
			require.NoError(t, err)
			models := decodeCodexManifestModels(t, body)
			require.Len(t, models, 1)
			require.Equal(t, []any{"text"}, models[0]["input_modalities"])
			if native {
				require.Equal(t, []string{"high"}, effortsFromManifestModel(t, models[0]))
				require.Equal(t, "high", models[0]["default_reasoning_level"])
			} else {
				require.Equal(t, []string{"low", "medium", "high", "xhigh", "max", "ultra"}, effortsFromManifestModel(t, models[0]))
				require.Equal(t, "low", models[0]["default_reasoning_level"])
			}
			// The snapshot's API context limit is not Codex's compaction window.
			// Actual native manifest windows are separately protected in the
			// TestGPT61TerraAPIKeyCatalogPreservesCapabilities completion test.
			require.EqualValues(t, 500000, models[0]["context_window"])
			require.EqualValues(t, 872000, models[0]["max_context_window"])
		})
	}
}
