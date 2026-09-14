//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Both executors speak the real WS codec to a local upstream. Compare actual
// dispatched frames, not just helper output or a synthetic relay state machine.
func TestOpenAIWSClientSemanticsCtxPoolPassthroughParity(t *testing.T) {
	for _, lite := range []bool{false, true} {
		t.Run(fmt.Sprintf("lite=%v", lite), func(t *testing.T) { testOpenAIWSClientSemanticsParity(t, lite) })
	}
}

func testOpenAIWSClientSemanticsParity(t *testing.T, lite bool) {
	var reference [][]byte
	for _, mode := range []string{OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			t.Cleanup(cancel)
			frames := make(chan []byte, 8)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					t.Errorf("upstream accept: %v", err)
					return
				}
				defer conn.CloseNow()
				for turn := 1; ; turn++ {
					_, payload, err := conn.Read(ctx)
					if err != nil {
						return
					}
					frames <- payload
					id := fmt.Sprintf("resp_semantics_%d", turn)
					created := fmt.Sprintf(`{"type":"response.created","response":{"id":%q,"model":"gpt-6-astra","status":"in_progress"}}`, id)
					if err := conn.Write(ctx, coderws.MessageText, []byte(created)); err != nil {
						return
					}
					output := `[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]`
					if turn == 1 {
						output = `[]`
					}
					if turn == 5 {
						output = `[{"type":"compaction","encrypted_content":"opaque-compacted-state"}]`
						if err := conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"opaque-compacted-state"}}`)); err != nil {
							return
						}
					}
					completed := fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"model":"gpt-6-astra","status":"completed","output":%s,"usage":{"input_tokens":2,"output_tokens":1}}}`, id, output)
					if err := conn.Write(ctx, coderws.MessageText, []byte(completed)); err != nil {
						return
					}
				}
			}))
			t.Cleanup(upstream.Close)
			cfg := openAIWSThreadTestConfig()
			cfg.Gateway.OpenAIWS.IngressModeDefault = mode
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 2
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 2
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 5
			cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 5
			dialer := &openAIWSThreadLocalDialer{target: "ws" + strings.TrimPrefix(upstream.URL, "http"), inner: newDefaultOpenAIWSClientDialer()}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(dialer)
			t.Cleanup(pool.Close)
			svc := &OpenAIGatewayService{cfg: cfg, openaiWSPool: pool, openaiWSPassthroughDialer: dialer, toolCorrector: NewCodexToolCorrector()}
			account := &Account{
				ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 2,
				Credentials: map[string]any{"access_token": "test-token", "chatgpt_account_id": "test-account"},
				Extra:       prepareCodexFingerprintExtraForCreate(PlatformOpenAI, AccountTypeOAuth, map[string]any{"codex_fingerprint_mode": "device"}),
			}
			results := make(chan *OpenAIForwardResult, 8)
			serverErrors := make(chan error, 1)
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					serverErrors <- err
					return
				}
				defer conn.CloseNow()
				_, first, err := conn.Read(ctx)
				if err != nil {
					serverErrors <- err
					return
				}
				c := newOpenAIWSThreadTestContext(7, 11, "root-session", "parent")
				c.Request = r.Clone(r.Context())
				hooks := &OpenAIWSIngressHooks{
					MaxReasoningEffort: "high",
					MapRequestModel: func(_ int, model string) (string, error) {
						if model != "client-astra-alias" {
							return "", fmt.Errorf("lost client model: %q", model)
						}
						return "gpt-6-astra", nil
					},
					AfterTurn: func(_ int, result *OpenAIForwardResult, err error) {
						if err != nil {
							t.Errorf("turn failed: %v", err)
						}
						results <- result
					},
				}
				serverErrors <- svc.ProxyResponsesWebSocketFromClient(r.Context(), c, conn, account, "test-token", first, hooks)
			}))
			t.Cleanup(gateway.Close)
			headers := http.Header{
				"Session-Id": {"root-session"}, "Thread-Id": {"parent"},
				openAIWSTurnMetadataHeader: {`{"turn_id":"stale","request_kind":"prewarm","removed_extra":"old"}`},
			}
			client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(gateway.URL, "http"), &coderws.DialOptions{HTTPHeader: headers})
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.CloseNow() })
			inputs := []string{
				`[{"role":"user","content":"hello"}]`, `[]`,
				`[{"role":"user","content":"think harder"},{"type":"configuration_update","reasoning":{"effort":"xhigh"}}]`,
				`[{"type":"function_call_output","name":"external_input","output":"worker finished"}]`,
				`[{"type":"compaction_trigger"}]`,
				`[{"role":"user","content":"after compaction"}]`,
				`[{"role":"user","content":"memory"}]`,
			}
			var captured [][]byte
			for i, input := range inputs {
				turn := i + 1
				body := map[string]any{
					"type": "response.create", "model": "client-astra-alias", "input": json.RawMessage(input), "store": false,
					"reasoning":        map[string]any{"effort": "low", "mode": "auto", "summary": "auto"},
					"prompt_cache_key": "root-session",
				}
				if turn == 1 {
					body["generate"] = false
				} else {
					body["previous_response_id"] = fmt.Sprintf("resp_semantics_%d", turn-1)
					delete(body, "model") // exercise session model inheritance too
				}
				kind := "turn"
				if turn == 1 {
					kind = "prewarm"
				}
				if turn == 5 {
					kind = "compaction"
				}
				if turn == 7 {
					kind = "memory"
				}
				cm := map[string]any{"session_id": "root-session", "thread_id": "parent", "turn_id": fmt.Sprintf("T%d", turn), "traceparent": "opaque-trace"}
				if lite {
					cm["ws_request_header_x_openai_internal_codex_responses_lite"] = "true"
				}
				if turn != 6 {
					cm[openAIWSTurnMetadataHeader] = fmt.Sprintf(`{"turn_id":"T%d","window_id":"W%d","request_kind":%q,"analytics_enabled":false,"custom_extra":"new","tool_namespaces_info":{"tools":{"functions":{"run":{"direct":false,"deferred":true,"source":{"kind":"mcp","server_name":"example"}}}}}}`, turn, turn, kind)
					body["stream_options"] = map[string]any{"reasoning_summary_delivery": "sequential_cutoff", "include_usage": true, "future_option": false}
				}
				body["client_metadata"] = cm
				raw, err := json.Marshal(body)
				require.NoError(t, err)
				require.NoError(t, client.Write(ctx, coderws.MessageText, raw))
				expectedEvents := []string{"response.created", "response.completed"}
				if turn == 5 {
					expectedEvents = []string{"response.created", "response.output_item.done", "response.completed"}
				}
				for _, event := range expectedEvents {
					_, frame, err := client.Read(ctx)
					require.NoError(t, err)
					require.Equal(t, event, gjson.GetBytes(frame, "type").String())
				}
				select {
				case result := <-results:
					require.NotNil(t, result)
					require.Equal(t, 2, result.Usage.InputTokens)
					require.Equal(t, 1, result.Usage.OutputTokens, "even prewarm retains actual usage")
					wantEffort, wantRequested := "low", "low"
					if turn >= 3 && turn <= 5 {
						wantEffort, wantRequested = "high", "xhigh"
					}
					require.Equal(t, &wantEffort, result.ReasoningEffort)
					require.Equal(t, &wantRequested, result.RequestedReasoningEffort)
					if turn == 1 {
						require.Nil(t, result.FirstTokenMs)
					}
				case <-ctx.Done():
					t.Fatal("missing accounting result")
				}
				select {
				case frame := <-frames:
					captured = append(captured, frame)
					require.Equal(t, "gpt-6-astra", gjson.GetBytes(frame, "model").String())
					require.Equal(t, "auto", gjson.GetBytes(frame, "reasoning.mode").String())
					require.Equal(t, "low", gjson.GetBytes(frame, "reasoning.effort").String(), "never rewrite the pinned cache baseline")
					if turn == 1 {
						require.Equal(t, gjson.False, gjson.GetBytes(frame, "generate").Type)
					}
					if turn > 1 {
						require.Equal(t, body["previous_response_id"], gjson.GetBytes(frame, "previous_response_id").String())
					}
					if turn == 2 {
						require.Empty(t, gjson.GetBytes(frame, "input").Array(), "warmup permits an empty delta")
					}
					if turn == 3 {
						require.Equal(t, "high", gjson.GetBytes(frame, "input.1.reasoning.effort").String())
					}
					if turn == 4 {
						require.Equal(t, "function_call_output", gjson.GetBytes(frame, "input.0.type").String())
						require.Equal(t, "worker finished", gjson.GetBytes(frame, "input.0.output").String())
					}
					if turn == 5 {
						items := gjson.GetBytes(frame, "input").Array()
						require.NotEmpty(t, items)
						require.Equal(t, "compaction_trigger", items[len(items)-1].Get("type").String())
					}
					require.Equal(t, turn == 5, ClassifyOpenAIResponsesRequest(frame).NativeCompactionV2)
					metadata := gjson.GetBytes(frame, "client_metadata")
					if turn == 6 {
						require.False(t, metadata.Get(openAIWSTurnMetadataHeader).Exists(), "must not resurrect handshake metadata")
						require.False(t, gjson.GetBytes(frame, "stream_options").Exists(), "no implicit summary option")
					} else {
						inner := gjson.Parse(metadata.Get(openAIWSTurnMetadataHeader).String())
						require.Equal(t, kind, inner.Get("request_kind").String())
						require.Equal(t, metadata.Get("turn_id").String(), inner.Get("turn_id").String())
						require.True(t, inner.Get("tool_namespaces_info.tools.functions.run.source").Exists())
						require.Equal(t, "false", inner.Get("analytics_enabled").Raw)
						require.False(t, inner.Get("removed_extra").Exists())
						require.JSONEq(t, `{"reasoning_summary_delivery":"sequential_cutoff","future_option":false}`, gjson.GetBytes(frame, "stream_options").Raw)
					}
				case <-ctx.Done():
					t.Fatal("missing outbound frame")
				}
			}
			require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
			select {
			case err := <-serverErrors:
				require.NoError(t, err)
			case <-ctx.Done():
				t.Fatal("gateway did not release client")
			}
			require.Equal(t, int32(1), dialer.dials.Load(), "no forced Astra relay or reconnection")
			require.Empty(t, frames, "no duplicate replay")
			if reference == nil {
				reference = captured
			} else {
				require.Len(t, captured, len(reference))
				for i := range reference {
					require.JSONEq(t, string(reference[i]), string(captured[i]), "turn %d differs between transports", i+1)
				}
			}
		})
	}
}
