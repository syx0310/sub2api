package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodexTurnStateLengthsHTTPForward(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("passthrough=%v/stream=%v", passthrough, stream), func(t *testing.T) {
				// OAuth passthrough asks Codex for SSE even when the client wants JSON.
				upstreamStream := stream || passthrough
				response := `{"id":"resp_lengths","object":"response","model":"gpt-5.1","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`
				contentType := "application/json"
				if upstreamStream {
					contentType = "text/event-stream"
					response = "data: {\"type\":\"response.metadata\",\"headers\":{\"x-codex-turn-state\":\"event-token\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":" + response + "}\n\n"
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}, "X-Codex-Turn-State": {"response-token"}}, Body: io.NopCloser(strings.NewReader(response))}}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream, toolCorrector: NewCodexToolCorrector()}
				account := &Account{ID: 17, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "test-token", "chatgpt_account_id": "test-account"}, Extra: map[string]any{"openai_passthrough": passthrough}}
				c, _ := newTurnStateTestContext(t, 7, "length-observation")
				c.Request.Header.Set(openAICodexTurnStateHeader, "request-token")
				body := []byte(fmt.Sprintf(`{"model":"gpt-5.1","stream":%v,"input":"hello","client_metadata":{"x-codex-turn-state":"metadata-token"}}`, stream))
				result, err := svc.Forward(context.Background(), c, account, body)
				require.NoError(t, err)
				require.Equal(t, len("request-token"), *result.CodexTurnState.RequestHeaderBytes)
				require.Equal(t, len("metadata-token"), *result.CodexTurnState.RequestMetadataBytes)
				require.Equal(t, len("response-token"), *result.CodexTurnState.ResponseHeaderBytes)
				require.Equal(t, "request-token", upstream.lastReq.Header.Get(openAICodexTurnStateHeader))
				require.Equal(t, "metadata-token", gjson.GetBytes(upstream.lastBody, "client_metadata.x-codex-turn-state").String())
				if upstreamStream {
					require.NotNil(t, result.CodexTurnState.ResponseMetadataBytes)
					require.Equal(t, len("event-token"), *result.CodexTurnState.ResponseMetadataBytes)
				} else {
					require.Nil(t, result.CodexTurnState.ResponseMetadataBytes)
				}
			})
		}
	}
}

type codexLengthWSDialer struct{ conn openAIWSClientConn }

type codexLengthWSConn struct{ *stagedPassthroughConn }

func (c *codexLengthWSConn) WriteJSON(ctx context.Context, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.WriteFrame(ctx, coderws.MessageText, encoded)
}

func (d *codexLengthWSDialer) Dial(context.Context, string, http.Header, string) (openAIWSClientConn, int, http.Header, error) {
	return d.conn, http.StatusSwitchingProtocols, http.Header{"X-Codex-Turn-State": {"handshake-token"}}, nil
}

func TestCodexTurnStateLengthsWSPerResponse(t *testing.T) {
	for _, mode := range []string{OpenAIWSIngressModePassthrough, OpenAIWSIngressModeCtxPool} {
		t.Run(mode, func(t *testing.T) {
			controlCtx, cancel := context.WithCancelCause(context.Background())
			defer cancel(context.Canceled)
			upstream := newStagedPassthroughConn()
			cfg := passthroughLifecycleConfig()
			cfg.Gateway.OpenAIFirstOutputTimeoutSeconds = 10
			svc := newPassthroughLifecycleService(cfg, upstream)
			dialer := &codexLengthWSDialer{conn: &codexLengthWSConn{upstream}}
			svc.openaiWSPassthroughDialer = dialer
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(dialer)
			defer pool.Close()
			svc.openaiWSPool = pool
			account := passthroughLifecycleAccount()
			account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
			results := make(chan CodexTurnStateLengths, 4)
			server, serverErr := startPassthroughHookRecordingServer(t, controlCtx, svc, account, &OpenAIWSIngressHooks{
				AfterTurn: func(_ int, result *OpenAIForwardResult, _ error) {
					if result != nil {
						results <- result.CodexTurnState
					}
				},
			})
			defer server.Close()
			client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"gpt-5.1","store":false,"client_metadata":{"x-codex-turn-state":"first-echo"},"input":"hi"}`)
			defer func() { _ = client.CloseNow() }()
			first := requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
			require.Equal(t, "first-echo", gjson.GetBytes(first, "client_metadata.x-codex-turn-state").String())
			upstream.Send(`{"type":"response.metadata","headers":{"x-codex-turn-state":"first-response-token"}}`)
			upstream.Send(`{"type":"response.created","response":{"id":"resp_length_1","model":"gpt-5.1"}}`)
			upstream.Send(`{"type":"response.completed","response":{"id":"resp_length_1","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`)
			for range 3 {
				_, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
				require.NoError(t, err)
			}
			select {
			case got := <-results:
				require.Equal(t, len("first-echo"), *got.RequestMetadataBytes)
				require.Equal(t, len("handshake-token"), *got.ResponseHeaderBytes)
				require.Equal(t, len("first-response-token"), *got.ResponseMetadataBytes)
			case <-time.After(3 * time.Second):
				t.Fatal("missing first turn observation")
			}
			writeCtx, cancelWrite := context.WithTimeout(controlCtx, 3*time.Second)
			err := client.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","store":false,"previous_response_id":"resp_length_1","input":"again"}`))
			cancelWrite()
			require.NoError(t, err)
			_ = requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
			upstream.Send(`{"type":"response.created","response":{"id":"resp_length_2","model":"gpt-5.1"}}`)
			upstream.Send(`{"type":"response.completed","response":{"id":"resp_length_2","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`)
			for range 2 {
				_, err = readPassthroughLifecycleFrame(t, client, 3*time.Second)
				require.NoError(t, err)
			}
			select {
			case got := <-results:
				require.Zero(t, *got.RequestMetadataBytes)
				require.Nil(t, got.RequestHeaderBytes, "no new handshake for the next turn")
				require.Nil(t, got.ResponseHeaderBytes, "do not misattribute cached handshake headers")
				require.Nil(t, got.ResponseMetadataBytes, "no stale metadata from the prior response")
			case <-time.After(3 * time.Second):
				t.Fatal("missing second turn observation")
			}
			_ = client.Close(coderws.StatusNormalClosure, "done")
			select {
			case <-serverErr:
			case <-time.After(3 * time.Second):
				t.Fatal("websocket did not finish")
			}
		})
	}
}
