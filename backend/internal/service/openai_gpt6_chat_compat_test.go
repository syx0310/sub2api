//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPT6ChatFallbackOutboundEffortAndBilling(t *testing.T) {
	for _, model := range []string{"gpt-6-astra", "gpt-6-sol", "gpt-6.1-sol", "gpt-6-luna"} {
		for _, route := range []string{"chat", "responses", "messages"} {
			for _, effort := range []string{"none", "minimal", "ultra", "max"} {
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/stream=%v", model, route, effort, stream), func(t *testing.T) {
						want := effort
						if effort == "none" || effort == "minimal" {
							want = "low"
						} else if effort == "ultra" {
							want = "max"
							if model == "gpt-6-astra" || model == "gpt-6.1-sol" {
								want = "xhigh"
							}
						}
						response := `{"id":"chatcmpl_1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
						contentType := "application/json"
						if stream {
							response = "data: {\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: {\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\ndata: [DONE]\n\n"
							contentType = "text/event-stream"
						}
						upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response))}}
						svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
						account := forceChatMessagesFallbackAccount()
						account.Credentials["model_mapping"] = map[string]any{"public-alias": model}
						rec := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(rec)
						c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+route, nil)
						request := map[string]any{"model": "public-alias", "stream": stream, "temperature": .3, "messages": []any{map[string]any{"role": "user", "content": "hi"}}}
						switch route {
						case "chat":
							request["reasoning_effort"] = effort
						case "responses":
							delete(request, "messages")
							request["input"] = "hi"
							request["reasoning"] = map[string]any{"effort": effort}
						case "messages":
							request["max_tokens"] = 100
							request["output_config"] = map[string]any{"effort": effort}
						}
						body, err := json.Marshal(request)
						require.NoError(t, err)
						var result *OpenAIForwardResult
						switch route {
						case "chat":
							result, err = svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
						case "responses":
							result, err = svc.forwardResponsesViaRawChatCompletions(context.Background(), c, account, body)
						case "messages":
							result, err = svc.forwardAnthropicViaRawChatCompletions(context.Background(), c, account, body, "")
						}
						require.NoError(t, err)
						require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String())
						require.Equal(t, want, gjson.GetBytes(upstream.lastBody, "reasoning_effort").String())
						require.False(t, gjson.GetBytes(upstream.lastBody, "temperature").Exists())
						require.NotNil(t, result.ReasoningEffort)
						require.Equal(t, want, *result.ReasoningEffort)
						multipliers := map[string]float64{"none": .5, "low": 2, "xhigh": 3, "max": 4}
						bs, resolver := newTokenCostTestEnv(t, PlatformOpenAI, []ChannelModelPricing{{
							Platform: PlatformOpenAI, Models: []string{model}, BillingMode: BillingModeToken,
							InputPrice: testPtrFloat64(1e-6), OutputPrice: testPtrFloat64(2e-6), ReasoningEffortMultipliers: multipliers,
						}}, nil)
						cost, err := bs.CalculateTokenCostForRequest(TokenCostRequest{
							Ctx: context.Background(), Model: model, Group: &Group{ID: 100, Platform: PlatformOpenAI}, Resolver: resolver,
							Tokens: UsageTokens{InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens}, RateMultiplier: 1, ReasoningEffort: *result.ReasoningEffort,
						})
						require.NoError(t, err)
						require.InDelta(t, 7e-6*multipliers[want], cost.TotalCost, 1e-12)
					})
				}
			}
		}
	}
}

func TestGPT61SolChatFallbackOfficialToolsRejected(t *testing.T) {
	for _, official := range []bool{true, false} {
		for _, route := range []string{"responses", "messages"} {
			t.Run(fmt.Sprintf("%s/official=%v", route, official), func(t *testing.T) {
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"chatcmpl_1","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`))}}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
				account := forceChatMessagesFallbackAccount()
				if official {
					account.Credentials["base_url"] = "https://api.openai.com/v1"
				}
				account.Credentials["model_mapping"] = map[string]any{"alias": "gpt-6.1-sol"}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+route, nil)
				var err error
				if route == "responses" {
					_, err = svc.forwardResponsesViaRawChatCompletions(context.Background(), c, account, []byte(`{"model":"alias","input":"hi","reasoning":{"effort":"none"},"tools":[{"type":"function","name":"test","parameters":{"type":"object"}}]}`))
				} else {
					_, err = svc.forwardAnthropicViaRawChatCompletions(context.Background(), c, account, []byte(`{"model":"alias","max_tokens":100,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"disabled"},"tools":[{"name":"test","input_schema":{"type":"object"}}]}`), "")
				}
				if official {
					require.ErrorContains(t, err, "Responses-capable")
					require.Equal(t, http.StatusBadRequest, rec.Code)
					require.Nil(t, upstream.lastReq, "do not send an unsupported official request")
				} else {
					require.NoError(t, err)
					require.Equal(t, "test", gjson.GetBytes(upstream.lastBody, "tools.0.function.name").String())
					require.Equal(t, "low", gjson.GetBytes(upstream.lastBody, "reasoning_effort").String())
				}
			})
		}
	}
}
