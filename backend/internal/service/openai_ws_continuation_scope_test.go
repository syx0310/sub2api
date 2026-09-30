package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type scopeCountingWSDialer struct {
	codexLengthWSDialer
	dials atomic.Int32
}

func (d *scopeCountingWSDialer) Dial(ctx context.Context, target string, headers http.Header, proxy string) (openAIWSClientConn, int, http.Header, error) {
	d.dials.Add(1)
	return d.codexLengthWSDialer.Dial(ctx, target, headers, proxy)
}

func TestOpenAIWSContinuationScopeGuardsOnlyImplicitAnchor(t *testing.T) {
	for _, tc := range []struct {
		name, thread, window, kind, explicit, want string
	}{
		{"same_scope_infers", "parent", "w1", "turn", "", "resp_previous"},
		{"new_window_does_not_infer", "parent", "w2", "turn", "", ""},
		{"child_does_not_infer", "child", "w1", "turn", "", ""},
		{"memory_does_not_infer", "parent", "w1", "memory", "", ""},
		{"compact_does_not_infer", "parent", "w1", "compaction", "", ""},
		{"new_window_preserves_explicit", "parent", "w2", "turn", "resp_explicit", "resp_explicit"},
		{"child_preserves_explicit", "child", "w1", "turn", "resp_explicit", "resp_explicit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(context.Canceled)
			upstream := newStagedPassthroughConn()
			cfg := passthroughLifecycleConfig()
			svc := newPassthroughLifecycleService(cfg, upstream)
			dialer := &scopeCountingWSDialer{codexLengthWSDialer: codexLengthWSDialer{conn: &codexLengthWSConn{upstream}}}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(dialer)
			defer pool.Close()
			svc.openaiWSPool = pool
			account := passthroughLifecycleAccount()
			account.Extra["openai_apikey_responses_websockets_v2_mode"] = OpenAIWSIngressModeCtxPool
			var audits atomic.Int32
			server, serverErr := startPassthroughHookRecordingServer(t, ctx, svc, account, &OpenAIWSIngressHooks{
				BeforeRequest: func(_ int, _ []byte, _ string) error { audits.Add(1); return nil },
			})
			defer server.Close()
			first := `{"type":"response.create","model":"gpt-6.1-sol","store":false,"input":[{"role":"user","content":"hi"}],"client_metadata":{"x-codex-window-id":"w1","x-codex-turn-metadata":"{\"thread_id\":\"parent\",\"request_kind\":\"turn\"}"}}`
			client := dialPassthroughLifecycleClientWithPayload(t, server, first)
			defer func() { _ = client.CloseNow() }()
			requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
			upstream.Send(`{"type":"response.completed","response":{"id":"resp_previous","output":[{"type":"function_call","name":"test","call_id":"call_a","arguments":"{}"}],"usage":{"input_tokens":1,"output_tokens":1}}}`)
			_, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
			require.NoError(t, err)
			second := map[string]any{
				"type": "response.create", "model": "gpt-6.1-sol", "store": false,
				"input": []any{map[string]any{"type": "function_call_output", "call_id": "call_a", "output": "ok"}},
				"client_metadata": map[string]any{
					"x-codex-window-id":     tc.window,
					"x-codex-turn-metadata": fmt.Sprintf(`{"thread_id":%q,"request_kind":%q}`, tc.thread, tc.kind),
				},
			}
			if tc.explicit != "" {
				second["previous_response_id"] = tc.explicit
			}
			payload, err := json.Marshal(second)
			require.NoError(t, err)
			writeCtx, cancelWrite := context.WithTimeout(ctx, 3*time.Second)
			err = client.Write(writeCtx, coderws.MessageText, payload)
			cancelWrite()
			require.NoError(t, err)
			out := requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
			require.Equal(t, tc.want, gjson.GetBytes(out, "previous_response_id").String())
			require.Len(t, gjson.GetBytes(out, "input").Array(), 1, "do not manufacture replay history")
			require.Equal(t, "function_call_output", gjson.GetBytes(out, "input.0.type").String())
			require.Equal(t, int32(1), audits.Load(), "the subsequent request must be audited once")
			upstream.Send(`{"type":"response.completed","response":{"id":"resp_second","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`)
			_, err = readPassthroughLifecycleFrame(t, client, 3*time.Second)
			require.NoError(t, err)
			_ = client.Close(coderws.StatusNormalClosure, "done")
			select {
			case <-serverErr:
			case <-time.After(3 * time.Second):
				t.Fatal("websocket did not finish")
			}
			require.Equal(t, int32(1), dialer.dials.Load(), "metadata changes must not force a reconnect")
		})
	}
}
