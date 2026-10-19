package service

import (
	_ "embed"
	"encoding/json"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

// Sol/Luna source: docs/model-catalog-2026-09-23.json; model_messages from
// Codex 408a77dc1a1cf95413df26b5185623cf245161d7. GPT-6.1 Sol uses upstream's
// openai.CodexGPT61SolMetadata, verified unchanged against Codex dd90f160ed9b.
// This is model-list metadata only;
// it must not change the gateway's existing request instructions policy.
//
//go:embed openai_codex_gpt6_sol_luna_models.json
var gpt6SolLunaCodexModelsJSON []byte

var (
	gpt6SolLunaCodexModelsOnce sync.Once
	gpt6SolLunaCodexModels     map[string]configuredCodexModelDescriptor
)

func configuredGPT6SolLunaModelDescriptor(modelID string) (configuredCodexModelDescriptor, bool) {
	gpt6SolLunaCodexModelsOnce.Do(func() {
		if err := json.Unmarshal(gpt6SolLunaCodexModelsJSON, &gpt6SolLunaCodexModels); err != nil {
			panic(err)
		}
		var sol configuredCodexModelDescriptor
		if err := json.Unmarshal(openai.CodexGPT61SolMetadata, &sol); err != nil {
			panic(err)
		}
		sol.officialMetadata = openai.CodexGPT61SolMetadata
		// Match the generated-catalog default in upstream v0.2.16. This is
		// only a fallback; explicit account catalog metadata remains authoritative.
		sol.ContextWindow = 500_000
		gpt6SolLunaCodexModels[openai.GPT61SolModelID] = sol
	})
	descriptor, ok := gpt6SolLunaCodexModels[canonicalizeOpenAIModelAliasSpelling(modelID)]
	return descriptor, ok
}
