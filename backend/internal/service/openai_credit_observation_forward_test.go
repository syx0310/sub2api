//go:build unit

package service

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCodexCreditHTTPObservationsWhileSubscriptionAvailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, source := range []string{"headers", "sse", "quota_only"} {
				t.Run(fmt.Sprintf("passthrough=%t/stream=%t/source=%s", passthrough, stream, source), func(t *testing.T) {
					headers := make(http.Header)
					headers.Set("Content-Type", "text/event-stream")
					headers.Set("x-codex-primary-used-percent", "10")
					headers.Set("x-codex-primary-window-minutes", "300")
					headers.Set("x-codex-primary-reset-after-seconds", "3600")
					headers.Set("x-codex-secondary-used-percent", "20")
					headers.Set("x-codex-secondary-window-minutes", "10080")
					headers.Set("x-codex-secondary-reset-after-seconds", "7200")
					body := openAINonStreamRegressionSSE
					switch source {
					case "headers":
						headers.Set("x-codex-credits-has-credits", "true")
						headers.Set("x-codex-credits-unlimited", "false")
						headers.Set("x-codex-credits-balance", "42.25")
					case "sse":
						body = "data: {\"type\":\"codex.rate_limits\",\"credits\":{\"has_credits\":true,\"unlimited\":false,\"balance\":\"42.25\"}}\n\n" + body
					}
					upstream := &httpUpstreamRecorder{resp: &http.Response{
						StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader(body)),
					}}
					cfg := &config.Config{}
					svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, responseHeaderFilter: compileResponseHeaderFilter(cfg)}
					account := creditTestAccount()
					account.Status, account.Schedulable, account.Concurrency = StatusActive, true, 1
					account.Credentials = map[string]any{"access_token": "test-only", "chatgpt_account_id": "test-only"}
					account.Extra["openai_passthrough"] = passthrough
					account.Extra[CodexAllowCreditUsageKey] = false
					oldAt := time.Now().Add(-24 * time.Hour).UnixMilli()
					if source == "quota_only" {
						account.Extra[codexCreditStateKey] = codexCreditState{Credits: &OpenAICredits{HasCredits: true}, CreditsAt: oldAt}
					}
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
					result, err := svc.Forward(t.Context(), c, account, []byte(fmt.Sprintf(`{"model":"gpt-5.1","input":"hi","stream":%t}`, stream)))
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Len(t, upstream.requests, 1, "no extra quota probe or retry")
					state := svc.codexCreditState(account)
					require.Equal(t, 20.0, readOpenAIQuotaUsedPercent(state.Quota, "7d"))
					require.NotNil(t, state.Credits)
					require.True(t, state.Credits.HasCredits, "observe credits even with quota remaining and spending disabled")
					require.Zero(t, state.BlockedAt)
					if source == "quota_only" {
						require.Equal(t, oldAt, state.CreditsAt)
					} else {
						require.Greater(t, state.CreditsAt, oldAt)
						require.NotNil(t, state.Credits.Balance)
						require.Equal(t, "42.25", *state.Credits.Balance)
					}
					require.NoError(t, svc.CheckOpenAICreditPolicy(t.Context(), account), "remaining subscription quota does not need credit permission")
				})
			}
		}
	}
}
