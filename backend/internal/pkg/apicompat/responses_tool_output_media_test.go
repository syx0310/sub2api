package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLiftResponsesToolOutputMedia(t *testing.T) {
	var input any
	require.NoError(t, json.Unmarshal([]byte(`[
		{"type":"function_call","call_id":"call_image","name":"view_image","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_image","output":[{"type":"input_image","image_url":"data:image/png;base64,AQID","detail":"original"}]}
	]`), &input))
	original, err := json.Marshal(input)
	require.NoError(t, err)
	lifted, changed, err := LiftResponsesToolOutputMedia(input)
	require.NoError(t, err)
	require.True(t, changed)
	items, ok := lifted.([]any)
	require.True(t, ok)
	require.Len(t, items, 3)
	encoded, err := json.Marshal(lifted)
	require.NoError(t, err)
	require.JSONEq(t, `[
		{"type":"function_call","call_id":"call_image","name":"view_image","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_image","output":"[{\"text\":\"[Tool output media moved to the following user message]\",\"type\":\"input_text\"}]"},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"[Tool output media for call call_image]"},{"type":"input_image","image_url":"data:image/png;base64,AQID","detail":"original"}]}
	]`, string(encoded))
	after, err := json.Marshal(input)
	require.NoError(t, err)
	require.Equal(t, original, after, "rewrites must not mutate the source for retries")
}

func TestLiftResponsesToolOutputMediaLeavesPlainOutputUntouched(t *testing.T) {
	for _, output := range []string{"plain output", "data:image/png;base64,AQID", `[{"type":"input_image","image_url":"data:image/png;base64,AQID"}]`} {
		input := []any{map[string]any{"type": "function_call_output", "call_id": "call_text", "output": output}}
		lifted, changed, err := LiftResponsesToolOutputMedia(input)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, input, lifted)
	}
}

func TestLiftResponsesToolOutputMediaKeepsParallelBatchContiguous(t *testing.T) {
	var input any
	require.NoError(t, json.Unmarshal([]byte(`[
		{"type":"function_call_output","call_id":"A","output":[{"type":"input_image","image_url":"data:image/png;base64,QQ=="}]},
		{"type":"function_call_output","call_id":"B","output":"text"},
		{"type":"function_call_output","call_id":"C","output":[{"type":"input_image","image_url":"data:image/png;base64,Qw=="}]},
		{"role":"developer","content":"keep this notice after the batch"}
	]`), &input))
	lifted, changed, err := LiftResponsesToolOutputMedia(input)
	require.NoError(t, err)
	require.True(t, changed)
	items, ok := lifted.([]any)
	require.True(t, ok)
	require.Len(t, items, 5)
	for i, id := range []string{"A", "B", "C"} {
		item, ok := items[i].(map[string]any)
		require.True(t, ok)
		require.Equal(t, id, item["call_id"])
		require.IsType(t, "", item["output"])
	}
	message, ok := items[3].(map[string]any)
	require.True(t, ok)
	content, ok := message["content"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, content, 4)
	require.Equal(t, "[Tool output media for call A]", content[0]["text"])
	require.Equal(t, "[Tool output media for call C]", content[2]["text"])
	source, ok := input.([]any)
	require.True(t, ok)
	require.Equal(t, source[3], items[4])
}

func TestLiftResponsesToolOutputMediaRejectsLossyConversions(t *testing.T) {
	for name, extra := range map[string]string{
		"audio":      `,{"type":"input_audio","audio_url":"data:audio/wav;base64,AQ=="}`,
		"encrypted":  `,{"type":"encrypted_content","encrypted_content":"never-echo-secret"}`,
		"file image": `,{"type":"input_image","file_id":"file_123"}`,
		"unknown":    `,{"type":"future_content","data":123}`,
	} {
		t.Run(name, func(t *testing.T) {
			var input any
			require.NoError(t, json.Unmarshal([]byte(`[{"type":"function_call_output","call_id":"A","output":[{"type":"input_image","image_url":"data:image/png;base64,QQ=="}`+extra+`]}]`), &input))
			_, changed, err := LiftResponsesToolOutputMedia(input)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "never-echo-secret")
			require.False(t, changed)
		})
	}
	for _, role := range []string{"developer", "system"} {
		var input any
		require.NoError(t, json.Unmarshal([]byte(`[
			{"type":"function_call_output","call_id":"A","output":[{"type":"input_image","image_url":"data:image/png;base64,QQ=="}]},
			{"role":"`+role+`","content":"instruction boundary"},
			{"type":"function_call_output","call_id":"B","output":"text"}
		]`), &input))
		before, err := json.Marshal(input)
		require.NoError(t, err)
		lifted, changed, err := LiftResponsesToolOutputMedia(input)
		require.ErrorContains(t, err, "across system/developer instructions")
		require.False(t, changed)
		after, err := json.Marshal(lifted)
		require.NoError(t, err)
		require.Equal(t, before, after)
	}
}
