package service

import (
	"errors"
	"fmt"

	"github.com/tidwall/gjson"
)

// prepareGPT6ChatUpstreamBody is shared by direct Chat and both conversion
// fallbacks. Call it after model mapping and before reading the billed effort.
func prepareGPT6ChatUpstreamBody(account *Account, model string, body []byte) ([]byte, error) {
	if !isOpenAIGPT6Model(model) {
		return body, nil
	}
	// The fork maps none to low. Official GPT-6 tool calls therefore need
	// Responses (6.1 Sol does not support Chat tools at any effort).
	// Never silently drop tools, change endpoints, or restrict third parties.
	if isOfficialOpenAICodexAccount(account) &&
		(len(gjson.GetBytes(body, "tools").Array()) > 0 || len(gjson.GetBytes(body, "functions").Array()) > 0) {
		return nil, errors.New("GPT-6 tool calling with reasoning requires a Responses-capable account; this account is routed to Chat Completions")
	}
	normalized, _, err := normalizeGPT6RequestBody(body, true)
	if err != nil {
		return nil, fmt.Errorf("normalize GPT-6 chat request: %w", err)
	}
	normalized, _, err = filterGPT6PromptCacheOptionsForAccount(normalized, account, model)
	if err != nil {
		return nil, fmt.Errorf("filter GPT-6 chat prompt cache options: %w", err)
	}
	return normalized, nil
}
