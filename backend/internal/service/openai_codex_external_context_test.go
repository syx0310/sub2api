package service

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodexExternalContextValidationAndReplay(t *testing.T) {
	for _, name := range []string{"browser_info", "goal_objective", "untrusted_context", "untrusted_content"} {
		body := []byte(fmt.Sprintf(`{"model":"gpt-6.2-sol","input":[{"type":"function_call_output","namespace":"agent","name":%q,"output":"external value"}]}`, name))
		var request map[string]any
		require.NoError(t, json.Unmarshal(body, &request))
		require.False(t, ValidateFunctionCallOutputContextBytes(body).HasFunctionCallOutput)
		require.Equal(t, ValidateFunctionCallOutputContext(request), ValidateFunctionCallOutputContextBytes(body))
		require.False(t, AnalyzeToolContinuationSignals(request).HasFunctionCallOutput)
		require.False(t, AnalyzeToolCallOutputContextCoverageBytes(body).HasFunctionCallOutput)
		for _, typ := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
			out, _, err := normalizeOpenAIResponsesWebSocketCompatibilityBody(body, &Account{Platform: PlatformOpenAI, Type: typ}, true)
			require.NoError(t, err)
			require.JSONEq(t, gjson.GetBytes(body, "input").Raw, gjson.GetBytes(out, "input").Raw)
		}
	}
	for _, item := range []string{
		`{"type":"function_call_output","namespace":"agent","name":"read_file","output":"result"}`,
		`{"type":"function_call_output","namespace":"other","name":"browser_info","output":"result"}`,
		`{"type":"function_call_output","namespace":"agent","name":"browser_info","call_id":null,"output":"result"}`,
		`{"type":"function_call_output","namespace":"agent","name":"browser_info","output":[]}`,
	} {
		body := []byte(`{"input":[` + item + `]}`)
		require.True(t, ValidateFunctionCallOutputContextBytes(body).HasFunctionCallOutputMissingCallID)
	}
	body := []byte(`{"input":[{"type":"function_call_output","namespace":"agent","name":"browser_info","output":"context"},{"type":"function_call_output","name":"read_file","output":"orphan"}]}`)
	require.True(t, ValidateFunctionCallOutputContextBytes(body).HasFunctionCallOutputMissingCallID, "external context must not validate a separate orphan result")
}

func TestToolSchemaPatternAccountBoundary(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","name":"f","parameters":{"type":"object","properties":{"text":{"type":"string","pattern":"^\\p{L}+$"}}}}]}`)
	for _, typ := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
		account := &Account{Platform: PlatformOpenAI, Type: typ}
		out, changed, err := sanitizeOpenAIResponsesToolSchemasForAccount(body, account)
		require.NoError(t, err)
		require.Equal(t, typ == AccountTypeOAuth, changed)
		if typ == AccountTypeAPIKey {
			require.Equal(t, body, out)
		}
		again, changed, err := sanitizeOpenAIResponsesToolSchemasForAccount(out, account)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, out, again)
	}
}

func TestAdditionalToolOpaqueIDsSurviveNativeReplay(t *testing.T) {
	for _, input := range []string{
		`[{"type":"additional_tools","id":"A7","tools":[]},{"type":"item_reference","id":"A7"}]`,
		`[{"type":"item_reference","id":"A7"}]`,
	} {
		body := []byte(`{"model":"gpt-6.2-sol","input":` + input + `}`)
		for _, typ := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
			out, _, err := normalizeOpenAIResponsesWebSocketCompatibilityBody(body, &Account{Platform: PlatformOpenAI, Type: typ}, true)
			require.NoError(t, err)
			require.JSONEq(t, input, gjson.GetBytes(out, "input").Raw)
		}
	}
}
