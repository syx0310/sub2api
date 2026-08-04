//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Codex accepts any non-empty prefix/suffix pair so legacy server IDs remain
// replayable even when their prefix differs from the current generated prefix.
func TestFilterCodexInput_KeepsPrefixedMessageItemID_WhenPreservingReferences(t *testing.T) {
	input := []any{
		map[string]any{
			"type": "message",
			"id":   "item_3bc5a3fa8ccde25f1c0000d4",
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_text", "text": "hello"},
			},
		},
	}

	filtered := filterCodexInputWithOptions(input, codexInputFilterOptions{
		PreserveReferences: true,
	})

	require.Len(t, filtered, 1)

	msg, ok := filtered[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "message", msg["type"])
	require.Equal(t, "item_3bc5a3fa8ccde25f1c0000d4", msg["id"])
	require.Equal(t, "user", msg["role"], "role must be preserved")
	require.NotNil(t, msg["content"], "content must be preserved")
}

// TestFilterCodexInput_KeepsMsgID_WhenPreservingReferences
// verifies that message items with a valid msg* id are kept when
// PreserveReferences is true, so context references are not lost.
func TestFilterCodexInput_KeepsMsgID_WhenPreservingReferences(t *testing.T) {
	input := []any{
		map[string]any{
			"type": "message",
			"id":   "msg_validID123",
			"role": "assistant",
		},
	}

	filtered := filterCodexInputWithOptions(input, codexInputFilterOptions{
		PreserveReferences: true,
	})

	require.Len(t, filtered, 1)
	msg, ok := filtered[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "msg_validID123", msg["id"], "valid msg* id must be preserved")
}

// The same generic prefixed-ID rule applies to a full request.
func TestFilterCodexInput_OnlyKeepsPrefixedMessageIDWithoutReferences(t *testing.T) {
	for _, tc := range []struct {
		id   string
		keep bool
	}{
		{id: "item_abc", keep: true},
		{id: "msg_validID123", keep: true},
		{id: "legacy-id", keep: false},
	} {
		tc := tc
		t.Run(tc.id, func(t *testing.T) {
			input := []any{
				map[string]any{
					"type": "message",
					"id":   tc.id,
					"role": "user",
				},
			}

			filtered := filterCodexInputWithOptions(input, codexInputFilterOptions{
				PreserveReferences: false,
			})

			require.Len(t, filtered, 1)
			msg, ok := filtered[0].(map[string]any)
			require.True(t, ok)
			id, hasID := msg["id"]
			require.Equal(t, tc.keep, hasID)
			if tc.keep {
				require.Equal(t, tc.id, id)
			}
		})
	}
}

// TestFilterCodexInput_MessageIDStripDoesNotMutateInput ensures the original
// input map is not modified in place when the id is stripped.
func TestFilterCodexInput_MessageIDStripDoesNotMutateInput(t *testing.T) {
	original := map[string]any{
		"type": "message",
		"id":   "legacy-id",
		"role": "user",
	}

	filtered := filterCodexInputWithOptions([]any{original}, codexInputFilterOptions{
		PreserveReferences: true,
	})

	require.Len(t, filtered, 1)
	require.Equal(t, "legacy-id", original["id"], "original input must not be mutated")
}

func TestFilterCodexInput_GenericPrefixedIDsKeepFunctionCallBehavior(t *testing.T) {
	input := []any{
		map[string]any{
			"type": "message",
			"id":   "item_msg_001",
			"role": "user",
		},
		map[string]any{
			"type":    "function_call",
			"id":      "fc_validID123",
			"call_id": "fc_validID123",
			"name":    "bash",
		},
		map[string]any{
			"type":    "function_call",
			"id":      "item_A9v0SNfS3VaLrfX0j3y4xhyK",
			"call_id": "fc_abc123",
			"name":    "bash",
		},
		map[string]any{
			"type":    "function_call_output",
			"id":      "fco_1",
			"call_id": "fc_abc123",
			"output":  "done",
		},
	}

	filtered := filterCodexInputWithOptions(input, codexInputFilterOptions{
		PreserveReferences: true,
	})

	require.Len(t, filtered, 4)

	msg, ok := filtered[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "item_msg_001", msg["id"])

	fcValid, ok := filtered[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "fc_validID123", fcValid["id"], "valid fc* id must be preserved")

	fcBad, ok := filtered[2].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "item_A9v0SNfS3VaLrfX0j3y4xhyK", fcBad["id"])
	require.Equal(t, "fc_abc123", fcBad["call_id"], "call_id pairing must survive")

	out, ok := filtered[3].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "fco_1", out["id"], "typed output item id should be preserved")
	require.Equal(t, "fc_abc123", out["call_id"], "call_id pairing must survive")
}
