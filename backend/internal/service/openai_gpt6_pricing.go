package service

// Share the upstream fallback definitions with billing and model lookup.
var openAIGPT6StandardFallbackPricing = map[string]*LiteLLMModelPricing{
	"gpt-6-sol":     openAIGPT6SolFallbackPricing,
	"gpt-6.1-sol":   openAIGPT61SolFallbackPricing,
	"gpt-6.1-terra": openAIGPT61TerraFallbackPricing,
	"gpt-6-luna":    openAIGPT6LunaFallbackPricing,
}
