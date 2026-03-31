package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func firstOpenAICompatConfig(cfgs ...*config.Config) *config.Config {
	for _, cfg := range cfgs {
		if cfg != nil {
			return cfg
		}
	}
	return nil
}

func rewriteGPT53CodexSparkEnabled(cfg *config.Config) bool {
	if cfg == nil {
		return true
	}
	return cfg.Gateway.OpenAICompat.RewriteGPT53CodexSpark
}

func normalizeCodexRequestModel(model string, cfgs ...*config.Config) string {
	cfg := firstOpenAICompatConfig(cfgs...)
	if !rewriteGPT53CodexSparkEnabled(cfg) {
		if canonical, ok := canonicalGPT53CodexSparkRequestModel(model); ok {
			return canonical
		}
	}
	return normalizeCodexModel(model)
}

func splitGPT53CodexSparkRequestModel(model string) (base string, suffix string, ok bool) {
	modelID := strings.TrimSpace(model)
	if modelID == "" {
		return "", "", false
	}
	if strings.Contains(modelID, "/") {
		parts := strings.Split(modelID, "/")
		modelID = parts[len(parts)-1]
	}
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return "", "", false
	}

	tokens := strings.FieldsFunc(strings.ToLower(modelID), func(r rune) bool {
		switch r {
		case '-', '_', ' ':
			return true
		default:
			return false
		}
	})
	if len(tokens) < 4 || tokens[0] != "gpt" || tokens[1] != "5.3" || tokens[2] != "codex" || tokens[3] != "spark" {
		return "", "", false
	}

	base = "gpt-5.3-codex-spark"
	if len(tokens) == 4 {
		return base, "", true
	}
	if len(tokens) != 5 {
		return "", "", false
	}

	switch tokens[4] {
	case "none", "minimal", "low", "medium", "high":
		return base, tokens[4], true
	case "xhigh", "extrahigh":
		return base, "xhigh", true
	default:
		return "", "", false
	}
}

func canonicalGPT53CodexSparkRequestModel(model string) (string, bool) {
	base, suffix, ok := splitGPT53CodexSparkRequestModel(model)
	if !ok {
		return "", false
	}
	if suffix == "" {
		return base, true
	}
	return base + "-" + suffix, true
}
