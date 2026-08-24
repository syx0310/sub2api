//go:build unit

package service

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIResponsesInputItemIDUsesCodexGenericPrefixRule(t *testing.T) {
	for _, itemType := range []string{"message", "function_call", "reasoning", "custom_tool_call", "future_item"} {
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

func TestSanitizeOpenAIResponsesInputItemIDsUsesGenericRuleWithoutCascading(t *testing.T) {
	body := []byte(`{"input":[
		{"type":"function_call","id":"item_valid_call","call_id":"call_valid","name":"lookup","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_valid","output":"preserve paired output"},
		{"type":"custom_tool_call","id":"fc_cross_namespace","call_id":"call_custom","name":"apply_patch","input":"patch"},
		{"type":"message","id":"malformed-id","content":"strip only this id"},
		{"type":"item_reference","id":"remote_valid"}
	]}`)

	sanitized, changed, err := sanitizeOpenAIResponsesInputItemIDs(body)

	require.NoError(t, err)
	require.True(t, changed)
	items := gjson.GetBytes(sanitized, "input").Array()
	require.Len(t, items, 5)
	require.Equal(t, "item_valid_call", items[0].Get("id").String())
	require.Equal(t, "fc_cross_namespace", items[2].Get("id").String())
	require.False(t, items[3].Get("id").Exists())
	require.Equal(t, "remote_valid", items[4].Get("id").String())
}

func TestSanitizeOpenAIResponsesInputItemIDsLeavesUnrelatedReferencesUntouched(t *testing.T) {
	body := []byte(`{"previous_response_id":"resp_1","input":[{"type":"item_reference","id":"remote_item"}]}`)

	sanitized, changed, err := sanitizeOpenAIResponsesInputItemIDs(body)

	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, sanitized)
}

func TestSanitizeOpenAIResponsesInputItemIDsStripsOnlyNonPairCallIDs(t *testing.T) {
	body := []byte(`{"input":[
		{"type":"message","call_id":"remove_message","content":"hi"},
		{"type":"reasoning","call_id":"remove_reasoning","id":"rs_keep","encrypted_content":"cipher","summary":[]},
		{"type":"image_generation_call","call_id":"remove_image","id":"ig_keep","status":"completed"},
		{"type":"function_call","call_id":"keep_function","name":"lookup","arguments":"{}"},
		{"type":"function_call_output","call_id":"keep_function","output":"ok"},
		{"type":"custom_tool_call","call_id":"keep_custom","name":"patch","input":"x"},
		{"type":"custom_tool_call_output","call_id":"keep_custom","output":"ok"},
		{"type":"tool_search_call","call_id":"keep_search","arguments":"{}"},
		{"type":"tool_search_output","call_id":"keep_search","output":"ok"},
		{"type":"local_shell_call","call_id":"keep_shell","name":"shell","arguments":"{}"}
	]}`)

	sanitized, changed, err := sanitizeOpenAIResponsesInputItemIDs(body)
	require.NoError(t, err)
	require.True(t, changed)
	for i := 0; i < 3; i++ {
		require.False(t, gjson.GetBytes(sanitized, "input."+strconv.Itoa(i)+".call_id").Exists())
	}
	for i := 3; i < 10; i++ {
		require.True(t, gjson.GetBytes(sanitized, "input."+strconv.Itoa(i)+".call_id").Exists())
	}
}
