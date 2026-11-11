package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type searchCreditDialer struct {
	*openAIWSQueueDialer
	exhausted bool
}

func (d *searchCreditDialer) Dial(ctx context.Context, url string, headers http.Header, proxy string) (openAIWSClientConn, int, http.Header, error) {
	conn, status, responseHeaders, err := d.openAIWSQueueDialer.Dial(ctx, url, headers, proxy)
	if err == nil && d.exhausted && d.DialCount() > 1 {
		responseHeaders = make(http.Header)
		responseHeaders.Set("x-codex-primary-used-percent", "100")
		responseHeaders.Set("x-codex-primary-window-minutes", "300")
		responseHeaders.Set("x-codex-primary-reset-after-seconds", "120")
		responseHeaders.Set("x-codex-credits-has-credits", "true")
		responseHeaders.Set("x-codex-credits-balance", "42")
	}
	return conn, status, responseHeaders, err
}

func TestHTTPToWSSearchReacquirePreservesAdmission(t *testing.T) {
	for _, name := range []string{"standalone", "anchored", "credits_denied"} {
		t.Run(name, func(t *testing.T) {
			cfg := newOpenAIWSV2TestConfig()
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
			old := &openAIWSCaptureConn{events: [][]byte{[]byte(`{"type":"response.completed","response":{"id":"resp_before_search","usage":{"input_tokens":1,"output_tokens":1}}}`)}}
			fresh := &openAIWSCaptureConn{events: [][]byte{[]byte(`{"type":"response.completed","response":{"id":"resp_with_search","usage":{"input_tokens":1,"output_tokens":1}}}`)}}
			dialer := &searchCreditDialer{openAIWSQueueDialer: &openAIWSQueueDialer{conns: []openAIWSClientConn{old, fresh}}, exhausted: name == "credits_denied"}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(dialer)
			t.Cleanup(pool.Close)
			svc := &OpenAIGatewayService{cfg: cfg, cache: &stubGatewayCache{}, openaiWSPool: pool, toolCorrector: NewCodexToolCorrector()}
			account := &Account{ID: 799, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"access_token": "test"}}
			call := func(body map[string]any) (*OpenAIForwardResult, error, int) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				result, err := svc.forwardOpenAIWSV2(t.Context(), c, account, body, "search-test", "search-test", "test", OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2}, true, false, "gpt-5.1", "gpt-5.1", time.Now(), 0, "", nil)
				return result, err, recorder.Code
			}
			_, err, _ := call(map[string]any{"model": "gpt-5.1", "store": false, "input": "before"})
			require.NoError(t, err)
			request := map[string]any{"model": "gpt-5.1", "store": false, "input": "complete new request", "tools": []any{map[string]any{"type": "web_search"}}}
			if name == "anchored" {
				request["previous_response_id"] = "resp_before_search"
			}
			result, err, status := call(request)
			require.Len(t, old.writes, 1, "no search request is written to the incompatible socket")
			switch name {
			case "anchored":
				require.Error(t, err)
				require.Equal(t, http.StatusBadRequest, status)
				require.Equal(t, 1, dialer.DialCount())
				require.Empty(t, fresh.writes)
			case "credits_denied":
				require.Error(t, err)
				require.Equal(t, http.StatusForbidden, infraerrors.Code(err))
				require.Equal(t, 2, dialer.DialCount())
				require.Empty(t, fresh.writes, "new handshake quota must be checked before sending generation")
			default:
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 2, dialer.DialCount())
				require.Len(t, fresh.writes, 1)
				require.Equal(t, "complete new request", fresh.writes[0]["input"])
			}
		})
	}
}
