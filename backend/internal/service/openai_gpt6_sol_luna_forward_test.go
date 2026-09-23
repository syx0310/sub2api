//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPT6SolLunaHTTPForwardKeepsClientInstructions(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		for _, route := range []string{"oauth", "passthrough", "apikey"} {
			t.Run(model+"/"+route, func(t *testing.T) {
				s := newAstraOAuthSetup(t, route == "passthrough")
				s.account.Credentials["model_mapping"] = map[string]any{"public-alias": model}
				if route == "apikey" {
					s.account.Type = AccountTypeAPIKey
					s.account.Credentials = map[string]any{"api_key": "test", "base_url": "https://api.openai.com/v1", "model_mapping": map[string]any{"public-alias": model}}
					s.account.Extra["openai_responses_supported"] = true
				}
				inner := fmt.Sprintf(`{"id":"resp_test","model":%q,"output":[],"usage":{"input_tokens":1,"output_tokens":1}}`, model)
				s.upstream.resp = &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(codexCompletedSSE(inner)))}
				requested := model
				if route != "passthrough" {
					requested = "public-alias"
				}
				body := []byte(fmt.Sprintf(`{"model":%q,"instructions":"client instructions, unchanged","stream":false,"reasoning":{"mode":"pro","effort":"none"},"temperature":0.2,"prompt_cache_options":{"ttl":"30m"},"input":[{"role":"user","content":"hi"}]}`, requested))
				result, err := s.svc.Forward(context.Background(), s.c, s.account, body)
				require.NoError(t, err)
				require.Equal(t, model, gjson.GetBytes(s.upstream.lastBody, "model").String())
				require.Equal(t, "low", gjson.GetBytes(s.upstream.lastBody, "reasoning.effort").String())
				require.Equal(t, "pro", gjson.GetBytes(s.upstream.lastBody, "reasoning.mode").String())
				require.Equal(t, "client instructions, unchanged", gjson.GetBytes(s.upstream.lastBody, "instructions").String())
				require.False(t, gjson.GetBytes(s.upstream.lastBody, "temperature").Exists())
				require.Equal(t, route == "apikey", gjson.GetBytes(s.upstream.lastBody, "prompt_cache_options").Exists())
				require.NotNil(t, result.ReasoningEffort)
				require.Equal(t, "low", *result.ReasoningEffort)
			})
		}
	}
}

func TestGPT6SolLunaChatToolsUseResponsesAndRawRejects(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		t.Run(model, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(codexCompletedSSE(fmt.Sprintf(`{"id":"resp_cc","model":%q,"output":[],"usage":{"input_tokens":1,"output_tokens":1}}`, model))))}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "base_url": "https://api.openai.com/v1", "model_mapping": map[string]any{"alias": model}}, Extra: map[string]any{"openai_responses_supported": true}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			body := []byte(`{"model":"alias","stream":false,"reasoning_effort":"none","temperature":0.3,"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"test","parameters":{"type":"object","properties":{}}}}]}`)
			_, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
			require.NoError(t, err)
			require.Equal(t, "/v1/responses", upstream.lastReq.URL.Path)
			require.Equal(t, "low", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
			require.Equal(t, "test", gjson.GetBytes(upstream.lastBody, "tools.0.name").String())
			require.False(t, gjson.GetBytes(upstream.lastBody, "temperature").Exists())

			recorder := httptest.NewRecorder()
			c, _ = gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			_, err = svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
			require.ErrorContains(t, err, "Responses-capable")
			require.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}

func TestGPT6SolLunaAnthropicMappedModelAndInstructions(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		for _, effort := range []string{"none", "max"} {
			t.Run(model+"/"+effort, func(t *testing.T) {
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(codexCompletedSSE(fmt.Sprintf(`{"id":"resp_messages","model":%q,"output":[],"usage":{"input_tokens":1,"output_tokens":1}}`, model))))}}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
				account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "base_url": "https://api.openai.com/v1", "model_mapping": map[string]any{"alias": model}}, Extra: map[string]any{"openai_responses_supported": true}}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				body := []byte(fmt.Sprintf(`{"model":"alias","system":"client system only","max_tokens":100,"output_config":{"effort":%q},"temperature":0.2,"messages":[{"role":"user","content":"hi"}]}`, effort))
				result, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
				require.NoError(t, err)
				want := effort
				if effort == "none" {
					want = "low"
				}
				require.Equal(t, want, gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
				require.Empty(t, gjson.GetBytes(upstream.lastBody, "instructions").String(), "API-key bridge keeps the existing empty instructions placeholder")
				require.Contains(t, gjson.GetBytes(upstream.lastBody, "input.0.content").Raw, "client system only")
				require.False(t, gjson.GetBytes(upstream.lastBody, "temperature").Exists())
				require.Equal(t, want, *result.ReasoningEffort)
			})
		}
	}
}

func TestGPT6SolLunaWSConfigurationAndCompact(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		for _, mode := range []string{OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough} {
			t.Run(model+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancelCause(context.Background())
				defer cancel(context.Canceled)
				upstream := newStagedPassthroughConn()
				cfg := passthroughLifecycleConfig()
				svc := newPassthroughLifecycleService(cfg, upstream)
				dialer := &codexLengthWSDialer{conn: &codexLengthWSConn{upstream}}
				svc.openaiWSPassthroughDialer = dialer
				pool := newOpenAIWSConnPool(cfg)
				pool.setClientDialerForTest(dialer)
				defer pool.Close()
				svc.openaiWSPool = pool
				account := passthroughLifecycleAccount()
				account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
				results := make(chan *OpenAIForwardResult, 4)
				server, serverErr := startPassthroughHookRecordingServer(t, ctx, svc, account, &OpenAIWSIngressHooks{
					AfterTurn: func(_ int, result *OpenAIForwardResult, _ error) {
						if result != nil {
							results <- result
						}
					},
				})
				defer server.Close()
				first := fmt.Sprintf(`{"type":"response.create","model":%q,"store":false,"instructions":"client-owned","reasoning":{"effort":"none"},"input":[{"role":"user","content":"hello"}]}`, model)
				client := dialPassthroughLifecycleClientWithPayload(t, server, first)
				defer func() { _ = client.CloseNow() }()
				for turn, want := range []string{"low", "max", "max", "medium"} {
					if turn > 0 {
						input := `[{"type":"configuration_update","reasoning":{"effort":"ultra"}},{"role":"user","content":"continue"}]`
						effort := "none"
						if turn == 2 {
							input = `[{"type":"compaction_trigger"}]`
						}
						if turn == 3 {
							input = `[{"role":"user","content":"after compact"}]`
							effort = "medium"
						}
						payload := fmt.Sprintf(`{"type":"response.create","model":%q,"store":false,"previous_response_id":"resp_%d","reasoning":{"effort":%q},"input":%s}`, model, turn, effort, input)
						writeCtx, cancelWrite := context.WithTimeout(ctx, 3*time.Second)
						err := client.Write(writeCtx, coderws.MessageText, []byte(payload))
						cancelWrite()
						require.NoError(t, err)
					}
					out := requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
					require.Equal(t, model, gjson.GetBytes(out, "model").String())
					if turn < 3 {
						require.Equal(t, "low", gjson.GetBytes(out, "reasoning.effort").String())
					}
					if turn == 0 {
						require.Equal(t, "client-owned", gjson.GetBytes(out, "instructions").String())
					}
					if turn == 1 {
						require.Equal(t, "max", gjson.GetBytes(out, "input.0.reasoning.effort").String())
					}
					if turn == 2 {
						items := gjson.GetBytes(out, "input").Array()
						require.NotEmpty(t, items)
						require.Equal(t, "compaction_trigger", items[len(items)-1].Get("type").String(), "the trigger must remain last, after any inherited configuration")
					}
					id := fmt.Sprintf("resp_%d", turn+1)
					upstream.Send(fmt.Sprintf(`{"type":"response.created","response":{"id":%q,"model":%q}}`, id, model))
					output := `[{"type":"message","role":"assistant","content":[]}]`
					frames := 2
					if turn == 2 {
						output = `[{"type":"compaction","encrypted_content":"opaque"}]`
						upstream.Send(`{"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"opaque"}}`)
						frames++
					}
					upstream.Send(fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"model":%q,"output":%s,"usage":{"input_tokens":1,"output_tokens":1}}}`, id, model, output))
					for range frames {
						_, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
						require.NoError(t, err)
					}
					select {
					case result := <-results:
						require.NotNil(t, result.ReasoningEffort)
						require.Equal(t, want, *result.ReasoningEffort, "turn %d", turn+1)
					case <-time.After(3 * time.Second):
						t.Fatal("missing usage result")
					}
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
}
