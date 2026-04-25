package service

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestNormalizeOpenAICompactRequestBody_PreservesFullCodexPayload(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":[{"type":"message","role":"user","content":"draw a cat"}],"instructions":"be helpful","tools":[{"type":"function","function":{"name":"get_weather"}}],"parallel_tool_calls":true,"reasoning":{"effort":"medium"},"text":{"verbosity":"low"},"previous_response_id":"resp_123"}`)

	normalized, changed, err := NormalizeOpenAICompactRequestBodyForTest(body)
	require.NoError(t, err)
	require.False(t, changed)
	require.JSONEq(t, string(body), string(normalized))
}

func TestOpenAIBuildUpstreamRequestCompactPreservesClientVersionHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader([]byte(`{"model":"gpt-5"}`)))
	c.Request.Header.Set("Version", "0.125.1")

	svc := &OpenAIGatewayService{}
	account := &Account{
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"chatgpt_account_id": "chatgpt-acc"},
	}

	req, err := svc.buildUpstreamRequest(c.Request.Context(), c, account, []byte(`{"model":"gpt-5"}`), "token", false, "", true)
	require.NoError(t, err)
	require.Equal(t, "0.125.1", req.Header.Get("Version"))
}

func TestOpenAIBuildUpstreamRequestOpenAIPassthroughCompactPreservesClientVersionHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader([]byte(`{"model":"gpt-5"}`)))
	c.Request.Header.Set("Version", "0.125.1")

	svc := &OpenAIGatewayService{}
	account := &Account{Type: AccountTypeOAuth}

	req, err := svc.buildUpstreamRequestOpenAIPassthrough(c.Request.Context(), c, account, []byte(`{"model":"gpt-5"}`), "token")
	require.NoError(t, err)
	require.Equal(t, "0.125.1", req.Header.Get("Version"))
}
