//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPrewarmBillingIsNotAnExtraBusinessRequest(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)
	for _, requestCount := range []int{0, 1} {
		input := CostInput{Ctx: context.Background(), Model: "gpt-5.1", Prewarm: true,
			Tokens: UsageTokens{InputTokens: 100, OutputTokens: 20}, RequestCount: requestCount, RateMultiplier: 1,
			Resolver: resolver, Resolved: &ResolvedPricing{Mode: BillingModePerRequest, DefaultPerRequestPrice: .07}}
		cost, err := bs.CalculateCostUnified(input)
		require.NoError(t, err)
		require.Zero(t, cost.TotalCost)
		require.Zero(t, cost.ActualCost)
		require.Equal(t, string(BillingModePerRequest), cost.BillingMode)
		input.Prewarm = false
		cost, err = bs.CalculateCostUnified(input)
		require.NoError(t, err)
		require.InDelta(t, .07, cost.TotalCost, 1e-12, "normal requests retain the existing default count of one")
	}
	// Token pricing keeps real upstream usage; prewarm is not a zero-cost override.
	for _, prewarm := range []bool{false, true} {
		cost, err := bs.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: "gpt-5.1", Prewarm: prewarm,
			Tokens: UsageTokens{InputTokens: 1000, OutputTokens: 200}, RateMultiplier: 1, Resolver: resolver})
		require.NoError(t, err)
		require.Positive(t, cost.TotalCost)
	}
}

func TestPrewarmUsageLogRetainsRealTokensAndMarker(t *testing.T) {
	repo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(repo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{RequestID: "prewarm-usage", Model: "gpt-5.1", Stream: true, Prewarm: true,
			Usage: OpenAIUsage{InputTokens: 1000, OutputTokens: 100}},
		APIKey: &APIKey{ID: 1}, User: &User{ID: 2}, Account: &Account{ID: 3}, PricingAt: time.Now(),
	})
	require.NoError(t, err)
	require.True(t, repo.lastLog.Prewarm)
	require.Equal(t, 1000, repo.lastLog.InputTokens)
	require.Equal(t, 100, repo.lastLog.OutputTokens)
	require.Positive(t, repo.lastLog.ActualCost)
	svc.RecordCyberPolicyUsageLog(context.Background(), CyberPolicyUsageInput{
		APIKey: &APIKey{ID: 1, User: &User{ID: 2}}, Account: &Account{ID: 3}, Model: "gpt-5.1", RequestID: "prewarm-cyber", Prewarm: true,
	})
	require.True(t, repo.lastLog.Prewarm)
	require.Equal(t, RequestTypeCyberBlocked, repo.lastLog.RequestType)
}

func TestPrewarmHTTPBridgeRejectsWithoutGeneratingAndAcceptsFreshFullInput(t *testing.T) {
	for _, kind := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		t.Run(kind, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200,
				Header: http.Header{"Content-Type": {"text/event-stream"}},
				Body:   io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_fresh\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"))}}
			svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}, httpUpstream: upstream}
			a := &Account{ID: 1, Platform: PlatformOpenAI, Type: kind, Credentials: map[string]any{}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			var events [][]byte
			write := func(payload []byte) error { events = append(events, payload); return nil }
			warmup := []byte(`{"type":"response.create","model":"gpt-6-astra","generate":false,"input":[{"role":"user","content":"keep full context"}]}`)
			result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, a, "test-token", warmup, int64(len(warmup)), len(warmup), "gpt-6-astra", "", "", "", "", 1, write)
			require.ErrorIs(t, err, errOpenAIWSHTTPBridgePrewarm)
			require.Nil(t, result)
			require.Empty(t, upstream.requests, "generate=false must not turn into an HTTP generation")
			require.Len(t, events, 1)
			require.Equal(t, "response.failed", gjson.GetBytes(events[0], "type").String())
			require.Equal(t, "unsupported_prewarm", gjson.GetBytes(events[0], "response.error.code").String())
			require.NotEqual(t, "completed", gjson.GetBytes(events[0], "response.status").String())
			// Native client discards failed prewarm sessions and opens a fresh full request.
			full := []byte(`{"type":"response.create","model":"gpt-6-astra","input":[{"role":"user","content":"keep full context"}]}`)
			result, err = svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, a, "test-token", full, int64(len(full)), len(full), "gpt-6-astra", "", "", "", "", 1, write)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, upstream.requests, 1)
			require.Contains(t, string(upstream.lastBody), "keep full context")
			require.False(t, gjson.GetBytes(upstream.lastBody, "previous_response_id").Exists())
		})
	}
}
