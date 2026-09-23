package service

import (
	_ "embed"
	"encoding/json"
	"sync"
)

// Source: docs/model-catalog-2026-09-23.json. model_messages matches Codex
// 408a77dc1a1cf95413df26b5185623cf245161d7. This is model-list metadata only;
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
	})
	descriptor, ok := gpt6SolLunaCodexModels[canonicalizeOpenAIModelAliasSpelling(modelID)]
	return descriptor, ok
}
