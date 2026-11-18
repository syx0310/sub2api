package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Preserve the fork's existing mid-conversation role policy; do not silently
// promote hook output while merging a provider translation update.
func TestAnthropicToResponses_MidConversationSystemKeepsForkPolicy(t *testing.T) {
	out, err := AnthropicToResponses(&AnthropicRequest{
		Model:     "gpt-5.2",
		MaxTokens: 64,
		Messages: []AnthropicMessage{
			{Role: "user", Content: json.RawMessage(`"hi"`)},
			{Role: "system", Content: json.RawMessage(`[{"type":"text","text":"hook output"}]`)},
			{Role: "system", Content: json.RawMessage(`"extra context"`)},
		},
	})
	require.NoError(t, err)

	input := gjson.ParseBytes(out.Input).Array()
	require.Len(t, input, 3)
	require.Equal(t, "user", input[0].Get("role").String())
	require.Equal(t, "user", input[1].Get("role").String())
	require.Equal(t, "hook output", input[1].Get("content.0.text").String())
	require.Equal(t, "user", input[2].Get("role").String())
	require.Equal(t, "extra context", input[2].Get("content.0.text").String())
}
