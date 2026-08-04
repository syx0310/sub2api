package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"testing"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestIsOpenAIWSClientDisconnectError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "io_eof", err: io.EOF, want: true},
		{name: "net_closed", err: net.ErrClosed, want: true},
		{name: "context_canceled", err: context.Canceled, want: true},
		{name: "ws_normal_closure", err: coderws.CloseError{Code: coderws.StatusNormalClosure}, want: true},
		{name: "ws_going_away", err: coderws.CloseError{Code: coderws.StatusGoingAway}, want: true},
		{name: "ws_no_status", err: coderws.CloseError{Code: coderws.StatusNoStatusRcvd}, want: true},
		{name: "ws_abnormal_1006", err: coderws.CloseError{Code: coderws.StatusAbnormalClosure}, want: true},
		{name: "ws_policy_violation", err: coderws.CloseError{Code: coderws.StatusPolicyViolation}, want: false},
		{name: "wrapped_eof_message", err: errors.New("failed to get reader: failed to read frame header: EOF"), want: true},
		{name: "connection_reset_by_peer", err: errors.New("failed to read frame header: read tcp 127.0.0.1:1234->127.0.0.1:5678: read: connection reset by peer"), want: true},
		{name: "windows_connection_reset", err: errors.New("failed to get reader: failed to read frame header: read tcp 127.0.0.1:1234->127.0.0.1:5678: wsarecv: An existing connection was forcibly closed by the remote host."), want: true},
		{name: "broken_pipe", err: errors.New("write tcp 127.0.0.1:1234->127.0.0.1:5678: write: broken pipe"), want: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, isOpenAIWSClientDisconnectError(tt.err))
		})
	}
}

func TestShouldReportOpenAIWSAccountScheduleFailure(t *testing.T) {
	t.Parallel()

	localPolicy := NewOpenAIWSClientCloseError(
		coderws.StatusPolicyViolation,
		"request rejected by local policy",
		errors.New("local policy rejection"),
	)
	require.False(t, ShouldReportOpenAIWSAccountScheduleFailure(localPolicy))

	clientWrite := wrapOpenAIWSIngressTurnError(
		"write_client",
		errors.New("write tcp: broken pipe"),
		true,
	)
	require.True(t, IsOpenAIWSClientDeliveryError(clientWrite))
	require.False(t, ShouldReportOpenAIWSAccountScheduleFailure(clientWrite))

	upstreamTimeout := NewOpenAIWSClientCloseError(
		coderws.StatusTryAgainLater,
		"upstream websocket connect timeout",
		context.DeadlineExceeded,
	)
	require.False(t, IsOpenAIWSClientDeliveryError(upstreamTimeout))
	require.True(t, ShouldReportOpenAIWSAccountScheduleFailure(upstreamTimeout))

	upstreamAuth := NewOpenAIWSClientCloseError(
		coderws.StatusPolicyViolation,
		"upstream websocket authentication failed",
		errors.New("handshake status 401"),
	)
	require.True(t, ShouldReportOpenAIWSAccountScheduleFailure(upstreamAuth))
	require.True(t, ShouldReportOpenAIWSAccountScheduleFailure(errors.New("upstream websocket read failed")))

	previousResponseMissing := NewOpenAIWSClientCloseError(
		coderws.StatusTryAgainLater,
		"previous response not found; reconnect to resend the full request",
		wrapOpenAIWSIngressTurnError(
			openAIWSIngressStagePreviousResponseNotFound,
			errors.New("previous response not found"),
			true,
		),
	)
	require.False(t, ShouldReportOpenAIWSAccountScheduleFailure(previousResponseMissing))
}

func TestOpenAIWSPayloadToolReplaySelfContained(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		payload    string
		want       bool
		wantReason string
	}{
		{
			name:       "function_call_pair",
			payload:    `{"input":[{"type":"function_call","call_id":"call_1","name":"shell","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`,
			want:       true,
			wantReason: "self_contained",
		},
		{
			name:       "local_shell_call_matches_function_call_output",
			payload:    `{"input":[{"type":"local_shell_call","call_id":"call_1","action":{"type":"exec","command":"pwd"}},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`,
			want:       true,
			wantReason: "self_contained",
		},
		{
			name:       "tool_search_pair",
			payload:    `{"input":[{"type":"tool_search_call","call_id":"search_1","query":"x"},{"type":"tool_search_output","call_id":"search_1","output":"ok"}]}`,
			want:       true,
			wantReason: "self_contained",
		},
		{
			name:       "custom_tool_pair",
			payload:    `{"input":[{"type":"custom_tool_call","call_id":"custom_1","name":"x","input":"{}"},{"type":"custom_tool_call_output","call_id":"custom_1","output":"ok"}]}`,
			want:       true,
			wantReason: "self_contained",
		},
		{
			name:       "mcp_tool_pair",
			payload:    `{"input":[{"type":"mcp_tool_call","call_id":"mcp_1","name":"x","arguments":"{}"},{"type":"mcp_tool_call_output","call_id":"mcp_1","output":"ok"}]}`,
			want:       true,
			wantReason: "self_contained",
		},
		{
			name:       "output_before_call_is_not_self_contained",
			payload:    `{"input":[{"type":"function_call_output","call_id":"call_1","output":"ok"},{"type":"function_call","call_id":"call_1","name":"shell","arguments":"{}"}]}`,
			want:       false,
			wantReason: "missing_tool_call_context",
		},
		{
			name:       "output_missing_call_id",
			payload:    `{"input":[{"type":"function_call","call_id":"call_1","name":"shell","arguments":"{}"},{"type":"function_call_output","output":"ok"}]}`,
			want:       false,
			wantReason: "tool_output_missing_call_id",
		},
		{
			name:       "mismatched_tool_type",
			payload:    `{"input":[{"type":"function_call","call_id":"call_1","name":"shell","arguments":"{}"},{"type":"tool_search_output","call_id":"call_1","output":"ok"}]}`,
			want:       false,
			wantReason: "missing_tool_call_context",
		},
		{
			name:       "item_reference_is_not_context",
			payload:    `{"input":[{"type":"item_reference","id":"fc_1"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`,
			want:       false,
			wantReason: "missing_tool_call_context",
		},
		{
			name:       "parallel_call_missing_output",
			payload:    `{"input":[{"type":"function_call","call_id":"call_1","name":"a","arguments":"{}"},{"type":"function_call","call_id":"call_2","name":"b","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`,
			want:       false,
			wantReason: "missing_tool_output_for_call",
		},
		{
			name:       "no_tool_output",
			payload:    `{"input":[{"type":"input_text","text":"hello"}]}`,
			want:       false,
			wantReason: "missing_tool_output",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, reason := OpenAIWSPayloadToolReplaySelfContained([]byte(tt.payload))
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.wantReason, reason)
		})
	}
}

func TestStripCodexSparkImageGenerationToolFromRawPayload(t *testing.T) {
	t.Run("strips_image_generation_for_spark", func(t *testing.T) {
		payload := []byte(`{"type":"response.create","model":"gpt-5.3-codex-spark","tools":[{"type":"function","name":"shell"},{"type":"image_generation","output_format":"png"}]}`)
		updated, changed, err := stripCodexSparkImageGenerationToolFromRawPayload(payload, "gpt-5.3-codex-spark")
		require.NoError(t, err)
		require.True(t, changed)
		require.False(t, gjson.GetBytes(updated, `tools.#(type=="image_generation")`).Exists())
		require.True(t, gjson.GetBytes(updated, `tools.#(type=="function")`).Exists())
	})

	t.Run("strips_namespace_tools_for_spark", func(t *testing.T) {
		payload := []byte(`{
			"type":"response.create",
			"model":"gpt-5.3-codex-spark",
			"input":[
				{"type":"message","role":"user","content":"hello"},
				{"type":"additional_tools","tools":[{"type":"namespace","name":"image_gen"}]}
			],
			"tool_choice":{"type":"namespace","name":"image_gen"}
		}`)
		updated, changed, err := stripCodexSparkImageGenerationToolFromRawPayload(payload, "gpt-5.3-codex-spark")
		require.NoError(t, err)
		require.True(t, changed)
		require.False(t, IsImageGenerationIntent(openAIResponsesEndpoint, "gpt-5.3-codex-spark", updated))
		require.Equal(t, "hello", gjson.GetBytes(updated, "input.0.content").String())
		require.False(t, gjson.GetBytes(updated, "tool_choice").Exists())
	})

	t.Run("keeps_image_generation_for_non_spark", func(t *testing.T) {
		payload := []byte(`{"type":"response.create","model":"gpt-5.3-codex","tools":[{"type":"image_generation","output_format":"png"}]}`)
		updated, changed, err := stripCodexSparkImageGenerationToolFromRawPayload(payload, "gpt-5.3-codex")
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, string(payload), string(updated))
	})

	t.Run("noop_when_no_image_tool", func(t *testing.T) {
		payload := []byte(`{"type":"response.create","model":"gpt-5.3-codex-spark","tools":[{"type":"function","name":"shell"}]}`)
		updated, changed, err := stripCodexSparkImageGenerationToolFromRawPayload(payload, "gpt-5.3-codex-spark")
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, string(payload), string(updated))
	})
}

func TestStripOpenAIImageGenerationToolsFromRawPayload(t *testing.T) {
	t.Run("flat image tool", func(t *testing.T) {
		payload := []byte(`{
			"type":"response.create",
			"model":"gpt-5.4",
			"tools":[
				{"type":"function","name":"shell"},
				{"type":"image_generation","output_format":"png"}
			],
			"tool_choice":{"type":"image_generation"}
		}`)

		updated, changed, err := stripOpenAIImageGenerationToolsFromRawPayload(payload)

		require.NoError(t, err)
		require.True(t, changed)
		require.False(t, gjson.GetBytes(updated, `tools.#(type=="image_generation")`).Exists())
		require.True(t, gjson.GetBytes(updated, `tools.#(type=="function")`).Exists())
		require.False(t, gjson.GetBytes(updated, "tool_choice").Exists())
	})

	t.Run("namespace and Responses Lite tools", func(t *testing.T) {
		payload := []byte(`{
			"type":"response.create",
			"model":"gpt-5.5",
			"tools":[
				{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]},
				{"type":"namespace","name":"code_tools","tools":[{"type":"function","name":"run"}]}
			],
			"input":[
				{"type":"message","role":"user","content":"hello"},
				{"type":"additional_tools","tools":[{"type":"namespace","name":"image_gen"}]}
			],
			"tool_choice":{"type":"namespace","name":"image_gen"}
		}`)

		updated, changed, err := stripOpenAIImageGenerationToolsFromRawPayload(payload)

		require.NoError(t, err)
		require.True(t, changed)
		require.False(t, IsImageGenerationIntent(openAIResponsesEndpoint, "gpt-5.5", updated))
		require.True(t, gjson.GetBytes(updated, `tools.#(name=="code_tools")`).Exists())
		require.Equal(t, "hello", gjson.GetBytes(updated, "input.0.content").String())
		require.False(t, gjson.GetBytes(updated, "tool_choice").Exists())
	})

	t.Run("non-image namespace is unchanged", func(t *testing.T) {
		payload := []byte(`{"type":"response.create","model":"gpt-5.5","tools":[{"type":"namespace","name":"code_tools"}]}`)

		updated, changed, err := stripOpenAIImageGenerationToolsFromRawPayload(payload)

		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, payload, updated)
	})
}

func TestSetPreviousResponseIDToRawPayload(t *testing.T) {
	t.Parallel()

	t.Run("empty_payload", func(t *testing.T) {
		updated, err := setPreviousResponseIDToRawPayload(nil, "resp_target")
		require.NoError(t, err)
		require.Empty(t, updated)
	})

	t.Run("empty_previous_response_id", func(t *testing.T) {
		payload := []byte(`{"type":"response.create","model":"gpt-5.1"}`)
		updated, err := setPreviousResponseIDToRawPayload(payload, "")
		require.NoError(t, err)
		require.Equal(t, string(payload), string(updated))
	})

	t.Run("set_previous_response_id_when_missing", func(t *testing.T) {
		payload := []byte(`{"type":"response.create","model":"gpt-5.1"}`)
		updated, err := setPreviousResponseIDToRawPayload(payload, "resp_target")
		require.NoError(t, err)
		require.Equal(t, "resp_target", gjson.GetBytes(updated, "previous_response_id").String())
		require.Equal(t, "gpt-5.1", gjson.GetBytes(updated, "model").String())
	})

	t.Run("overwrite_existing_previous_response_id", func(t *testing.T) {
		payload := []byte(`{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_old"}`)
		updated, err := setPreviousResponseIDToRawPayload(payload, "resp_new")
		require.NoError(t, err)
		require.Equal(t, "resp_new", gjson.GetBytes(updated, "previous_response_id").String())
	})
}

func TestShouldInferIngressFunctionCallOutputPreviousResponseID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                    string
		storeDisabled           bool
		turn                    int
		signals                 ToolContinuationSignals
		currentPreviousResponse string
		expectedPrevious        string
		want                    bool
	}{
		{
			name:             "infer_when_all_conditions_match",
			storeDisabled:    true,
			turn:             2,
			signals:          ToolContinuationSignals{HasFunctionCallOutput: true},
			expectedPrevious: "resp_1",
			want:             true,
		},
		{
			name:             "skip_when_store_enabled",
			storeDisabled:    false,
			turn:             2,
			signals:          ToolContinuationSignals{HasFunctionCallOutput: true},
			expectedPrevious: "resp_1",
			want:             false,
		},
		{
			name:             "skip_on_first_turn",
			storeDisabled:    true,
			turn:             1,
			signals:          ToolContinuationSignals{HasFunctionCallOutput: true},
			expectedPrevious: "resp_1",
			want:             false,
		},
		{
			name:             "skip_without_function_call_output",
			storeDisabled:    true,
			turn:             2,
			signals:          ToolContinuationSignals{},
			expectedPrevious: "resp_1",
			want:             false,
		},
		{
			name:                    "skip_when_request_already_has_previous_response_id",
			storeDisabled:           true,
			turn:                    2,
			signals:                 ToolContinuationSignals{HasFunctionCallOutput: true},
			currentPreviousResponse: "resp_client",
			expectedPrevious:        "resp_1",
			want:                    false,
		},
		{
			name:             "skip_when_last_turn_response_id_missing",
			storeDisabled:    true,
			turn:             2,
			signals:          ToolContinuationSignals{HasFunctionCallOutput: true},
			expectedPrevious: "",
			want:             false,
		},
		{
			name:             "trim_whitespace_before_judgement",
			storeDisabled:    true,
			turn:             2,
			signals:          ToolContinuationSignals{HasFunctionCallOutput: true},
			expectedPrevious: "   resp_2   ",
			want:             true,
		},
		{
			name:             "skip_when_tool_call_context_already_present",
			storeDisabled:    true,
			turn:             2,
			signals:          ToolContinuationSignals{HasFunctionCallOutput: true, HasToolCallContext: true},
			expectedPrevious: "resp_2",
			want:             false,
		},
		{
			name:             "infer_when_only_item_reference_covers_call_ids",
			storeDisabled:    true,
			turn:             2,
			signals:          ToolContinuationSignals{HasFunctionCallOutput: true, HasItemReferenceForAllCallIDs: true},
			expectedPrevious: "resp_2",
			want:             true,
		},
		{
			name:             "skip_when_function_call_output_missing_call_id",
			storeDisabled:    true,
			turn:             2,
			signals:          ToolContinuationSignals{HasFunctionCallOutput: true, HasFunctionCallOutputMissingCallID: true},
			expectedPrevious: "resp_2",
			want:             false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := shouldInferIngressFunctionCallOutputPreviousResponseID(
				tt.storeDisabled,
				tt.turn,
				tt.signals,
				tt.currentPreviousResponse,
				tt.expectedPrevious,
			)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeOpenAIWSJSONForCompare(t *testing.T) {
	t.Parallel()

	normalized, err := normalizeOpenAIWSJSONForCompare([]byte(`{"b":2,"a":1}`))
	require.NoError(t, err)
	require.Equal(t, `{"a":1,"b":2}`, string(normalized))

	_, err = normalizeOpenAIWSJSONForCompare([]byte("   "))
	require.Error(t, err)

	_, err = normalizeOpenAIWSJSONForCompare([]byte(`{"a":`))
	require.Error(t, err)
}

func TestNormalizeOpenAIWSJSONForCompareOrRaw(t *testing.T) {
	t.Parallel()

	require.Equal(t, `{"a":1,"b":2}`, string(normalizeOpenAIWSJSONForCompareOrRaw([]byte(`{"b":2,"a":1}`))))
	require.Equal(t, `{"a":`, string(normalizeOpenAIWSJSONForCompareOrRaw([]byte(`{"a":`))))
}

func TestOpenAIWSExtractNormalizedInputSequence(t *testing.T) {
	t.Parallel()

	t.Run("empty_payload", func(t *testing.T) {
		items, exists, err := openAIWSExtractNormalizedInputSequence(nil)
		require.NoError(t, err)
		require.False(t, exists)
		require.Nil(t, items)
	})

	t.Run("input_missing", func(t *testing.T) {
		items, exists, err := openAIWSExtractNormalizedInputSequence([]byte(`{"type":"response.create"}`))
		require.NoError(t, err)
		require.False(t, exists)
		require.Nil(t, items)
	})

	t.Run("input_array", func(t *testing.T) {
		items, exists, err := openAIWSExtractNormalizedInputSequence([]byte(`{"input":[{"type":"input_text","text":"hello"}]}`))
		require.NoError(t, err)
		require.True(t, exists)
		require.Len(t, items, 1)
	})

	t.Run("input_object", func(t *testing.T) {
		items, exists, err := openAIWSExtractNormalizedInputSequence([]byte(`{"input":{"type":"input_text","text":"hello"}}`))
		require.NoError(t, err)
		require.True(t, exists)
		require.Len(t, items, 1)
	})

	t.Run("input_string", func(t *testing.T) {
		items, exists, err := openAIWSExtractNormalizedInputSequence([]byte(`{"input":"hello"}`))
		require.NoError(t, err)
		require.True(t, exists)
		require.Len(t, items, 1)
		require.Equal(t, `"hello"`, string(items[0]))
	})

	t.Run("input_number", func(t *testing.T) {
		items, exists, err := openAIWSExtractNormalizedInputSequence([]byte(`{"input":42}`))
		require.NoError(t, err)
		require.True(t, exists)
		require.Len(t, items, 1)
		require.Equal(t, "42", string(items[0]))
	})

	t.Run("input_bool", func(t *testing.T) {
		items, exists, err := openAIWSExtractNormalizedInputSequence([]byte(`{"input":true}`))
		require.NoError(t, err)
		require.True(t, exists)
		require.Len(t, items, 1)
		require.Equal(t, "true", string(items[0]))
	})

	t.Run("input_null", func(t *testing.T) {
		items, exists, err := openAIWSExtractNormalizedInputSequence([]byte(`{"input":null}`))
		require.NoError(t, err)
		require.True(t, exists)
		require.Len(t, items, 1)
		require.Equal(t, "null", string(items[0]))
	})

	t.Run("input_invalid_array_json", func(t *testing.T) {
		items, exists, err := openAIWSExtractNormalizedInputSequence([]byte(`{"input":[}`))
		require.Error(t, err)
		require.True(t, exists)
		require.Nil(t, items)
	})
}

func TestBuildOpenAIWSReplayInputSequence(t *testing.T) {
	t.Parallel()

	lastFull := []json.RawMessage{
		json.RawMessage(`{"type":"input_text","text":"hello"}`),
	}

	t.Run("no_previous_response_id_use_current", func(t *testing.T) {
		items, exists, err := buildOpenAIWSReplayInputSequence(
			lastFull,
			true,
			[]byte(`{"input":[{"type":"input_text","text":"new"}]}`),
			false,
		)
		require.NoError(t, err)
		require.True(t, exists)
		require.Len(t, items, 1)
		require.Equal(t, "new", gjson.GetBytes(items[0], "text").String())
	})

	t.Run("previous_response_id_delta_append", func(t *testing.T) {
		items, exists, err := buildOpenAIWSReplayInputSequence(
			lastFull,
			true,
			[]byte(`{"previous_response_id":"resp_1","input":[{"type":"input_text","text":"world"}]}`),
			true,
		)
		require.NoError(t, err)
		require.True(t, exists)
		require.Len(t, items, 2)
		require.Equal(t, "hello", gjson.GetBytes(items[0], "text").String())
		require.Equal(t, "world", gjson.GetBytes(items[1], "text").String())
	})

	t.Run("previous_response_id_full_input_replace", func(t *testing.T) {
		items, exists, err := buildOpenAIWSReplayInputSequence(
			lastFull,
			true,
			[]byte(`{"previous_response_id":"resp_1","input":[{"type":"input_text","text":"hello"},{"type":"input_text","text":"world"}]}`),
			true,
		)
		require.NoError(t, err)
		require.True(t, exists)
		require.Len(t, items, 2)
		require.Equal(t, "hello", gjson.GetBytes(items[0], "text").String())
		require.Equal(t, "world", gjson.GetBytes(items[1], "text").String())
	})
}

func TestOpenAIWSRawPayloadHasToolCallOutput(t *testing.T) {
	t.Parallel()

	for _, typ := range []string{
		"function_call_output",
		"tool_search_output",
		"custom_tool_call_output",
		"mcp_tool_call_output",
	} {
		typ := typ
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			payload := []byte(`{"input":[{"type":"` + typ + `","call_id":"call_1","output":"ok"}]}`)
			require.True(t, openAIWSRawPayloadHasToolCallOutput(payload))
		})
	}

	t.Run("object_input", func(t *testing.T) {
		t.Parallel()
		payload := []byte(`{"input":{"type":"tool_search_output","call_id":"call_1","output":"ok"}}`)
		require.True(t, openAIWSRawPayloadHasToolCallOutput(payload))
	})

	t.Run("non_tool_output", func(t *testing.T) {
		t.Parallel()
		payload := []byte(`{"input":[{"type":"input_text","text":"hello"}]}`)
		require.False(t, openAIWSRawPayloadHasToolCallOutput(payload))
	})
}

func TestSetOpenAIWSPayloadInputSequence(t *testing.T) {
	t.Parallel()

	t.Run("set_items", func(t *testing.T) {
		original := []byte(`{"type":"response.create","previous_response_id":"resp_1"}`)
		items := []json.RawMessage{
			json.RawMessage(`{"type":"input_text","text":"hello"}`),
			json.RawMessage(`{"type":"input_text","text":"world"}`),
		}
		updated, err := setOpenAIWSPayloadInputSequence(original, items, true)
		require.NoError(t, err)
		require.Equal(t, "hello", gjson.GetBytes(updated, "input.0.text").String())
		require.Equal(t, "world", gjson.GetBytes(updated, "input.1.text").String())
	})

	t.Run("preserve_empty_array_not_null", func(t *testing.T) {
		original := []byte(`{"type":"response.create","previous_response_id":"resp_1"}`)
		updated, err := setOpenAIWSPayloadInputSequence(original, nil, true)
		require.NoError(t, err)
		require.True(t, gjson.GetBytes(updated, "input").IsArray())
		require.Len(t, gjson.GetBytes(updated, "input").Array(), 0)
		require.False(t, gjson.GetBytes(updated, "input").Type == gjson.Null)
	})
}

func TestCloneOpenAIWSRawMessages(t *testing.T) {
	t.Parallel()

	t.Run("nil_slice", func(t *testing.T) {
		cloned := cloneOpenAIWSRawMessages(nil)
		require.Nil(t, cloned)
	})

	t.Run("empty_slice", func(t *testing.T) {
		items := make([]json.RawMessage, 0)
		cloned := cloneOpenAIWSRawMessages(items)
		require.NotNil(t, cloned)
		require.Len(t, cloned, 0)
	})
}
