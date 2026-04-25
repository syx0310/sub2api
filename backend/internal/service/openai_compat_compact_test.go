package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNormalizeOpenAIResponsesCompactRequestedModel(t *testing.T) {
	t.Parallel()

	require.Equal(t, "gpt-5.5", NormalizeOpenAIResponsesCompactRequestedModel("gpt-5.5", true))
	require.Equal(t, "gpt-5.5", NormalizeOpenAIResponsesCompactRequestedModel("gpt-5.5", false, &config.Config{Gateway: config.GatewayConfig{OpenAICompat: config.GatewayOpenAICompatConfig{RewriteGPT55CompactToGPT54: true}}}))

	cfg := &config.Config{Gateway: config.GatewayConfig{OpenAICompat: config.GatewayOpenAICompatConfig{RewriteGPT55CompactToGPT54: true}}}
	require.Equal(t, "gpt-5.4", NormalizeOpenAIResponsesCompactRequestedModel("gpt-5.5", true, cfg))
	require.Equal(t, "gpt-5.4", NormalizeOpenAIResponsesCompactRequestedModel("openai/gpt-5.5", true, cfg))
	require.Equal(t, "gpt-5.4-mini", NormalizeOpenAIResponsesCompactRequestedModel("gpt-5.4-mini", true, cfg))
}
