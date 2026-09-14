package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestResponsesRequestSemanticsPerCreate(t *testing.T) {
	for _, tc := range []struct {
		name, body, kind string
		prewarm, compact bool
	}{
		{"ordinary", `{"type":"response.create","input":[]}`, "turn", false, false},
		{"compact without stream", `{"type":"response.create","input":[{"type":"compaction_trigger"}]}`, "compaction", false, true},
		{"incremental compact", `{"type":"response.create","previous_response_id":"resp_1","input":[{"type":"compaction_trigger"}]}`, "compaction", false, true},
		{"history only", `{"input":[{"type":"compaction"},{"type":"compaction_summary"},{"type":"context_compaction"}]}`, "turn", false, false},
		{"prewarm", `{"generate":false,"input":[]}`, "prewarm", true, false},
		{"prewarm wins", `{"generate":false,"input":[{"type":"compaction_trigger"}]}`, "prewarm", true, false},
		{"string false is not prewarm", `{"generate":"false"}`, "turn", false, false},
		{"memory", `{"client_metadata":{"x-codex-turn-metadata":"{\"request_kind\":\"memory\"}"}}`, "memory", false, false},
		{"declared compact only", `{"client_metadata":{"x-codex-turn-metadata":"{\"request_kind\":\"compaction\"}"}}`, "compaction", false, false},
		{"prewarm claim only", `{"client_metadata":{"x-codex-turn-metadata":"{\"request_kind\":\"prewarm\"}"}}`, "turn", false, false},
		{"opaque metadata", `{"client_metadata":{"x-codex-turn-metadata":"not-json"}}`, "turn", false, false},
		{"control never inherits", `{"type":"response.steer","input":[{"type":"compaction_trigger"}]}`, "control", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, OpenAIResponsesRequestSemantics{Kind: tc.kind, Prewarm: tc.prewarm, NativeCompactionV2: tc.compact}, ClassifyOpenAIResponsesRequest([]byte(tc.body)))
		})
	}
}

func TestCodexTurnMetadataCanonicalSnapshot(t *testing.T) {
	const header = `{"turn_id":"T1","request_kind":"prewarm","window_id":"W1","removed_extra":"do not resurrect"}`
	const embedded = `{"installation_id":"I","session_id":"S","thread_id":"thread","agent_name":"agent","turn_id":"T2","window_id":"W2","window_number":2,"context_window_id":"C2","request_kind":"compaction","forked_from_thread_id":"parent","forked_from_ordinal_exclusive":18446744073709551615,"parent_thread_id":"parent","parent_turn_id":"PT","root_turn_id":"RT","subagent_kind":"worker","thread_source":{"subagent":"worker"},"turn_trigger":"steer","sandbox":"sandbox","sandbox_mode":"workspace-write","auto_review_enabled":false,"node_repl_auto_review_required":false,"node_repl_disabled":false,"workspaces":{"repo":{"associated_remote_urls":{"origin":"https://example.com/repo"},"latest_git_commit_hash":"abc","has_changes":false}},"tool_namespaces_info":{"tools":{"name":"tools","functions":{"run":{"name":"run","direct":false,"code_mode_name":null,"deferred":true,"source":{"kind":"mcp","server_name":"test"}}}}},"turn_started_at_unix_ms":9007199254740993,"history_ingest_requested":false,"analytics_enabled":false,"compaction":{"trigger":"auto","reason":"context_limit","implementation":"remote","phase":"mid_turn","strategy":"native_v2"},"custom_extra":"new"}`
	body, err := sjson.SetBytes([]byte(`{"model":"gpt-6-astra","input":[],"client_metadata":{"turn_id":"T2","x-codex-window-id":"W2","traceparent":"trace","x-codex-turn-state":"opaque"}}`), "client_metadata."+openAIWSTurnMetadataHeader, embedded)
	require.NoError(t, err)
	for _, turn := range []int{1, 2, 3} {
		got, err := normalizeOpenAIWSInitialTurnMetadata(body, header, turn)
		require.NoError(t, err)
		require.Equal(t, body, got, "a complete snapshot must remain byte-for-byte intact")
	}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "acct-test"}}
	got, changed, err := applyCodexAccountIdentityClientMetadataRaw(body, account, 7)
	require.NoError(t, err)
	require.True(t, changed)
	cm := gjson.GetBytes(got, "client_metadata")
	inner := gjson.Parse(cm.Get(openAIWSTurnMetadataHeader).String())
	require.Equal(t, cm.Get("turn_id").String(), inner.Get("turn_id").String())
	require.Equal(t, cm.Get("x-codex-window-id").String(), inner.Get("window_id").String())
	require.NotEqual(t, "T2", inner.Get("turn_id").String())
	for _, key := range []string{"tool_namespaces_info", "workspaces", "compaction", "auto_review_enabled", "node_repl_disabled", "history_ingest_requested", "analytics_enabled", "forked_from_ordinal_exclusive", "turn_started_at_unix_ms", "custom_extra"} {
		expected := gjson.Get(embedded, key)
		require.True(t, inner.Get(key).Exists(), key)
		if expected.Type == gjson.JSON {
			require.JSONEq(t, expected.Raw, inner.Get(key).Raw, key)
		} else {
			require.Equal(t, expected.Raw, inner.Get(key).Raw, key)
		}
	}
	require.False(t, inner.Get("removed_extra").Exists())
	require.Equal(t, "trace", cm.Get("traceparent").String())
	require.Equal(t, "opaque", cm.Get("x-codex-turn-state").String())
}

func TestCodexTurnMetadataPresenceAndFirstFrameFallback(t *testing.T) {
	const header = `{"turn_id":"initial"}`
	for _, body := range []string{
		`{"client_metadata":{"x-codex-turn-metadata":null}}`,
		`{"client_metadata":{"x-codex-turn-metadata":""}}`,
		`{"client_metadata":{"x-codex-turn-metadata":"{}"}}`,
		`{"client_metadata":null}`,
	} {
		got, err := normalizeOpenAIWSInitialTurnMetadata([]byte(body), header, 1)
		require.NoError(t, err)
		require.Equal(t, body, string(got))
	}
	for _, turn := range []int{1, 2} {
		got, err := normalizeOpenAIWSInitialTurnMetadata([]byte(`{"client_metadata":{"traceparent":"trace"}}`), header, turn)
		require.NoError(t, err)
		require.Equal(t, turn == 1, gjson.GetBytes(got, "client_metadata."+openAIWSTurnMetadataHeader).Exists())
		require.Equal(t, "trace", gjson.GetBytes(got, "client_metadata.traceparent").String())
	}
}

func TestCodexResponsesStreamOptionsHTTPAndWSParity(t *testing.T) {
	for _, tc := range []struct{ options, want string }{
		{`{"reasoning_summary_delivery":"sequential_cutoff"}`, `{"reasoning_summary_delivery":"sequential_cutoff"}`},
		{`{"reasoning_summary_delivery":"sequential_cutoff","include_usage":true,"future_option":false}`, `{"reasoning_summary_delivery":"sequential_cutoff","future_option":false}`},
		{`{"include_usage":true}`, ``},
		{`{"include_obfuscation":false}`, `{"include_obfuscation":false}`},
		{`null`, `null`},
	} {
		raw := []byte(`{"model":"gpt-6-astra","input":[],"stream_options":` + tc.options + `}`)
		var mapped map[string]any
		require.NoError(t, json.Unmarshal(raw, &mapped))
		normalizeOpenAIOAuthResponsesCompatibilityFields(mapped)
		mapBody, err := json.Marshal(mapped)
		require.NoError(t, err)
		wsBody, _, err := normalizeOpenAIResponsesWebSocketCompatibilityBody(raw, &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, false)
		require.NoError(t, err)
		for _, got := range [][]byte{mapBody, wsBody} {
			field := gjson.GetBytes(got, "stream_options")
			if tc.want == "" {
				require.False(t, field.Exists())
			} else {
				require.JSONEq(t, tc.want, field.Raw)
			}
		}
	}
	body := []byte(`{"model":"gpt-6-astra","input":[]}`)
	got, _, err := normalizeOpenAIOAuthResponsesCompatibilityBody(body)
	require.NoError(t, err)
	require.Equal(t, body, got, "do not opt the client into concurrent summaries")
}
