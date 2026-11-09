package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type instructionsEchoStagedConn struct{ *stagedPassthroughConn }

func (c *instructionsEchoStagedConn) WriteJSON(ctx context.Context, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.WriteFrame(ctx, coderws.MessageText, body)
}

// Use real downstream sockets and the normal per-create lifecycle. ctx_pool
// deliberately appends its enabled image bridge instructions; passthrough does
// not. Neither path may echo another create's instructions or retain its body.
func TestWSInstructionsEchoIsPerCreate(t *testing.T) {
	for _, mode := range []string{OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			upstream := newStagedPassthroughConn()
			cfg := passthroughLifecycleConfig()
			cfg.Gateway.OpenAIWS.OAuthEnabled = true
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 5
			cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds = 5
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.ForceCodexCLI = true
			cfg.Gateway.CodexImageGenerationBridgeEnabled = true
			account := passthroughLifecycleAccount()
			account.Type = AccountTypeOAuth
			account.Credentials = map[string]any{"access_token": "test-token"}
			account.Extra = map[string]any{"openai_oauth_responses_websockets_v2_mode": mode}
			svc := newPassthroughLifecycleService(cfg, upstream)
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(&stagedPassthroughDialer{conn: &instructionsEchoStagedConn{upstream}})
			svc.openaiWSPool = pool
			defer pool.Close()
			results := make(chan *OpenAIForwardResult, 4)
			server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(*gin.Context) *OpenAIWSIngressHooks {
				return &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, err error) {
					if err == nil && result != nil {
						results <- result
					}
				}}
			})
			defer server.Close()
			bodyFor := func(raw string) string {
				field := ""
				if raw != "" {
					field = `,"instructions":` + raw
				}
				return `{"type":"response.create","model":"gpt-5.1","input":[{"type":"message","role":"user","content":"hello"}]` + field + `}`
			}
			client := dialPassthroughLifecycleClientWithPayload(t, server, bodyFor(`"first"`))
			defer client.CloseNow()
			for i, raw := range []string{`"first"`, `"second\nline"`, "", `""`} {
				if i > 0 {
					require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(bodyFor(raw))))
				}
				request := requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
				echo := gjson.GetBytes(request, "instructions").Raw
				if mode == OpenAIWSIngressModeCtxPool {
					require.Contains(t, gjson.GetBytes(request, "instructions").String(), codexImageGenerationBridgeText)
				} else {
					require.Equal(t, raw, echo)
				}
				if echo == "" {
					echo = "null"
				}
				upstream.Send(fmt.Sprintf(`{"type":"response.created","response":{"id":"resp_echo_%d","model":"gpt-5.1","instructions":%s}}`, i, echo))
				created, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
				require.NoError(t, err)
				upstream.Send(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_echo_%d","model":"gpt-5.1","instructions":%s,"usage":{"input_tokens":1,"output_tokens":1}}}`, i, echo))
				message, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
				require.NoError(t, err)
				want := raw
				if want == "" {
					want = "null"
				}
				require.JSONEq(t, want, gjson.GetBytes(message, "response.instructions").Raw)
				require.JSONEq(t, want, gjson.GetBytes(created, "response.instructions").Raw)
				select {
				case result := <-results:
					require.EqualValues(t, len(created)+len(message), *result.ResponseBodyBytes)
				case <-time.After(3 * time.Second):
					t.Fatal("turn accounting did not complete")
				}
			}
			_ = client.CloseNow()
			cancel()
			select {
			case <-serverErr:
			case <-time.After(3 * time.Second):
				t.Fatal("relay did not stop")
			}
		})
	}
}

func TestWSHTTPBridgeIdentityUsesCurrentCanonicalBody(t *testing.T) {
	account := newTestOAuthAccount(7103, nil) // device, not session/full
	upstream := instructionsEchoUpstream(`null`)
	svc := &OpenAIGatewayService{cfg: passthroughLifecycleConfig(), httpUpstream: upstream}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	c.Request.Header.Set(openAIWSTurnMetadataHeader, `{"thread_id":"old","request_kind":"compaction"}`)
	c.Request.Header.Set("thread-id", "old")
	c.Request.Header.Set("x-client-request-id", "old")
	for turn, thread := range []string{"child-a", "child-b", ""} {
		upstream.resp = instructionsEchoUpstream(`null`).resp
		metadata := map[string]any{}
		if thread != "" {
			embedded, err := json.Marshal(map[string]any{"thread_id": thread, "session_id": "root", "turn_id": "turn-" + thread, "request_kind": "turn"})
			require.NoError(t, err)
			metadata = map[string]any{openAIWSTurnMetadataHeader: string(embedded), "thread_id": "stale-projection"}
		}
		body, err := json.Marshal(map[string]any{"type": "response.create", "model": "gpt-5.1", "input": "hi", "client_metadata": metadata})
		require.NoError(t, err)
		_, err = svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, account, "test-token", body, int64(len(body)), len(body), "gpt-5.1", "", "", "", "", turn+1, func([]byte) error { return nil })
		require.NoError(t, err)
		headers := upstream.lastReq.Header
		require.Equal(t, thread, headers.Get("thread-id"))
		require.Equal(t, thread, headers.Get("x-client-request-id"))
		require.NotContains(t, headers.Get(openAIWSTurnMetadataHeader), "compaction")
		require.NotEmpty(t, headers.Get("x-codex-installation-id"))
		require.False(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").Exists())
		if thread != "" {
			require.Equal(t, "root", headers.Get("session_id"))
			require.Equal(t, "turn-"+thread, gjson.Get(headers.Get(openAIWSTurnMetadataHeader), "turn_id").String())
		} else {
			require.Empty(t, headers.Get(openAIWSTurnMetadataHeader))
			require.Empty(t, headers.Get("session_id"))
		}
	}
}

func TestWSHTTPBridgeInstructionsEchoCountsDeliveredBytes(t *testing.T) {
	upstream := instructionsEchoUpstream(`"injected"`)
	svc := &OpenAIGatewayService{cfg: passthroughLifecycleConfig(), httpUpstream: upstream}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	body := []byte(`{"type":"response.create","model":"gpt-5.1","input":"hi","instructions":"injected"}`)
	var frames [][]byte
	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, passthroughLifecycleAccount(), "test-token", body, int64(len(body)), len(body), "gpt-5.1", "", "", "", "", 1, func(payload []byte) error {
		frames = append(frames, payload)
		return nil
	}, captureOpenAIResponsesClientInstructions([]byte(`{"instructions":"original"}`)))
	require.NoError(t, err)
	var size int
	for _, frame := range frames {
		size += len(frame)
		if value := gjson.GetBytes(frame, "response.instructions"); value.Exists() {
			require.Equal(t, "original", value.String())
		}
		require.False(t, strings.Contains(string(frame), "injected"))
	}
	require.EqualValues(t, size, *result.ResponseBodyBytes)
}

func TestHTTPToWSInstructionsEchoPreservesOriginalValue(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			cfg := passthroughLifecycleConfig()
			cfg.Gateway.OpenAIWS.OAuthEnabled = true
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.ForceCodexCLI = true
			cfg.Gateway.CodexImageGenerationBridgeEnabled = true
			conn := &openAIWSCaptureConn{events: [][]byte{
				[]byte(`{"type":"response.created","response":{"id":"resp_http_echo","instructions":"injected"}}`),
				[]byte(`{"type":"response.completed","response":{"id":"resp_http_echo","instructions":"injected","usage":{"input_tokens":1,"output_tokens":1}}}`),
			}}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: conn})
			defer pool.Close()
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: &httpUpstreamRecorder{err: errors.New("unexpected HTTP fallback")}, cache: &stubGatewayCache{}, openaiWSPool: pool, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector()}
			account := instructionsEchoOAuthAccount(false)
			account.Extra = map[string]any{"openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModeCtxPool}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			// Exercise the existing legacy/unknown-transport bridge branch. The
			// public handler's explicit HTTP transport must still force HTTP.
			body := []byte(fmt.Sprintf(`{"model":"gpt-5.5","stream":%t,"instructions":"original","input":[{"role":"user","content":"hi"}]}`, stream))
			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err)
			require.True(t, result.OpenAIWSMode)
			require.NotEqual(t, "original", conn.lastWrite["instructions"], "test must exercise an actual instructions rewrite")
			if stream {
				require.Equal(t, "original", sseResponseInstructions(t, recorder.Body.String(), "response.completed").String())
			} else {
				require.Equal(t, "original", gjson.GetBytes(recorder.Body.Bytes(), "instructions").String())
			}
			require.EqualValues(t, recorder.Body.Len(), *result.ResponseBodyBytes)
		})
	}
}
