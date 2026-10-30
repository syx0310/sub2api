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

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// Exercise the actual forwarders: a synthetic ForwardResult{ImageEndpoint:false}
// cannot catch an upstream adapter incorrectly deriving the flag from ImageCount.
func TestMixedImageForwardersKeepTokenBilling(t *testing.T) {
	const model = "gemini-3.1-flash-image"
	for _, protocol := range []string{"native", "messages", "chat", "antigravity"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol, stream), func(t *testing.T) {
				body := []byte(`{"contents":[{"role":"user","parts":[{"text":"draw and explain"}]}]}`)
				action := "generateContent"
				if stream {
					action = "streamGenerateContent"
				}
				path := "/v1beta/models/" + model + ":" + action
				if protocol == "messages" || protocol == "chat" {
					path = "/v1/messages"
					if protocol == "chat" {
						path = "/v1/chat/completions"
					}
					body = []byte(fmt.Sprintf(`{"model":%q,"max_tokens":100,"stream":%t,"messages":[{"role":"user","content":"draw and explain"}]}`, model, stream))
				}
				c, recorder := newAntigravityCompatContext(http.MethodPost, path, body)
				groupID := int64(919)
				key := &APIKey{ID: 919, GroupID: &groupID, Group: &Group{
					ID: groupID, Platform: PlatformComposite, Hydrated: true, Status: StatusActive,
					AllowImageGeneration: true, ImageCountBilling: true, ImagePrice1K: f64p(9),
				}}
				c.Set("api_key", key)
				responseBody := `{"candidates":[{"content":{"role":"model","parts":[{"text":"here is the drawing"},{"inlineData":{"mimeType":"image/png","data":"aGVsbG8="}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"totalTokenCount":120}}`
				contentType := "application/json"
				if protocol == "antigravity" {
					responseBody = `{"response":` + responseBody + `}`
				}
				if stream || protocol == "antigravity" {
					responseBody = "data: " + responseBody + "\n\n"
					contentType = "text/event-stream"
				}
				upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{
					StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}},
					Body: io.NopCloser(strings.NewReader(responseBody)),
				}}}
				var result *ForwardResult
				var err error
				if protocol == "antigravity" {
					svc := newAntigravityCompatService(config.GatewayConfig{}, upstream)
					account := newAntigravityCompatAccount(AccountTypeOAuth)
					account.Credentials["model_mapping"] = map[string]any{model: model}
					result, err = svc.ForwardGemini(context.Background(), c, account, model, action, stream, body, false)
				} else {
					svc := &GeminiMessagesCompatService{cfg: &config.Config{}, httpUpstream: upstream}
					account := geminiVertexAPIKeyTestAccount("")
					switch protocol {
					case "native":
						result, err = svc.ForwardNative(context.Background(), c, account, model, action, stream, body)
					case "messages":
						result, err = svc.Forward(context.Background(), c, account, body)
					case "chat":
						result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body)
					}
				}
				require.NoError(t, err, recorder.Body.String())
				require.NotNil(t, result)
				require.Positive(t, result.ImageCount)
				require.False(t, result.ImageEndpoint)
				require.Equal(t, 100, result.Usage.InputTokens)
				require.Positive(t, result.Usage.OutputTokens)
				resolver := newOpenAITokenImageChannelPricingResolverForTest(t, groupID, model)
				billing := &GatewayService{billingService: resolver.billingService, resolver: resolver}
				var previousCost float64
				for _, enabled := range []bool{false, true} {
					key.Group.ImageCountBilling = enabled
					cost := billing.calculateRecordUsageCost(context.Background(), result, key, model, 1, 1, time.Now())
					require.NotNil(t, cost)
					require.Equal(t, string(BillingModeToken), cost.BillingMode)
					require.Positive(t, cost.TotalCost)
					if enabled {
						require.Equal(t, previousCost, cost.TotalCost, "image switch cannot change mixed request pricing")
					}
					previousCost = cost.TotalCost
				}
			})
		}
	}
}
