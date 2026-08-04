//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIResponsesInputItemIDUsesCodexGenericPrefixRule(t *testing.T) {
	for _, itemType := range []string{"message", "function_call", "reasoning", "future_item"} {
		t.Run(itemType, func(t *testing.T) {
			require.False(t, shouldStripOpenAIResponsesInputItemID(itemType, "future_01"))
			require.False(t, shouldStripOpenAIResponsesInputItemID(itemType, "item_legacy"))
			require.False(t, shouldStripOpenAIResponsesInputItemID(itemType, " _suffix"), "Codex checks the original string without trimming")
			require.False(t, shouldStripOpenAIResponsesInputItemID(itemType, "prefix_ "), "a non-empty whitespace suffix is still prefixed to Codex")
			require.True(t, shouldStripOpenAIResponsesInputItemID(itemType, "legacy-id"))
			require.True(t, shouldStripOpenAIResponsesInputItemID(itemType, "_missing_prefix"))
			require.True(t, shouldStripOpenAIResponsesInputItemID(itemType, "missing_"))
			require.True(t, shouldStripOpenAIResponsesInputItemID(itemType, ""))
		})
	}
}
