package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCNResponsesToolMediaOnlyRewritesDeepSeek(t *testing.T) {
	body := []byte(`{"model":"test","store":true,"previous_response_id":"resp_1","large":9007199254740993,"input":[{"type":"function_call_output","call_id":"A","output":[{"type":"input_image","image_url":"data:image/png;base64,QQ==","detail":"original"}]}]}`)
	for _, platform := range []string{PlatformDeepseek, PlatformKimi, PlatformMiniMax, PlatformOpenCodeGo, PlatformOpenAI} {
		t.Run(platform, func(t *testing.T) {
			account := &Account{Platform: platform, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": APIProtocolResponses}}
			normalized, err := normalizeDeepSeekResponsesRequestBody(account, body)
			require.NoError(t, err)
			require.Equal(t, "9007199254740993", gjson.GetBytes(normalized, "large").Raw)
			if platform == PlatformDeepseek {
				require.Equal(t, gjson.String, gjson.GetBytes(normalized, "input.0.output").Type)
				require.Equal(t, "original", gjson.GetBytes(normalized, "input.1.content.1.detail").String())
			} else {
				require.JSONEq(t, gjson.GetBytes(body, "input").Raw, gjson.GetBytes(normalized, "input").Raw)
			}
			if platform == PlatformOpenAI {
				require.Equal(t, body, normalized)
			}
		})
	}
}

func TestDeepSeekUnrepresentableToolMediaReturnsClientError(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4.1-flash","input":[{"type":"function_call_output","call_id":"A","output":[{"type":"input_image","image_url":"data:image/png;base64,QQ=="},{"type":"encrypted_content","encrypted_content":"private-result"}]}]}`)
	for _, passthrough := range []bool{false, true} {
		name := "normal"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			account := &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": APIProtocolResponses}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}}
			var err error
			if passthrough {
				_, err = svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, account, body, "token")
			} else {
				_, err = svc.buildUpstreamRequest(context.Background(), c, account, body, "token", true, "", false)
			}
			require.Error(t, err)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Equal(t, "invalid_request_error", gjson.Get(recorder.Body.String(), "error.type").String())
			require.NotContains(t, recorder.Body.String(), "private-result")
		})
	}
}
