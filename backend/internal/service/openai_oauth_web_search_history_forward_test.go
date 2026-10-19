//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// TestForward_OAuthWebSearchHistoryDeclaresTool drives the Codex compaction
// shape (web_search_call history, tools:[]) through Forward and asserts the
// body reaching chatgpt.com declares web_search (#7927): top-level for the
// standard wire, input additional_tools for Responses Lite.
func TestForward_OAuthWebSearchHistoryDeclaresTool(t *testing.T) {
	for _, tc := range []struct {
		name        string
		passthrough bool
		lite        bool
	}{
		{"transform", false, false},
		{"passthrough", true, false},
		{"transform lite", false, true},
		{"passthrough lite", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			s := newAstraOAuthSetup(t, tc.passthrough)
			if tc.lite {
				s.c.Request.Header.Set(responsesLiteHeader, "true")
			}
			inner := `{"id":"resp_test","model":"gpt-5.5","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
			s.upstream.resp = &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(codexCompletedSSE(inner))),
			}
			body, err := sjson.SetRawBytes([]byte(openAIWebSearchHistoryCompactionBody), "input.-1", []byte(`{"type":"compaction_trigger"}`))
			require.NoError(t, err)

			result, err := s.svc.Forward(context.Background(), s.c, s.account, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, s.upstream.lastReq)
			require.Equal(t, astraProCodexResponsesURL, s.upstream.lastReq.URL.String())

			forwarded := s.upstream.lastBody
			require.Equal(t, "web_search_call", gjson.GetBytes(forwarded, "input.1.type").String())
			require.Equal(t, "none", gjson.GetBytes(forwarded, "tool_choice").String())
			items := gjson.GetBytes(forwarded, "input").Array()
			require.Equal(t, "compaction_trigger", items[len(items)-1].Get("type").String())
			if !tc.lite {
				tools := gjson.GetBytes(forwarded, "tools").Array()
				require.Len(t, tools, 1)
				require.Equal(t, "web_search", tools[0].Get("type").String())
				return
			}
			require.False(t, gjsonToolsContainWebSearch(gjson.GetBytes(forwarded, "tools")))
			additional := items[len(items)-2]
			require.Equal(t, "additional_tools", additional.Get("type").String())
			require.Equal(t, "web_search", additional.Get("tools.0.type").String())
		})
	}
}

// Exercise real forwarding branches, not just compatibility helper output.
// The historical HTTP->WS branch is covered without changing the production
// HTTP ingress rule that normally selects SSE.
func TestForward_OAuthWebSearchCompactionClientShapes(t *testing.T) {
	for _, route := range []string{"transform SSE", "passthrough SSE", "legacy HTTP to WS"} {
		for _, lite := range []bool{false, true} {
			for _, kind := range []string{"native", "native with anchor", "header local", "body turn wins", "body null wins", "body empty wins"} {
				t.Run(fmt.Sprintf("%s/lite=%v/%s", route, lite, kind), func(t *testing.T) {
					s := newAstraOAuthSetup(t, route == "passthrough SSE")
					s.c.Request.Header.Set(openAIWSTurnMetadataHeader, `{"request_kind":"compaction"}`)
					if lite {
						s.c.Request.Header.Set(responsesLiteHeader, "true")
					}
					body := oauthWebSearchCompactionClientBody(t, kind, lite)
					inner := `{"id":"resp_compact","model":"gpt-6.1-sol","output":[{"type":"compaction","encrypted_content":"new-summary"}],"usage":{"input_tokens":1,"output_tokens":1}}`
					s.upstream.resp = &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(codexCompletedSSE(inner)))}
					var capture *openAIWSCaptureConn
					if route == "legacy HTTP to WS" {
						s.svc.cfg = openAIWSThreadTestConfig()
						capture = &openAIWSCaptureConn{events: [][]byte{[]byte(`{"type":"response.completed","response":` + inner + `}`)}}
						pool := newOpenAIWSConnPool(s.svc.cfg)
						pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: capture})
						t.Cleanup(pool.Close)
						s.svc.openaiWSPool = pool
					} else {
						SetOpenAIClientTransport(s.c, OpenAIClientTransportHTTP)
					}
					result, err := s.svc.Forward(context.Background(), s.c, s.account, body)
					if kind == "native with anchor" {
						require.Error(t, err)
						require.Nil(t, result)
						require.Equal(t, http.StatusBadRequest, s.rec.Code)
						require.Equal(t, string(OpenAIHTTPContinuationUnsupportedReason), gjson.Get(s.rec.Body.String(), "error.code").String())
						require.Contains(t, s.rec.Body.String(), "complete required context")
						require.Nil(t, s.upstream.lastReq, "OAuth HTTP must not borrow a WS anchor")
						if capture != nil {
							capture.mu.Lock()
							count := len(capture.writes)
							capture.mu.Unlock()
							require.Zero(t, count)
						}
						return
					}
					require.NoError(t, err)
					require.NotNil(t, result)
					forwarded := s.upstream.lastBody
					if capture != nil {
						capture.mu.Lock()
						writes := append([]map[string]any(nil), capture.writes...)
						capture.mu.Unlock()
						require.Len(t, writes, 1)
						forwarded = []byte(requestToJSONString(writes[0]))
						require.Nil(t, s.upstream.lastReq, "must exercise upstream WS, not SSE")
					} else {
						require.Len(t, s.upstream.requests, 1, "no destructive recovery/retry")
					}
					assertOAuthWebSearchCompactionForward(t, body, forwarded, lite, strings.HasPrefix(kind, "native") || kind == "header local")
				})
			}
		}
	}
}

func TestForward_OAuthWebSearchExplicitToolChoice(t *testing.T) {
	const function = `{"type":"function","name":"echo","parameters":{"type":"object","properties":{}}}`
	for _, passthrough := range []bool{false, true} {
		for _, lite := range []bool{false, true} {
			for _, placement := range []string{"history function", "history namespace", "top namespace", "missing"} {
				if lite && placement == "top namespace" {
					continue // Lite declarations belong in additional_tools.
				}
				for _, choice := range []string{`"required"`, `{"type":"function","name":"echo"}`, `{"type":"function","name":"echo","namespace":"functions"}`, `{"type":"allowed_tools","mode":"auto","tools":[{"type":"function","name":"echo"}]}`, `{"type":"custom","name":"echo"}`} {
					t.Run(fmt.Sprintf("passthrough=%v/lite=%v/%s/%s", passthrough, lite, placement, choice), func(t *testing.T) {
						s := newAstraOAuthSetup(t, passthrough)
						SetOpenAIClientTransport(s.c, OpenAIClientTransportHTTP)
						if lite {
							s.c.Request.Header.Set(responsesLiteHeader, "true")
						}
						body := oauthWebSearchCompactionClientBody(t, "native", lite)
						body, err := sjson.DeleteBytes(body, "tools")
						require.NoError(t, err)
						switch placement {
						case "history function":
							body, err = sjson.SetRawBytes(body, "input.0.tools", []byte(`[`+function+`]`))
						case "top namespace":
							body, err = sjson.SetRawBytes(body, "tools", []byte(gjson.GetBytes(body, "input.0.tools").Raw))
							require.NoError(t, err)
							body, err = sjson.DeleteBytes(body, "input.0")
						case "missing":
							body, err = sjson.SetRawBytes(body, "input.0.tools", []byte(`[]`))
						}
						require.NoError(t, err)
						body, err = sjson.SetRawBytes(body, "tool_choice", []byte(choice))
						require.NoError(t, err)
						// Upstream validation, not success from a permissive fixture,
						// is the expected owner of conflicting/unknown choices.
						s.upstream.resp = &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"fixture validation error"}}`))}
						result, err := s.svc.Forward(context.Background(), s.c, s.account, body)
						require.Error(t, err)
						require.Nil(t, result)
						require.Len(t, s.upstream.requests, 1, "never turn an explicit choice into a replayable auto/none request")
						forwarded := s.upstream.lastBody
						require.JSONEq(t, choice, gjson.GetBytes(forwarded, "tool_choice").Raw)
						require.False(t, gjsonToolsContainWebSearch(gjson.GetBytes(forwarded, "tools")))
						for _, item := range gjson.GetBytes(forwarded, "input").Array() {
							require.False(t, gjsonToolsContainWebSearch(item.Get("tools")))
						}
						require.Equal(t, "opaque-summary", gjson.GetBytes(forwarded, `input.#(type=="compaction").encrypted_content`).String())
					})
				}
			}
		}
	}
}

func TestForward_OAuthHTTPContinuationRequiresCompleteReplay(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, lite := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("passthrough=%v/lite=%v/stream=%v", passthrough, lite, stream), func(t *testing.T) {
					s := newAstraOAuthSetup(t, passthrough)
					SetOpenAIClientTransport(s.c, OpenAIClientTransportHTTP)
					if lite {
						s.c.Request.Header.Set(responsesLiteHeader, "true")
					}
					body := []byte(fmt.Sprintf(`{"model":"gpt-6.1-sol","stream":%v,"previous_response_id":"resp_ws_only","input":[{"type":"compaction_trigger"}]}`, stream))
					original := string(body)
					result, err := s.svc.Forward(context.Background(), s.c, s.account, body)
					require.Error(t, err)
					require.Nil(t, result)
					require.Equal(t, http.StatusBadRequest, s.rec.Code)
					require.Equal(t, "previous_response_id", gjson.Get(s.rec.Body.String(), "error.param").String())
					require.Contains(t, s.rec.Body.String(), "complete required context")
					require.NotContains(t, s.rec.Body.String(), "resp_ws_only")
					require.Empty(t, s.upstream.requests, "reject before any upstream I/O, not after accepting a synthetic continuation")
					require.Equal(t, original, string(body))

					// A new HTTP request must supply actual full context, not just
					// the same incremental input with the anchor silently deleted.
					replay := newAstraOAuthSetup(t, passthrough)
					SetOpenAIClientTransport(replay.c, OpenAIClientTransportHTTP)
					if lite {
						replay.c.Request.Header.Set(responsesLiteHeader, "true")
					}
					replayBody := oauthWebSearchCompactionClientBody(t, "native", lite)
					replayBody, err = sjson.SetBytes(replayBody, "stream", stream)
					require.NoError(t, err)
					inner := `{"id":"resp_replay","model":"gpt-6.1-sol","output":[{"type":"compaction","encrypted_content":"new-summary"}],"usage":{"input_tokens":1,"output_tokens":1}}`
					replay.upstream.resp = &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(codexCompletedSSE(inner)))}
					result, err = replay.svc.Forward(context.Background(), replay.c, replay.account, replayBody)
					require.NoError(t, err)
					require.NotNil(t, result)
					assertOAuthWebSearchCompactionForward(t, replayBody, replay.upstream.lastBody, lite, true)
					require.False(t, gjson.GetBytes(replay.upstream.lastBody, "previous_response_id").Exists())
				})
			}
		}
	}
}

func oauthWebSearchCompactionClientBody(t *testing.T, kind string, lite bool) []byte {
	t.Helper()
	body := []byte(openAIWebSearchHistoryNativeCompactionBody)
	var err error
	if !strings.HasPrefix(kind, "native") {
		body, err = sjson.DeleteBytes(body, "input.3")
		require.NoError(t, err)
	} else if !lite {
		body, err = sjson.SetRawBytes(body, "tools", []byte(`[{"type":"function","name":"echo","parameters":{"type":"object","properties":{}}}]`))
		require.NoError(t, err)
	}
	if kind == "native with anchor" {
		body, err = sjson.SetBytes(body, "previous_response_id", "resp_keep")
		require.NoError(t, err)
	}
	if lite {
		body, err = sjson.SetBytes(body, "client_metadata.ws_request_header_x_openai_internal_codex_responses_lite", "true")
		require.NoError(t, err)
	}
	if value, ok := map[string]string{
		"body turn wins": `"{\"request_kind\":\"turn\"}"`,
		"body null wins": `null`, "body empty wins": `""`,
	}[kind]; ok {
		body, err = sjson.SetRawBytes(body, "client_metadata."+openAIWSTurnMetadataHeader, []byte(value))
		require.NoError(t, err)
	}
	return body
}

func assertOAuthWebSearchCompactionForward(t *testing.T, original, forwarded []byte, lite, injected bool) {
	t.Helper()
	for _, itemType := range []string{"additional_tools", "web_search_call", "compaction"} {
		path := fmt.Sprintf(`input.#(type==%q)`, itemType)
		// Namespaces may be adapted elsewhere in the forwarding pipeline, but
		// the original declaration/typed ID and opaque summary must survive.
		require.Equal(t, gjson.GetBytes(original, path+".id").String(), gjson.GetBytes(forwarded, path+".id").String(), itemType)
	}
	require.Equal(t, "opaque-summary", gjson.GetBytes(forwarded, `input.#(type=="compaction").encrypted_content`).String())
	if anchor := gjson.GetBytes(original, "previous_response_id"); anchor.Exists() {
		require.Equal(t, anchor.String(), gjson.GetBytes(forwarded, "previous_response_id").String())
	}
	choice := "auto"
	if injected {
		choice = "none"
	}
	require.Equal(t, choice, gjson.GetBytes(forwarded, "tool_choice").String())
	input := gjson.GetBytes(forwarded, "input").Array()
	if HasCompactionTriggerInInput(original) {
		require.Equal(t, "compaction_trigger", input[len(input)-1].Get("type").String())
	}
	if lite {
		require.False(t, gjsonToolsContainWebSearch(gjson.GetBytes(forwarded, "tools")))
		at := len(input) - 1
		if HasCompactionTriggerInInput(original) {
			at--
		}
		require.Equal(t, injected, gjsonToolsContainWebSearch(input[at].Get("tools")))
		if injected {
			require.Equal(t, "additional_tools", input[at].Get("type").String())
			require.False(t, input[at].Get("id").Exists(), "do not manufacture or edit a typed historical ID")
		}
	} else {
		require.Equal(t, injected, gjsonToolsContainWebSearch(gjson.GetBytes(forwarded, "tools")))
	}
}

func TestOAuthWebSearchCompactionWSPerCreate(t *testing.T) {
	for _, mode := range []string{OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough} {
		for _, lite := range []bool{false, true} {
			for _, kind := range []string{"native", "native with anchor", "header local", "body turn wins", "body null wins", "body empty wins"} {
				t.Run(fmt.Sprintf("%s/lite=%v/%s", mode, lite, kind), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					upstream := newStagedPassthroughConn()
					cfg := openAIWSThreadTestConfig()
					cfg.Gateway.OpenAIWS.IngressModeDefault = mode
					svc := newPassthroughLifecycleService(cfg, upstream)
					dialer := &codexLengthWSDialer{conn: &codexLengthWSConn{upstream}}
					svc.openaiWSPassthroughDialer = dialer
					pool := newOpenAIWSConnPool(cfg)
					pool.setClientDialerForTest(dialer)
					defer pool.Close()
					svc.openaiWSPool = pool
					account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 2,
						Credentials: map[string]any{"access_token": "test-token", "chatgpt_account_id": "test-account"}}
					server, serverErr := startPassthroughHookRecordingServer(t, ctx, svc, account, nil)
					defer server.Close()
					client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &coderws.DialOptions{
						HTTPHeader: http.Header{http.CanonicalHeaderKey(openAIWSTurnMetadataHeader): {`{"request_kind":"compaction"}`}},
					})
					require.NoError(t, err)
					defer client.CloseNow()
					finishTurn := func(id string, compact bool) {
						output := `[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]`
						if compact {
							output = `[{"type":"compaction","encrypted_content":"new-summary"}]`
						}
						upstream.Send(fmt.Sprintf(`{"type":"response.created","response":{"id":%q,"model":"gpt-6.1-sol"}}`, id))
						upstream.Send(fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"model":"gpt-6.1-sol","output":%s,"usage":{"input_tokens":1,"output_tokens":1}}}`, id, output))
						for _, event := range []string{"response.created", "response.completed"} {
							frame, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
							require.NoError(t, err)
							require.Equal(t, event, gjson.GetBytes(frame, "type").String())
						}
					}
					if kind == "native with anchor" {
						// A store=false ctx_pool anchor belongs to a live upstream
						// connection. Establish it instead of inventing a cached ID.
						initial := oauthWebSearchCompactionClientBody(t, "body turn wins", lite)
						initial, err = sjson.SetBytes(initial, "type", "response.create")
						require.NoError(t, err)
						initial, err = sjson.SetRawBytes(initial, "input", []byte(`[{"type":"message","role":"user","content":"hello"}]`))
						require.NoError(t, err)
						require.NoError(t, client.Write(ctx, coderws.MessageText, initial))
						requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
						finishTurn("resp_keep", false)
					}
					for turn := 1; turn <= 2; turn++ {
						requestKind := kind
						if turn == 2 {
							requestKind = "ordinary" // no trigger, no body intent, no anchor
						}
						body := oauthWebSearchCompactionClientBody(t, requestKind, lite)
						body, err = sjson.SetBytes(body, "type", "response.create")
						require.NoError(t, err)
						require.NoError(t, client.Write(ctx, coderws.MessageText, body))
						forwarded := requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
						injected := turn == 1 && (strings.HasPrefix(kind, "native") || kind == "header local")
						assertOAuthWebSearchCompactionForward(t, body, forwarded, lite, injected)
						if turn == 2 {
							require.False(t, gjson.GetBytes(forwarded, "client_metadata."+openAIWSTurnMetadataHeader).Exists(), "never inherit handshake compaction intent")
							require.False(t, gjson.GetBytes(forwarded, "previous_response_id").Exists(), "do not synthesize a compaction anchor")
						}
						// Complete only after the outbound structure has been checked.
						finishTurn(fmt.Sprintf("resp_search_%d", turn), injected)
					}
					_ = client.Close(coderws.StatusNormalClosure, "done")
					select {
					case <-serverErr:
					case <-ctx.Done():
						t.Fatal("websocket did not finish")
					}
				})
			}
		}
	}
}
