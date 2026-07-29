package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeOpenAIPassthroughOAuthBody_RemovesUnsupportedUser(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"hello","user":"user_123","metadata":{"user_id":"user_123"},"prompt_cache_retention":"24h","safety_identifier":"sid","stream_options":{"include_usage":true}}`)

	normalized, changed, err := normalizeOpenAIPassthroughOAuthBody(body, false, false)
	require.NoError(t, err)
	require.True(t, changed)
	for _, field := range openAIChatGPTInternalUnsupportedFields {
		require.False(t, gjson.GetBytes(normalized, field).Exists(), "%s should be stripped", field)
	}
	require.True(t, gjson.GetBytes(normalized, "stream").Bool())
	require.False(t, gjson.GetBytes(normalized, "store").Bool())
}

func TestNormalizeOpenAIPassthroughOAuthBody_CompactRemovesUnsupportedUser(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"hello","user":"user_123","metadata":{"user_id":"user_123"},"stream":true,"store":true}`)

	normalized, changed, err := normalizeOpenAIPassthroughOAuthBody(body, true, false)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(normalized, "user").Exists())
	require.False(t, gjson.GetBytes(normalized, "metadata").Exists())
	require.False(t, gjson.GetBytes(normalized, "stream").Exists())
	require.False(t, gjson.GetBytes(normalized, "store").Exists())
}

func TestNormalizeOpenAIPassthroughOAuthBody_ResponsesLitePreservesMissingInstructions(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":[{"type":"additional_tools","role":"developer","tools":[{"type":"function","name":"shell"}]},{"type":"message","role":"developer","content":[{"type":"input_text","text":"instructions"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`)

	normalized, changed, err := normalizeOpenAIPassthroughOAuthBody(body, false, true)

	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(normalized, "instructions").Exists())
	require.False(t, gjson.GetBytes(normalized, "tools").Exists())
	require.Equal(t, "additional_tools", gjson.GetBytes(normalized, "input.0.type").String())
	require.Equal(t, "developer", gjson.GetBytes(normalized, "input.1.role").String())
	require.False(t, gjson.GetBytes(normalized, "store").Bool())
	require.True(t, gjson.GetBytes(normalized, "stream").Bool())
}

func TestIsOpenAIResponsesLiteWebSocketPayload(t *testing.T) {
	require.True(t, isOpenAIResponsesLiteWebSocketPayload([]byte(`{"client_metadata":{"ws_request_header_x_openai_internal_codex_responses_lite":"true"}}`)))
	require.True(t, isOpenAIResponsesLiteWebSocketPayload([]byte(`{"client_metadata":{"ws_request_header_x_openai_internal_codex_responses_lite":"TRUE"}}`)))
	require.False(t, isOpenAIResponsesLiteWebSocketPayload([]byte(`{"client_metadata":{"ws_request_header_x_openai_internal_codex_responses_lite":"false"}}`)))
	require.False(t, isOpenAIResponsesLiteWebSocketPayload([]byte(`{}`)))
}

func TestNormalizeOpenAIPassthroughOAuthBody_StringInputWrappedAsArray(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"hello world"}`)

	normalized, changed, err := normalizeOpenAIPassthroughOAuthBody(body, false, false)
	require.NoError(t, err)
	require.True(t, changed)

	input := gjson.GetBytes(normalized, "input")
	require.True(t, input.IsArray(), "string input should be converted to array")
	items := input.Array()
	require.Len(t, items, 1)
	require.Equal(t, "message", items[0].Get("type").String())
	require.Equal(t, "user", items[0].Get("role").String())
	require.Equal(t, "hello world", items[0].Get("content").String())
}

func TestNormalizeOpenAIPassthroughOAuthBody_EmptyStringInputWrappedAsEmptyArray(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"  "}`)

	normalized, changed, err := normalizeOpenAIPassthroughOAuthBody(body, false, false)
	require.NoError(t, err)
	require.True(t, changed)

	input := gjson.GetBytes(normalized, "input")
	require.True(t, input.IsArray())
	require.Len(t, input.Array(), 0, "whitespace-only input should become empty array")
}

func TestNormalizeOpenAIPassthroughOAuthBody_ObjectInputWrappedAsArray(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":{"type":"message","role":"user","content":"hi"}}`)

	normalized, changed, err := normalizeOpenAIPassthroughOAuthBody(body, false, false)
	require.NoError(t, err)
	require.True(t, changed)

	input := gjson.GetBytes(normalized, "input")
	require.True(t, input.IsArray(), "object input should be wrapped in array")
	items := input.Array()
	require.Len(t, items, 1)
	require.Equal(t, "message", items[0].Get("type").String())
}

func TestNormalizeOpenAIPassthroughOAuthBody_ObjectSystemInputWrappedAndRewritten(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":{"type":"message","role":"system","content":"be precise"}}`)

	normalized, changed, err := normalizeOpenAIPassthroughOAuthBody(body, false, false)
	require.NoError(t, err)
	require.True(t, changed)

	input := gjson.GetBytes(normalized, "input")
	require.True(t, input.IsArray())
	require.Len(t, input.Array(), 1)
	require.Equal(t, "developer", input.Array()[0].Get("role").String())
}

func TestNormalizeOpenAIPassthroughOAuthBody_ArrayInputUnchanged(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":"hi"}]}`)

	normalized, _, err := normalizeOpenAIPassthroughOAuthBody(body, false, false)
	require.NoError(t, err)

	input := gjson.GetBytes(normalized, "input")
	require.True(t, input.IsArray())
	require.Len(t, input.Array(), 1)
	require.Equal(t, "message", input.Array()[0].Get("type").String())
}
