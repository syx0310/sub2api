package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func rewriteGPT55CompactToGPT54Enabled(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return cfg.Gateway.OpenAICompat.RewriteGPT55CompactToGPT54
}

func NormalizeOpenAIResponsesCompactRequestedModel(model string, compact bool, cfgs ...*config.Config) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" || !compact {
		return trimmed
	}
	cfg := firstOpenAICompatConfig(cfgs...)
	if !rewriteGPT55CompactToGPT54Enabled(cfg) {
		return trimmed
	}
	if rewritten, ok := rewrittenGPT55CompactRequestModel(trimmed); ok {
		return rewritten
	}
	return trimmed
}

func rewrittenGPT55CompactRequestModel(model string) (string, bool) {
	modelID := strings.TrimSpace(model)
	if modelID == "" {
		return "", false
	}
	if strings.Contains(modelID, "/") {
		parts := strings.Split(modelID, "/")
		modelID = parts[len(parts)-1]
	}
	modelID = strings.TrimSpace(modelID)
	switch strings.ToLower(modelID) {
	case "gpt-5.5", "gpt 5.5":
		return "gpt-5.4", true
	default:
		return "", false
	}
}
