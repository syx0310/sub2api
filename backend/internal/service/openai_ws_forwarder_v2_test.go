package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestForwardOpenAIWSV2_CreditAdmissionAfterHandshake(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name                      string
		allow, prewarm, apiKey    bool
		used, hasCredits, balance string
		wantBlocked               bool
		prewarmQuota              bool
	}{
		{name: "exhausted permission off", used: "100", hasCredits: "true", balance: "42", wantBlocked: true},
		{name: "denied before prewarm too", prewarm: true, used: "100", hasCredits: "true", balance: "42", wantBlocked: true},
		{name: "prewarm reveals exhaustion", prewarm: true, prewarmQuota: true, wantBlocked: true},
		{name: "enabled but empty", allow: true, used: "100", hasCredits: "false", balance: "0", wantBlocked: true},
		{name: "enabled fresh credits", allow: true, used: "100", hasCredits: "true", balance: "42"},
		{name: "subscription still available", used: "20", hasCredits: "false", balance: "0"},
		{name: "quota unknown"},
		{name: "API key is outside OAuth policy", apiKey: true, used: "100", hasCredits: "false", balance: "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			cfg := newOpenAIWSV2TestConfig()
			cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.OpenAIWS.PrewarmGenerateEnabled = tc.prewarm
			headers := make(http.Header)
			if tc.used != "" {
				headers.Set("x-codex-primary-used-percent", tc.used)
				headers.Set("x-codex-primary-window-minutes", "300")
				headers.Set("x-codex-primary-reset-after-seconds", "120")
				headers.Set("x-codex-credits-has-credits", tc.hasCredits)
				headers.Set("x-codex-credits-unlimited", "false")
				headers.Set("x-codex-credits-balance", tc.balance)
			}
			conn := &openAIWSCaptureConn{events: [][]byte{[]byte(`{"type":"response.completed","response":{"id":"resp_credit","status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`)}}
			if tc.prewarmQuota {
				conn.events = append([][]byte{[]byte(`{"type":"codex.rate_limits","rate_limits":{"primary":{"used_percent":100,"window_minutes":300,"reset_after_seconds":120}},"credits":{"has_credits":true,"unlimited":false,"balance":"42"}}`)}, conn.events...)
			}
			dialer := &openAIWSCaptureDialer{conn: conn, handshake: headers}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(dialer)
			t.Cleanup(pool.Close)
			httpRecorder := &httpUpstreamRecorder{}
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: httpRecorder, cache: &stubGatewayCache{}, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(), openaiWSPool: pool}
			account := &Account{ID: 5884, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{"access_token": "test-only"}, Extra: map[string]any{"responses_websockets_v2_enabled": true, CodexAllowCreditUsageKey: tc.allow}}
			if tc.apiKey {
				account.Type = AccountTypeAPIKey
				account.Credentials = map[string]any{"api_key": "test-only"}
			}
			require.NoError(t, svc.CheckOpenAICreditPolicy(t.Context(), account), "quota is not known at initial selection")
			result, err := svc.Forward(t.Context(), c, account, []byte(`{"model":"gpt-5.1","stream":false,"input":"hi"}`))
			require.Equal(t, 1, dialer.DialCount(), "policy denial must not reconnect/replay")
			require.Empty(t, httpRecorder.requests, "policy denial must not fall back to HTTP")
			conn.mu.Lock()
			writes, closed := len(conn.writes), conn.closed
			conn.mu.Unlock()
			if tc.wantBlocked {
				require.Error(t, err)
				require.Equal(t, http.StatusForbidden, infraerrors.Code(err))
				require.Nil(t, result)
				if tc.prewarmQuota {
					require.Equal(t, 1, writes)
					require.Equal(t, false, conn.lastWrite["generate"], "only prewarm, never real generation, was sent")
				} else {
					require.Zero(t, writes, "no upstream generation or prewarm may be sent after denial")
				}
				require.True(t, closed, "denied lease must be released and discarded")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			require.True(t, result.OpenAIWSMode)
			require.Equal(t, 1, writes)
		})
	}
}

// HTTP POST /v1/responses -> forwardOpenAIWSV2 keeps the canonical outbound
// tier separate from response.completed.service_tier for usage-time billing.
func TestForwardOpenAIWSV2_KeepsOutboundAndObservedServiceTiersSeparate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name        string
		requestTier string
		stream      bool
	}{
		{name: "priority_nonstream", requestTier: "priority", stream: false},
		{name: "fast_stream", requestTier: "fast", stream: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Request.Header.Set("User-Agent", "unit-test-agent/1.0")

			cfg := &config.Config{}
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			cfg.Gateway.OpenAIWS.Enabled = true
			cfg.Gateway.OpenAIWS.APIKeyEnabled = true
			cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
			cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
			cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 5
			cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

			captureConn := &openAIWSCaptureConn{
				events: [][]byte{
					[]byte(`{"type":"response.completed","response":{"id":"resp_tier_v2","model":"gpt-5.5","status":"completed","service_tier":"default","usage":{"input_tokens":1,"output_tokens":1}}}`),
				},
			}
			captureDialer := &openAIWSCaptureDialer{conn: captureConn}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(captureDialer)

			svc := &OpenAIGatewayService{
				cfg:              cfg,
				httpUpstream:     &httpUpstreamRecorder{},
				cache:            &stubGatewayCache{},
				openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
				toolCorrector:    NewCodexToolCorrector(),
				openaiWSPool:     pool,
			}
			account := &Account{
				ID:          5882,
				Name:        "openai-ws-v2-tier",
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Status:      StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-test"},
				Extra:       map[string]any{"responses_websockets_v2_enabled": true},
			}

			body := []byte(fmt.Sprintf(
				`{"model":"gpt-5.5","stream":%t,"service_tier":%q,"input":[{"type":"input_text","text":"hi"}]}`,
				tc.stream, tc.requestTier,
			))
			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.True(t, result.OpenAIWSMode, "must take HTTP POST → forwardOpenAIWSV2, not HTTP fallback")
			require.Equal(t, tc.stream, result.Stream)
			require.Equal(t, "resp_tier_v2", result.RequestID)
			require.NotNil(t, result.ServiceTier)
			require.Equal(t, "priority", *result.ServiceTier)
			require.Equal(t, "default", result.UpstreamResponseServiceTier)
			require.Equal(t, "priority", captureConn.lastWrite["service_tier"],
				"outbound WS payload still carries the requested Fast tier")
		})
	}
}

func TestForwardOpenAIWSV2_MarksCyberPolicyForFailureEventShapes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name          string
		upstreamEvent []byte
		wantError     bool
		wantInput     int
		wantOutput    int
	}{
		{
			name:          "error_before_return",
			upstreamEvent: []byte(`{"type":"error","error":{"type":"rate_limit_error","code":"cyber_policy","message":"rate limit exceeded by cyber policy"},"usage":{"input_tokens":5,"output_tokens":1}}`),
			wantError:     true,
			wantInput:     5,
			wantOutput:    1,
		},
		{
			name:          "response_failed_terminal",
			upstreamEvent: []byte(`{"type":"response.failed","response":{"id":"resp_cyber","status":"failed","error":{"code":"cyber_policy","message":"blocked by cyber policy"},"usage":{"input_tokens":9,"output_tokens":2}}}`),
			wantInput:     9,
			wantOutput:    2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)

			cfg := newOpenAIWSV2TestConfig()
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
			captureConn := &openAIWSCaptureConn{events: [][]byte{append([]byte(nil), tt.upstreamEvent...)}}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: captureConn})
			svc := &OpenAIGatewayService{
				cfg:              cfg,
				httpUpstream:     &httpUpstreamRecorder{},
				cache:            &stubGatewayCache{},
				openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
				toolCorrector:    NewCodexToolCorrector(),
				openaiWSPool:     pool,
			}
			account := &Account{
				ID: 5883, Name: "openai-ws-v2-cyber", Platform: PlatformOpenAI,
				Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-test"},
				Extra:       map[string]any{"responses_websockets_v2_enabled": true},
			}

			result, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.5","stream":false,"input":"hello"}`))
			if tt.wantError {
				require.Error(t, err)
				require.Nil(t, result)
				var failoverErr *UpstreamFailoverError
				require.False(t, errors.As(err, &failoverErr))
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
			}

			mark := GetOpsCyberPolicy(c)
			require.NotNil(t, mark)
			require.Equal(t, "cyber_policy", mark.Code)
			require.Equal(t, tt.wantInput, mark.UpstreamInTok)
			require.Equal(t, tt.wantOutput, mark.UpstreamOutTok)
		})
	}
}
