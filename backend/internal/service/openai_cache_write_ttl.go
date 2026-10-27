package service

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/tidwall/gjson"
)

// The flag describes the final request, not a client hint before filtering.
// ChatGPT OAuth and third-party providers do not inherit the public API TTL
// contract. Other models keep their existing billing policy in this change.
func openAIUses12hCacheWrite(account *Account, model, ttl string) bool {
	return canonicalizeOpenAIModelAliasSpelling(model) == openai.GPT61TerraModelID &&
		shouldPreserveOpenAIPromptCacheOptions(account, model) && ttl == "12h"
}

func openAIUses12hCacheWriteBody(account *Account, model string, body []byte) bool {
	return openAIUses12hCacheWrite(account, model, gjson.GetBytes(body, "prompt_cache_options.ttl").String())
}

func openAIUses12hCacheWriteMap(account *Account, model string, body map[string]any) bool {
	options, _ := body["prompt_cache_options"].(map[string]any)
	ttl, _ := options["ttl"].(string)
	return openAIUses12hCacheWrite(account, model, ttl)
}
