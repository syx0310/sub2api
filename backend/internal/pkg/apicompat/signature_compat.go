package apicompat

import (
	"encoding/base64"
	"strings"
)

const maxReasoningSignatureLen = 32 * 1024 * 1024

type reasoningSignatureProvider string

const (
	reasoningSignatureProviderUnknown reasoningSignatureProvider = "unknown"
	reasoningSignatureProviderClaude  reasoningSignatureProvider = "claude"
	reasoningSignatureProviderGPT     reasoningSignatureProvider = "gpt"
)

func compatibleGPTReasoningEncryptedContent(raw string) (string, bool) {
	return CompatibleGPTReasoningEncryptedContent(raw)
}

func CompatibleGPTReasoningEncryptedContent(raw string) (string, bool) {
	payload, ok := signaturePayloadForProvider(raw, reasoningSignatureProviderGPT)
	if !ok {
		return "", false
	}
	if !isValidGPTReasoningEncryptedContent(payload) {
		return "", false
	}
	return payload, true
}

func compatibleClaudeThinkingSignature(raw string) (string, bool) {
	return CompatibleClaudeThinkingSignature(raw)
}

func CompatibleClaudeThinkingSignature(raw string) (string, bool) {
	payload, ok := signaturePayloadForProvider(raw, reasoningSignatureProviderClaude)
	if !ok {
		return "", false
	}
	if !isClaudeThinkingSignatureShape(payload) {
		return "", false
	}
	return payload, true
}

func signaturePayloadForProvider(raw string, target reasoningSignatureProvider) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	if provider, payload, ok := splitReasoningSignatureProviderPrefix(trimmed); ok {
		if provider != target {
			return "", false
		}
		return payload, payload != ""
	}
	if strings.Contains(trimmed, "#") {
		return "", false
	}
	return trimmed, true
}

func splitReasoningSignatureProviderPrefix(raw string) (reasoningSignatureProvider, string, bool) {
	prefix, rest, ok := strings.Cut(strings.TrimSpace(raw), "#")
	if !ok {
		return reasoningSignatureProviderUnknown, raw, false
	}
	provider := reasoningSignatureProviderFromPrefix(prefix)
	if provider == reasoningSignatureProviderUnknown {
		return reasoningSignatureProviderUnknown, raw, false
	}
	return provider, strings.TrimSpace(rest), true
}

func reasoningSignatureProviderFromPrefix(prefix string) reasoningSignatureProvider {
	switch strings.ToLower(strings.TrimSpace(prefix)) {
	case "claude", "anthropic":
		return reasoningSignatureProviderClaude
	case "openai", "gpt", "codex":
		return reasoningSignatureProviderGPT
	default:
		return reasoningSignatureProviderUnknown
	}
}

func isValidGPTReasoningEncryptedContent(raw string) bool {
	sig := strings.TrimSpace(raw)
	if sig == "" || len(sig) > maxReasoningSignatureLen || !strings.HasPrefix(sig, "gAAAA") {
		return false
	}
	for _, r := range sig {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '=':
		default:
			return false
		}
	}

	decoded, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		decoded, err = base64.URLEncoding.DecodeString(sig)
	}
	if err != nil || len(decoded) < 73 || decoded[0] != 0x80 {
		return false
	}
	ciphertextLen := len(decoded) - 1 - 8 - 16 - 32
	return ciphertextLen > 0 && ciphertextLen%16 == 0
}

func isClaudeThinkingSignatureShape(raw string) bool {
	sig := strings.TrimSpace(raw)
	if sig == "" || len(sig) > maxReasoningSignatureLen {
		return false
	}
	switch sig[0] {
	case 'E':
		return isClaudeSingleLayerThinkingSignature(sig)
	case 'R':
		decoded, err := decodeStdBase64(sig)
		if err != nil {
			return false
		}
		inner := strings.TrimSpace(string(decoded))
		return strings.HasPrefix(inner, "E") && isClaudeSingleLayerThinkingSignature(inner)
	default:
		return false
	}
}

func isClaudeSingleLayerThinkingSignature(sig string) bool {
	decoded, err := decodeStdBase64(sig)
	if err != nil {
		return false
	}
	return len(decoded) > 0 && decoded[0] == 0x12
}

func decodeStdBase64(sig string) ([]byte, error) {
	if decoded, err := base64.StdEncoding.DecodeString(sig); err == nil {
		return decoded, nil
	}
	return base64.RawStdEncoding.DecodeString(sig)
}
