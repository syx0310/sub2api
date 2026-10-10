package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodexHistoryInitializationRemainsClientOwned(t *testing.T) {
	for _, initialization := range []string{"new", "cleared", "cold_resume", "warm_fork", "cold_fork", "supplied_history"} {
		t.Run(initialization, func(t *testing.T) {
			account := newTestOAuthAccount(1, map[string]any{"openai_device_id": "account-device"})
			ids := resolveCodexFingerprintIDsFromRequest(account, nil)
			metadata := `{"installation_id":"client-device","session_id":"session","thread_id":"child","parent_thread_id":"parent","history_initialization":"` + initialization + `","request_kind":"turn","future":{"value":"子代理"}}`
			body := map[string]any{"client_metadata": map[string]any{openAIWSTurnMetadataHeader: metadata}}
			require.True(t, applyCodexFingerprintClientMetadata(body, ids))
			out, err := json.Marshal(body)
			require.NoError(t, err)
			embedded := gjson.GetBytes(out, "client_metadata."+openAIWSTurnMetadataHeader).String()
			require.Equal(t, initialization, gjson.Get(embedded, "history_initialization").String())
			require.Equal(t, "session", gjson.Get(embedded, "session_id").String())
			require.Equal(t, "child", gjson.Get(embedded, "thread_id").String())
			require.Equal(t, "parent", gjson.Get(embedded, "parent_thread_id").String())
			require.Equal(t, "子代理", gjson.Get(embedded, "future.value").String())
			require.Equal(t, "account-device", gjson.Get(embedded, "installation_id").String())
		})
	}
}

func TestSynthesizedWSFailurePreservesRetryAdviceOnly(t *testing.T) {
	for _, source := range []string{
		`{"error":{"code":"server_is_overloaded","message":"busy","headers":{"Retry-After":"30","Authorization":"secret"}}}`,
		`{"response":{"error":{"code":"server_is_overloaded","message":"busy","headers":{"retry-after":"30","Set-Cookie":"secret"}}}}`,
		`{"error":{"code":"server_is_overloaded","message":"busy"},"headers":{"RETRY-AFTER":"30","Cookie":"secret"}}`,
	} {
		out := buildOpenAIWSHTTPBridgeFailedEvent("resp_1", "gpt-6.1-sol", []byte(source), "")
		require.Equal(t, "response.failed", gjson.GetBytes(out, "type").String())
		require.JSONEq(t, `{"Retry-After":"30"}`, gjson.GetBytes(out, "response.error.headers").Raw)
		require.NotContains(t, string(out), "secret")
	}
	for _, value := range []string{`30`, `null`, `"30\r\nSet-Cookie: secret"`} {
		out := buildOpenAIWSHTTPBridgeFailedEvent("", "", []byte(`{"error":{"headers":{"Retry-After":`+value+`}}}`), "busy")
		require.False(t, gjson.GetBytes(out, "response.error.headers").Exists())
	}
}
