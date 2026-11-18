package service

import "time"

// Official GPT-6.2 prices (2026-11-18), USD/token. These independent cards
// deliberately do not alias older Sol/Astra cards or operator overrides.
func openAIGPT62Pricing(input, output, read float64) *LiteLLMModelPricing {
	return &LiteLLMModelPricing{
		CacheCreationInputTokenCostExplicit: true,
		InputCostPerToken:                   input, InputCostPerTokenPriority: input * 2,
		OutputCostPerToken: output, OutputCostPerTokenPriority: output * 2,
		CacheReadInputTokenCost: read, CacheReadInputTokenCostPriority: read * 2,
		CacheCreationInputTokenCost: input * 1.25, CacheCreationInputTokenCostPriority: input * 2.5,
		LongContextInputTokenThreshold: 272_000,
		LongContextInputCostMultiplier: 2, LongContextOutputCostMultiplier: 1.5,
		SupportsServiceTier: true, LiteLLMProvider: "openai", Mode: "responses", SupportsPromptCaching: true,
	}
}

// March 1, 2027 midnight America/Los_Angeles is 08:00 UTC (before DST).
// PricingAt is the frozen logical-turn timestamp, not async settlement time.
var openAIGPT62UltrafastPromotionEnd = time.Date(2027, 3, 1, 8, 0, 0, 0, time.UTC)

func openAIUltrafastMultiplierAt(model string, pricingAt time.Time) float64 {
	model = normalizeKnownOpenAICodexModel(model)
	if !openAIModelSupportsPublicUltrafast(model) {
		return 0
	}
	if model == "gpt-6.2-sol" || model == "gpt-6.2-astra" {
		if pricingAt.IsZero() {
			pricingAt = time.Now()
		}
		if pricingAt.Before(openAIGPT62UltrafastPromotionEnd) {
			return 3
		}
	}
	return 6
}

// Share the upstream fallback definitions with billing and model lookup.
var openAIGPT6StandardFallbackPricing = map[string]*LiteLLMModelPricing{
	"gpt-6.2-sol":   openAIGPT62Pricing(2e-6, 10e-6, .1e-6),
	"gpt-6.2-astra": openAIGPT62Pricing(8e-6, 40e-6, .4e-6),
	"gpt-6-sol":     openAIGPT6SolFallbackPricing,
	"gpt-6.1-sol":   openAIGPT61SolFallbackPricing,
	"gpt-6.1-terra": openAIGPT61TerraFallbackPricing,
	"gpt-6-luna":    openAIGPT6LunaFallbackPricing,
}
