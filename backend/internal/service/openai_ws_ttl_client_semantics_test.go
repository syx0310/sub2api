//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Feed real forwarding results into the production accounting entry point.
// Fixed operator rates make the debit assertion independent of model catalogs.
func assertOpenAITTLUsageRecorded(t *testing.T, result *OpenAIForwardResult) {
	t.Helper()
	const groupID int64 = 777
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
	pricing := ChannelModelPricing{
		Platform: PlatformOpenAI, Models: []string{result.Model}, BillingMode: BillingModeToken,
		InputPrice: float64Ptr(1e-6), OutputPrice: float64Ptr(2e-6), CacheReadPrice: float64Ptr(0.1e-6),
		CacheWritePrice: float64Ptr(3e-6), CacheWrite12hPrice: float64Ptr(6e-6),
	}
	channel := Channel{ID: 1, Status: StatusActive, GroupIDs: []int64{groupID}, ModelPricing: []ChannelModelPricing{pricing}, ApplyPricingToAccountStats: true}
	svc.channelService = &ChannelService{}
	svc.channelService.cache.Store(populateChannelCache([]Channel{channel}, map[int64]string{groupID: PlatformOpenAI}))
	svc.resolver = NewModelPricingResolver(svc.channelService, svc.billingService)
	require.NoError(t, svc.RecordUsage(t.Context(), &OpenAIRecordUsageInput{
		Result: result,
		APIKey: &APIKey{ID: 1, GroupID: i64p(groupID), Group: &Group{ID: groupID, Platform: PlatformOpenAI, RateMultiplier: 0.5}},
		User:   &User{ID: 1}, Account: &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		ChannelUsageFields: ChannelUsageFields{OriginalModel: result.Model, BillingModelSource: BillingModelSourceRequested},
	}))
	log := usageRepo.lastLog
	require.NotNil(t, log)
	u := result.Usage
	cacheCost := float64(u.CacheCreationInputTokens) * 3e-6
	if u.CacheCreationInputTokens > 0 && u.CacheCreation30mInputTokens+u.CacheCreation12hInputTokens == u.CacheCreationInputTokens {
		cacheCost = float64(u.CacheCreation30mInputTokens)*3e-6 + float64(u.CacheCreation12hInputTokens)*6e-6
	}
	want := float64(max(u.InputTokens-u.CacheCreationInputTokens-u.CacheReadInputTokens, 0))*1e-6 + float64(u.OutputTokens)*2e-6 + float64(u.CacheReadInputTokens)*0.1e-6 + cacheCost
	require.Equal(t, u.CacheCreation30mInputTokens, log.CacheCreation30mTokens)
	require.Equal(t, u.CacheCreation12hInputTokens, log.CacheCreation12hTokens)
	require.InDelta(t, cacheCost, log.CacheCreationCost, 1e-12)
	require.InDelta(t, want*0.5, log.ActualCost, 1e-12)
	require.InDelta(t, want*0.5, userRepo.lastAmount, 1e-12)
	require.NotNil(t, log.AccountStatsCost)
	require.InDelta(t, want, *log.AccountStatsCost, 1e-12)
}

func TestHTTPToWSCacheWriteTTLAndTTFT(t *testing.T) {
	for _, mode := range []string{OpenAITTFTModeSemantic, OpenAITTFTModeVisible} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", mode, stream), func(t *testing.T) {
				gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{openAITTFTMode: mode, expiresAt: time.Now().Add(time.Minute).UnixNano()})
				t.Cleanup(func() { gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{}) })
				cfg := newOpenAIWSV2TestConfig()
				cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
				cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
				cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
				conn := &openAIWSCaptureConn{events: [][]byte{
					[]byte(`{"type":"codex.rate_limits","rate_limits":{"primary":{"used_percent":1}}}`),
					[]byte(`{"type":"response.output_item.added","item":{"id":"rs_ttl","type":"reasoning","summary":[]}}`),
					[]byte(`{"type":"response.output_text.delta","delta":"ok"}`),
					[]byte(`{"type":"response.completed","response":{"id":"resp_ttl_ws","model":"gpt-6.1-terra","output":[],"usage":{"input_tokens":101,"output_tokens":2,"input_tokens_details":{"cache_write_tokens":100,"cache_write_tokens_details":{"ttl_30m_tokens":40,"ttl_12h_tokens":60}}}}}`),
				}, readDelays: []time.Duration{0, 20 * time.Millisecond, 120 * time.Millisecond, 0}}
				pool := newOpenAIWSConnPool(cfg)
				pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: conn})
				t.Cleanup(pool.Close)
				svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: &httpUpstreamRecorder{err: errors.New("unexpected HTTP fallback")}, cache: &stubGatewayCache{}, openaiWSPool: pool, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector()}
				account := passthroughLifecycleAccount()
				account.Extra["openai_apikey_responses_websockets_v2_mode"] = OpenAIWSIngressModeCtxPool
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				result, err := svc.Forward(t.Context(), c, account, []byte(fmt.Sprintf(`{"model":"gpt-6.1-terra","stream":%t,"prompt_cache_options":{"ttl":"12h"},"input":[{"role":"user","content":"hi"}]}`, stream)))
				require.NoError(t, err)
				require.True(t, result.OpenAIWSMode)
				require.Equal(t, 40, result.Usage.CacheCreation30mInputTokens)
				require.Equal(t, 60, result.Usage.CacheCreation12hInputTokens)
				require.NotNil(t, result.FirstTokenMs)
				if mode == OpenAITTFTModeSemantic {
					require.Less(t, *result.FirstTokenMs, 120)
				} else {
					require.GreaterOrEqual(t, *result.FirstTokenMs, 140)
				}
				assertOpenAITTLUsageRecorded(t, result)
			})
		}
	}
}

// Codex main writes >1 MiB requests as one text message in bounded frames,
// including frames split inside a UTF-8 character. These are not extra turns.
func TestCodexFragmentedCreateIsOneRequest(t *testing.T) {
	for _, mode := range []string{OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			upstream := newStagedPassthroughConn()
			cfg := passthroughLifecycleConfig()
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 5
			svc := newPassthroughLifecycleService(cfg, upstream)
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(&stagedPassthroughDialer{conn: &instructionsEchoStagedConn{upstream}})
			svc.openaiWSPool = pool
			t.Cleanup(pool.Close)
			account := passthroughLifecycleAccount()
			account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
			results := make(chan *OpenAIForwardResult, 2)
			server, serverErr := startPassthroughHookRecordingServer(t, ctx, svc, account, &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, _ error) {
				if result != nil {
					results <- result
				}
			}})
			defer server.Close()
			client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			require.NoError(t, err)
			defer func() { _ = client.CloseNow() }()
			prefix := `{"type":"response.create","model":"gpt-6.1-sol","input":[{"role":"user","content":"`
			text := strings.Repeat("a", (1<<20)-len(prefix)-1) + "🌱" + strings.Repeat("b", 65536)
			body := []byte(prefix + text + `"}]}`)
			writer, err := client.Writer(ctx, coderws.MessageText)
			require.NoError(t, err)
			_, err = writer.Write(body[:1<<20])
			require.NoError(t, err)
			_, err = writer.Write(body[1<<20:])
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			forwarded := requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
			require.Equal(t, text, gjson.GetBytes(forwarded, "input.0.content").String())
			upstream.Send(`{"type":"response.completed","response":{"id":"resp_fragments","usage":{"input_tokens":1,"output_tokens":1}}}`)
			_, err = readPassthroughLifecycleFrame(t, client, 3*time.Second)
			require.NoError(t, err)
			select {
			case result := <-results:
				require.NotNil(t, result.RequestBodyBytes)
				require.EqualValues(t, len(body), *result.RequestBodyBytes)
			case <-ctx.Done():
				t.Fatal("missing single-turn result")
			}
			_ = client.Close(coderws.StatusNormalClosure, "done")
			select {
			case <-serverErr:
			case <-ctx.Done():
				t.Fatal("relay did not exit")
			}
			require.Empty(t, results, "one fragmented message must be accounted only once")
		})
	}
}
