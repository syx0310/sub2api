package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIGatewayService_Forward_WSv2CompactionNeverRetriedOrPoisoned(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, itemType := range []string{"compaction", "compaction_summary"} {
		for _, stream := range []bool{false, true} {
			for _, objectInput := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream=%v/object=%v", itemType, stream, objectInput), func(t *testing.T) {
					cfg := &config.Config{}
					cfg.Gateway.OpenAIWS.Enabled = true
					cfg.Gateway.OpenAIWS.APIKeyEnabled = true
					cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
					cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
					cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
					cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
					cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
					cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
					errorEvent := []byte(`{"type":"error","error":{"type":"invalid_request_error","code":"invalid_encrypted_content","message":"The encrypted content could not be verified"}}`)
					first := &openAIWSCaptureConn{events: [][]byte{errorEvent}}
					second := &openAIWSCaptureConn{events: [][]byte{errorEvent}}
					pool := newOpenAIWSConnPool(cfg)
					pool.setClientDialerForTest(&openAIWSQueueDialer{conns: []openAIWSClientConn{first, second}})
					t.Cleanup(pool.Close)
					upstream := &httpUpstreamRecorder{}
					svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, openaiWSPool: pool,
						openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector()}
					account := &Account{ID: 95, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
						Credentials: map[string]any{"api_key": "test-key"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
					item := fmt.Sprintf(`{"type":%q,"id":"cmp_keep","encrypted_content":"only-history"}`, itemType)
					input := item
					if !objectInput {
						input = `[` + item + `,{"type":"reasoning","id":"rs_keep","encrypted_content":"reasoning-cipher","summary":[]},{"type":"message","role":"user","content":"continue"}]`
					}
					body := []byte(fmt.Sprintf(`{"model":"gpt-5.3-codex","stream":%v,"previous_response_id":"resp_keep","input":%s}`, stream, input))
					for _, conn := range []*openAIWSCaptureConn{first, second} {
						rec := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(rec)
						c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(string(body)))
						c.Request.Header.Set("Session-Id", "compaction-session")
						c.Request.Header.Set("Thread-Id", "compaction-thread")
						result, err := svc.Forward(context.Background(), c, account, body)
						require.Error(t, err)
						require.Nil(t, result)
						require.Contains(t, strings.ToLower(rec.Body.String()), "encrypted content")
						conn.mu.Lock()
						writes := append([]map[string]any(nil), conn.writes...)
						conn.mu.Unlock()
						require.Len(t, writes, 1, "one upstream dispatch per client request; never delete history to retry")
						wire := requestToJSONString(writes[0])
						require.Equal(t, "resp_keep", gjson.Get(wire, "previous_response_id").String())
						require.JSONEq(t, input, gjson.Get(wire, "input").Raw, "both initial and client-resubmitted history must survive")
						require.False(t, svc.getOpenAIWSStateStore().HasAnySessionInvalidEncryptedContent())
					}
					require.Nil(t, upstream.lastReq, "no HTTP fallback")
				})
			}
		}
	}
}
