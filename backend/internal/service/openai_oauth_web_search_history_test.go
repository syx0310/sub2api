package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Codex local compaction replays hosted web_search_call history with tools:[];
// ChatGPT then fails with "response protection is unavailable" (#7927).
const openAIWebSearchHistoryCompactionBody = `{"model":"gpt-5.5","instructions":"x","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"look it up"}]},{"type":"web_search_call","id":"ws_123","status":"completed","action":{"type":"search","query":"kwin inputmethod"}},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"found"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"summarize"}]}],"tools":[],"tool_choice":"auto","parallel_tool_calls":false,"stream":true,"store":false}`

// Native v2 / Lite keeps old function namespaces in additional_tools, not
// necessarily the hosted tool that produced a web_search_call in old history.
const openAIWebSearchHistoryNativeCompactionBody = `{"model":"gpt-6.1-sol","instructions":"x","input":[{"type":"additional_tools","id":"at_original","role":"developer","tools":[{"type":"namespace","name":"functions","tools":[{"type":"function","name":"echo","parameters":{"type":"object","properties":{}}}]}]},{"type":"web_search_call","id":"ws_original","status":"completed","action":{"type":"search","query":"q"}},{"type":"compaction","id":"cmp_original","encrypted_content":"opaque-summary"},{"type":"compaction_trigger"}],"tool_choice":"auto","stream":true,"store":false}`

func TestOAuthWebSearchHistoryToolAcrossPaths(t *testing.T) {
	oauth := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	transform := func(compact bool) func([]byte) ([]byte, bool, error) {
		return func(b []byte) ([]byte, bool, error) {
			var req map[string]any
			if err := json.Unmarshal(b, &req); err != nil {
				return nil, false, err
			}
			result := applyCodexOAuthTransform(req, true, compact)
			out, err := json.Marshal(req)
			return out, result.Modified, err
		}
	}
	tests := []struct {
		name      string
		normalize func([]byte) ([]byte, bool, error)
		inject    bool
	}{
		{"transformed OAuth", transform(false), true},
		{"transformed OAuth compact", transform(true), false},
		{"OAuth websocket", func(b []byte) ([]byte, bool, error) {
			return normalizeOpenAIResponsesWebSocketCompatibilityBody(b, oauth, false)
		}, true},
		{"OAuth passthrough compact", func(b []byte) ([]byte, bool, error) {
			return normalizeOpenAIResponsesCompatibilityBodyWithOptions(b, oauth, openAIResponsesCompatibilityOptions{Compact: true})
		}, false},
		{"API key websocket", func(b []byte) ([]byte, bool, error) {
			return normalizeOpenAIResponsesWebSocketCompatibilityBody(b, &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, false)
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, _, err := tt.normalize([]byte(openAIWebSearchHistoryCompactionBody))
			require.NoError(t, err)
			require.Equal(t, "web_search_call", gjson.GetBytes(out, "input.1.type").String())
			if !tt.inject {
				require.False(t, gjsonToolsContainWebSearch(gjson.GetBytes(out, "tools")))
				return
			}
			tools := gjson.GetBytes(out, "tools").Array()
			require.Len(t, tools, 1)
			require.Equal(t, "web_search", tools[0].Get("type").String())
			require.False(t, tools[0].Get("external_web_access").Bool())
			require.True(t, tools[0].Get("external_web_access").Exists())
			require.Equal(t, "none", gjson.GetBytes(out, "tool_choice").String())
		})
	}
}

func TestEnsureOpenAIOAuthWebSearchToolForHistory(t *testing.T) {
	const (
		user    = `{"type":"message","role":"user","content":[{"type":"input_text","text":"q"}]}`
		search  = `{"type":"web_search_call","id":"ws_1","status":"completed","action":{"type":"search","query":"q"}}`
		trigger = `{"type":"compaction_trigger"}`
		history = `[` + user + `,` + search + `]`
	)
	tests := []struct {
		name       string
		body       string
		lite       bool
		changed    bool
		toolsPath  string
		toolCount  int
		toolChoice string
		itemTypes  []string
	}{
		{"tools absent", `{"input":` + history + `}`, false, true, "tools", 1, "none", nil},
		{"tools null", `{"input":` + history + `,"tools":null,"tool_choice":null}`, false, true, "tools", 1, "none", nil},
		{"no-tools prewarm keeps existing history compatibility", `{"generate":false,"input":` + history + `,"tool_choice":"auto"}`, false, true, "tools", 1, "none", nil},
		{"prewarm with explicit none keeps existing history compatibility", `{"generate":false,"input":` + history + `,"tools":[{"type":"function","name":"echo"}],"tool_choice":"none"}`, false, true, "tools", 2, "none", nil},
		{"explicit none kept", `{"input":` + history + `,"tools":[],"tool_choice":"none"}`, false, true, "tools", 1, "none", nil},
		{"required not rewritten or expanded", `{"input":` + history + `,"tools":[],"tool_choice":"required"}`, false, false, "", 0, "", nil},
		{"caller tools do not enable search", `{"input":` + history + `,"tools":[{"type":"function","name":"echo"}],"tool_choice":"auto"}`, false, false, "", 0, "", nil},
		{"explicit none with caller tools", `{"input":` + history + `,"tools":[{"type":"function","name":"echo"}],"tool_choice":"none"}`, false, true, "tools", 2, "none", nil},
		{"incremental tools cannot be inferred", `{"previous_response_id":"resp_parent","input":` + history + `}`, false, false, "", 0, "", nil},
		{"named choice remains exact", `{"input":` + history + `,"tools":[{"type":"function","name":"echo"}],"tool_choice":{"type":"function","name":"echo"}}`, false, false, "", 0, "", nil},
		{"web_search already declared", `{"input":` + history + `,"tools":[{"type":"web_search"}]}`, false, false, "", 0, "", nil},
		{"web_search_preview declared", `{"input":` + history + `,"tools":[{"type":"web_search_preview"}]}`, false, false, "", 0, "", nil},
		{"declared via additional_tools", `{"input":[{"type":"additional_tools","tools":[{"type":"web_search"}]},` + search + `]}`, false, false, "", 0, "", nil},
		{"no web_search_call item", `{"input":[{"type":"message","role":"user","content":"web_search_call"}],"tools":[]}`, false, false, "", 0, "", nil},
		{"string input", `{"input":"web_search_call"}`, false, false, "", 0, "", nil},
		{"lite inserts before compaction trigger", `{"input":[` + user + `,` + search + `,` + trigger + `]}`, true, true, "input.2.tools", 1, "none",
			[]string{"message", "web_search_call", "additional_tools", "compaction_trigger"}},
		{"lite appends without trigger", `{"input":` + history + `,"tool_choice":"auto"}`, true, true, "input.2.tools", 1, "none",
			[]string{"message", "web_search_call", "additional_tools"}},
		{"lite native compaction disables historical functions", `{"input":[{"type":"additional_tools","role":"developer","tools":[{"type":"function","name":"echo"}]},` + search + `,` + trigger + `],"tool_choice":"auto"}`, true, true, "input.2.tools", 1, "none",
			[]string{"additional_tools", "web_search_call", "additional_tools", "compaction_trigger"}},
		{"lite preserves typed historical declaration", `{"input":[{"id":"at_original","type":"additional_tools","role":"developer","tools":[{"type":"function","name":"echo"}]},` + search + `,` + trigger + `],"tool_choice":"none"}`, true, true, "input.2.tools", 1, "none",
			[]string{"additional_tools", "web_search_call", "additional_tools", "compaction_trigger"}},
		{"lite preserves empty additional_tools", `{"input":[{"type":"additional_tools","role":"developer"},` + search + `]}`, true, true, "input.2.tools", 1, "none",
			[]string{"additional_tools", "web_search_call", "additional_tools"}},
		{"lite keeps top-level tools untouched", `{"input":` + history + `,"tools":[{"type":"function","name":"echo"}]}`, true, false, "", 0, "", nil},
		{"local compaction disables historical functions", `{"input":[{"id":"at_original","type":"additional_tools","role":"developer","tools":[{"type":"function","name":"echo"}]},` + search + `],"tool_choice":"auto","client_metadata":{"x-codex-turn-metadata":"{\"request_kind\":\"compaction\"}"}}`, true, true, "input.2.tools", 1, "none",
			[]string{"additional_tools", "web_search_call", "additional_tools"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, changed, err := ensureOpenAIOAuthWebSearchToolForHistoryBody([]byte(tt.body), tt.lite)
			require.NoError(t, err)
			require.Equal(t, tt.changed, changed)
			var req map[string]any
			require.NoError(t, json.Unmarshal([]byte(tt.body), &req))
			require.Equal(t, tt.changed, ensureOpenAIOAuthWebSearchToolForHistory(req, tt.lite))
			if !tt.changed {
				require.JSONEq(t, tt.body, string(out))
				mapOut, marshalErr := json.Marshal(req)
				require.NoError(t, marshalErr)
				require.JSONEq(t, tt.body, string(mapOut))
				return
			}
			require.True(t, gjson.ValidBytes(out))
			tools := gjson.GetBytes(out, tt.toolsPath).Array()
			require.Len(t, tools, tt.toolCount)
			require.Equal(t, "web_search", tools[len(tools)-1].Get("type").String())
			require.Equal(t, tt.toolChoice, gjson.GetBytes(out, "tool_choice").String())
			if tt.lite {
				require.False(t, gjsonToolsContainWebSearch(gjson.GetBytes(out, "tools")), "Lite rejects top-level hosted tools")
				var types []string
				for _, item := range gjson.GetBytes(out, "input").Array() {
					types = append(types, item.Get("type").String())
				}
				require.Equal(t, tt.itemTypes, types)
				if gjson.Get(tt.body, "input.0.type").String() == "additional_tools" {
					require.JSONEq(t, gjson.Get(tt.body, "input.0").Raw, gjson.GetBytes(out, "input.0").Raw)
				}
			}

			// The map variant used by the Codex transform must agree.
			mapOut, err := json.Marshal(req)
			require.NoError(t, err)
			require.JSONEq(t, string(out), string(mapOut))
			again, changedAgain, err := ensureOpenAIOAuthWebSearchToolForHistoryBody(out, tt.lite)
			require.NoError(t, err)
			require.False(t, changedAgain)
			require.Equal(t, out, again)
		})
	}
}

func TestOAuthWebSearchHistoryNativeCompactionPermissions(t *testing.T) {
	for _, lite := range []bool{false, true} {
		for _, choice := range []string{"", `null`, `"auto"`, `"none"`, `"required"`, `{"type":"function","name":"echo"}`, `{"type":"allowed_tools","mode":"auto","tools":[{"type":"function","name":"echo"}]}`} {
			t.Run(fmt.Sprintf("lite=%v/choice=%s", lite, choice), func(t *testing.T) {
				body := []byte(openAIWebSearchHistoryNativeCompactionBody)
				var err error
				body, err = sjson.SetBytes(body, "previous_response_id", "resp_keep")
				require.NoError(t, err)
				body, err = sjson.SetRawBytes(body, "future_metadata", []byte(`{"ordinal":9007199254740993}`))
				require.NoError(t, err)
				if !lite {
					body, err = sjson.SetRawBytes(body, "tools", []byte(`[{"type":"function","name":"echo","parameters":{"type":"object","properties":{}}}]`))
					require.NoError(t, err)
				}
				if choice == "" {
					body, err = sjson.DeleteBytes(body, "tool_choice")
				} else {
					body, err = sjson.SetRawBytes(body, "tool_choice", []byte(choice))
				}
				require.NoError(t, err)
				wantChange := choice == "" || choice == "null" || choice == `"auto"` || choice == `"none"`
				out, changed, err := ensureOpenAIOAuthWebSearchToolForHistoryBody(body, lite)
				require.NoError(t, err)
				require.Equal(t, wantChange, changed)
				if wantChange {
					require.Equal(t, "none", gjson.GetBytes(out, "tool_choice").String())
					if lite {
						require.False(t, gjson.GetBytes(out, "tools").Exists())
						require.Equal(t, "web_search", gjson.GetBytes(out, "input.3.tools.0.type").String())
						require.Equal(t, "compaction_trigger", gjson.GetBytes(out, "input.4.type").String())
					} else {
						require.Equal(t, "web_search", gjson.GetBytes(out, "tools.1.type").String())
						require.Equal(t, "compaction_trigger", gjson.GetBytes(out, "input.3.type").String())
					}
				} else {
					require.Equal(t, body, out, "conflicting choices remain byte-for-byte unchanged")
				}
				for _, path := range []string{"input.0", "input.1", "input.2", "previous_response_id", "future_metadata", "tools.0"} {
					require.Equal(t, gjson.GetBytes(body, path).Raw, gjson.GetBytes(out, path).Raw, path)
				}
				var mapped map[string]any
				decoder := json.NewDecoder(strings.NewReader(string(body)))
				decoder.UseNumber()
				require.NoError(t, decoder.Decode(&mapped))
				require.Equal(t, wantChange, ensureOpenAIOAuthWebSearchToolForHistory(mapped, lite))
				mapOut, err := json.Marshal(mapped)
				require.NoError(t, err)
				require.Equal(t, "9007199254740993", gjson.GetBytes(mapOut, "future_metadata.ordinal").Raw)
				require.JSONEq(t, string(out), string(mapOut))
				again, changed, err := ensureOpenAIOAuthWebSearchToolForHistoryBody(out, lite)
				require.NoError(t, err)
				require.False(t, changed)
				require.Equal(t, out, again)
			})
		}
	}
}

func TestOAuthWebSearchHistoryDoesNotInheritCompactionIntent(t *testing.T) {
	const local = `{"input":[{"type":"additional_tools","role":"developer","tools":[{"type":"function","name":"echo"}]},{"type":"web_search_call","id":"ws_keep"}],"tool_choice":"auto"}`
	for _, tc := range []struct {
		name, field, value string
		native             bool
	}{
		{"native prewarm", "generate", `false`, true},
		{"native control", "type", `"response.steer"`, true},
		{"local prewarm", "generate", `false`, false},
		{"local control", "type", `"response.steer"`, false},
		{"body metadata is not an embedded string", "client_metadata.x-codex-turn-metadata", `{"request_kind":"compaction"}`, false},
		{"ordinary turn", "client_metadata.x-codex-turn-metadata", `"{\"request_kind\":\"turn\"}"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(local)
			if tc.native {
				body = []byte(openAIWebSearchHistoryNativeCompactionBody)
			}
			body, err := sjson.SetBytes(body, "client_metadata."+openAIWSTurnMetadataHeader, `{"request_kind":"compaction"}`)
			require.NoError(t, err)
			body, err = sjson.SetRawBytes(body, tc.field, []byte(tc.value))
			require.NoError(t, err)
			out, changed, err := ensureOpenAIOAuthWebSearchToolForHistoryBody(body, true)
			require.NoError(t, err)
			require.False(t, changed)
			require.Equal(t, body, out)
			var mapped map[string]any
			require.NoError(t, json.Unmarshal(body, &mapped))
			require.False(t, ensureOpenAIOAuthWebSearchToolForHistory(mapped, true))
		})
	}
}

func TestOAuthWebSearchNativeCompactionExcludedPaths(t *testing.T) {
	for _, lite := range []bool{false, true} {
		for _, accountType := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
			t.Run(fmt.Sprintf("lite=%v/account=%s", lite, accountType), func(t *testing.T) {
				body := []byte(openAIWebSearchHistoryNativeCompactionBody)
				account := &Account{Platform: PlatformOpenAI, Type: accountType}
				out, _, err := normalizeOpenAIResponsesCompatibilityBodyWithOptions(body, account,
					openAIResponsesCompatibilityOptions{ResponsesLite: lite, Compact: accountType == AccountTypeOAuth})
				require.NoError(t, err)
				require.False(t, gjsonToolsContainWebSearch(gjson.GetBytes(out, "tools")))
				require.Len(t, gjson.GetBytes(out, "input").Array(), 4)
				require.Equal(t, "auto", gjson.GetBytes(out, "tool_choice").String())
				if accountType == AccountTypeOAuth {
					var mapped map[string]any
					require.NoError(t, json.Unmarshal(body, &mapped))
					applyCodexOAuthTransformWithOptions(mapped, codexOAuthTransformOptions{IsCodexCLI: true, IsCompact: true, ResponsesLite: lite})
					require.False(t, openAIToolsContainWebSearch(mapped["tools"]))
					require.Len(t, mapped["input"], 4)
					require.Equal(t, "auto", mapped["tool_choice"])
				}
			})
		}
	}
}

func TestEnsureOpenAIOAuthWebSearchToolForHistoryDoesNotShareToolMap(t *testing.T) {
	first := map[string]any{"input": []any{map[string]any{"type": "web_search_call"}}}
	second := map[string]any{"input": []any{map[string]any{"type": "web_search_call"}}}
	require.True(t, ensureOpenAIOAuthWebSearchToolForHistory(first, false))
	require.True(t, ensureOpenAIOAuthWebSearchToolForHistory(second, false))
	firstTools, ok := first["tools"].([]any)
	require.True(t, ok)
	firstTool, ok := firstTools[0].(map[string]any)
	require.True(t, ok)
	firstTool["external_web_access"] = true
	secondTools, ok := second["tools"].([]any)
	require.True(t, ok)
	secondTool, ok := secondTools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, false, secondTool["external_web_access"])
	require.Equal(t, false, openAIWebSearchHistoryTool["external_web_access"])
}
