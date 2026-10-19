//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestV0217ConversionFloorPreservesRolePolicy(t *testing.T) {
	for _, floor := range []int{16, 128, 256} {
		for _, preserveSystem := range []bool{false, true} {
			t.Run(fmt.Sprintf("floor=%d/preserve=%t", floor, preserveSystem), func(t *testing.T) {
				limit := 1
				req := &apicompat.ChatCompletionsRequest{Model: "gpt-6.1-sol", MaxTokens: &limit,
					ReasoningEffort: "none", Messages: []apicompat.ChatMessage{
						{Role: "system", Content: json.RawMessage(`"keep these instructions"`)},
						{Role: "user", Content: json.RawMessage(`"hello"`)},
					}}
				cfg := &config.Config{Gateway: config.GatewayConfig{MinOutputTokens: floor}}
				var out *apicompat.ResponsesRequest
				var err error
				if preserveSystem {
					out, err = apicompat.ChatCompletionsToResponsesPreserveSystemRoleWithMinOutputTokens(req, configuredMinOutputTokens(cfg))
				} else {
					out, err = chatCompletionsToResponses(cfg, req)
				}
				require.NoError(t, err, "the final-model gateway policy, not the converter, resolves none")
				require.Equal(t, floor, *out.MaxOutputTokens)
				role := "developer"
				if preserveSystem {
					role = "system"
				}
				require.Equal(t, role, gjson.GetBytes(out.Input, "0.role").String())
				require.Contains(t, string(out.Input), "keep these instructions")
				require.Nil(t, out.Instructions, "conversion must not introduce a new prompt")
			})
		}
	}
	for _, floor := range []int{16, 256} {
		out, err := anthropicToResponses(&config.Config{Gateway: config.GatewayConfig{MinOutputTokens: floor}},
			&apicompat.AnthropicRequest{Model: "gpt-6.1-sol", MaxTokens: 1,
				Messages: []apicompat.AnthropicMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}}})
		require.NoError(t, err)
		require.Equal(t, floor, *out.MaxOutputTokens)
	}
}

// The latest client uses a full HTTP request for speculative remote V2
// compaction, not an HTTP request borrowing a WebSocket continuation anchor.
func TestV0217BackgroundCompactionHTTPPreservesClientReplayFields(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, lite := range []bool{false, true} {
			t.Run(fmt.Sprintf("passthrough=%t/lite=%t", passthrough, lite), func(t *testing.T) {
				s := newAstraOAuthSetup(t, passthrough)
				SetOpenAIClientTransport(s.c, OpenAIClientTransportHTTP)
				s.svc.cfg.Gateway.MinOutputTokens = 256
				if lite {
					s.c.Request.Header.Set(responsesLiteHeader, "true")
				}
				metadata := `{"request_kind":"compaction","compaction_phase":"background","thread_id":"child-thread","future":{"keep":true}}`
				body := oauthWebSearchCompactionClientBody(t, "native", lite)
				var err error
				for path, value := range map[string]any{
					"client_metadata." + openAIWSTurnMetadataHeader: metadata,
					"include": []string{"reasoning.encrypted_content", "output.encrypted_content"},
					"stream_options.reasoning_summary_delivery": "sequential_cutoff",
				} {
					body, err = sjson.SetBytes(body, path, value)
					require.NoError(t, err)
				}
				const inner = `{"id":"resp_background","model":"gpt-6.1-sol","created_at":1700000000,"output":[{"type":"compaction","encrypted_content":"new-summary"}],"usage":{"input_tokens":1,"output_tokens":1}}`
				s.upstream.resp = &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
					Body: io.NopCloser(strings.NewReader(codexCompletedSSE(inner)))}
				_, err = s.svc.Forward(context.Background(), s.c, s.account, body)
				require.NoError(t, err)
				require.Len(t, s.upstream.requests, 1)
				forwarded := s.upstream.lastBody
				assertOAuthWebSearchCompactionForward(t, body, forwarded, lite, true)
				// Existing credential/API-key namespace isolation is distinct from
				// optional fingerprint merging; it must still happen exactly once.
				expectedMetadata, err := sjson.Set(metadata, "thread_id", scopeCodexAccountIdentityValue(s.account, 0, "thread", "child-thread"))
				require.NoError(t, err)
				require.JSONEq(t, expectedMetadata, gjson.GetBytes(forwarded, "client_metadata."+openAIWSTurnMetadataHeader).String())
				require.Contains(t, gjson.GetBytes(forwarded, "include").Raw, "output.encrypted_content")
				require.Equal(t, "sequential_cutoff", gjson.GetBytes(forwarded, "stream_options.reasoning_summary_delivery").String())
				require.False(t, gjson.GetBytes(forwarded, "previous_response_id").Exists())
				require.False(t, gjson.GetBytes(forwarded, "max_output_tokens").Exists(), "conversion floor does not inject a native Responses output cap")
			})
		}
	}
}
