package service

// Official Standard/Fast rates verified 2026-09-23:
// https://developers.openai.com/api/docs/pricing
var openAIGPT6SolLunaFallbackPricing = map[string]*LiteLLMModelPricing{
	"gpt-6-sol": {
		InputCostPerToken: 2e-6, OutputCostPerToken: 10e-6,
		CacheReadInputTokenCost: .2e-6, CacheCreationInputTokenCost: 2.5e-6,
		InputCostPerTokenPriority: 4e-6, OutputCostPerTokenPriority: 20e-6,
		CacheReadInputTokenCostPriority: .4e-6, CacheCreationInputTokenCostPriority: 5e-6,
		LongContextInputTokenThreshold: 272_000,
		LongContextInputCostMultiplier: 2, LongContextOutputCostMultiplier: 1.5,
		SupportsServiceTier: true, SupportsPromptCaching: true,
		LiteLLMProvider: "openai", Mode: "responses",
	},
	"gpt-6-luna": {
		InputCostPerToken: .1e-6, OutputCostPerToken: .5e-6,
		CacheReadInputTokenCost: .01e-6, CacheCreationInputTokenCost: .125e-6,
		InputCostPerTokenPriority: .2e-6, OutputCostPerTokenPriority: 1e-6,
		CacheReadInputTokenCostPriority: .02e-6, CacheCreationInputTokenCostPriority: .25e-6,
		LongContextInputTokenThreshold: 272_000,
		LongContextInputCostMultiplier: 2, LongContextOutputCostMultiplier: 1.5,
		SupportsServiceTier: true, SupportsPromptCaching: true,
		LiteLLMProvider: "openai", Mode: "responses",
	},
}
