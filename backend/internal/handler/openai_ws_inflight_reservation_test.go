package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIWSInflightTurnLifecycleAndBillingHandoff(t *testing.T) {
	cache := newHandlerInflightCache(1)
	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
	billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	key := &service.APIKey{User: &service.User{ID: 1}}
	var turn openAIWSInflightTurn
	defer turn.close()
	reserve := func() (context.Context, func(), error) {
		return reserveInflightBalanceCtx(context.Background(), billing, &countingEstimator{cost: .7, priced: true}, key, nil, tokenInflightEstimate("test", nil))
	}
	require.NoError(t, turn.begin(1, reserve))
	require.NoError(t, turn.begin(1, func() (context.Context, func(), error) {
		t.Fatal("transparent retries must not reserve twice")
		return nil, nil, nil
	}))
	require.Equal(t, 1, cache.count())
	ctx, handlerDone := turn.take(1, context.Background())
	task, abandon := wrapUsageRecordTaskContext(ctx, func(context.Context) {})
	handlerDone()
	require.Equal(t, 1, cache.count(), "async billing still owns the reservation")
	require.ErrorIs(t, turn.begin(2, reserve), service.ErrInsufficientBalance)
	task(context.Background())
	abandon()
	require.Zero(t, cache.count(), "idle connection holds no reservation after billing")
	require.NoError(t, turn.begin(2, reserve))
	turn.close() // Disconnect or failure before AfterTurn.
	turn.close()
	require.Zero(t, cache.count())
}

type observedWSInflightCache struct {
	*handlerInflightCache
	reserves atomic.Int64
}

func (c *observedWSInflightCache) ReserveInflightBalance(ctx context.Context, userID int64, id string, amount, balance float64, ttl time.Duration) (bool, float64, error) {
	allowed, sum, err := c.handlerInflightCache.ReserveInflightBalance(ctx, userID, id, amount, balance, ttl)
	if allowed && err == nil {
		c.reserves.Add(1)
	}
	return allowed, sum, err
}

func TestOpenAIResponsesWebSocket_InflightPerTurnNotPerConnection(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModeCtxPool, service.OpenAIWSIngressModePassthrough} {
		t.Run(mode, func(t *testing.T) {
			cache := &observedWSInflightCache{handlerInflightCache: newHandlerInflightCache(10)}
			got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
				ingressMode: mode, inflightCache: cache,
				firstPayload:  `{"type":"response.create","model":"gpt-6-astra","generate":false,"store":false,"input":[]}`,
				secondPayload: `{"type":"response.create","model":"gpt-6-astra","reasoning":{"effort":"none"},"service_tier":"ultrafast","max_output_tokens":1000,"store":false,"input":[{"role":"user","content":"hi"}]}`,
				thirdPayload:  `{"type":"response.create","model":"gpt-6.1-sol","reasoning":{"effort":"max"},"max_output_tokens":2000,"store":false,"input":[{"role":"user","content":"next"}],"client_metadata":{"x-codex-turn-metadata":"{\"request_kind\":\"compaction\"}"}}`,
				onUpstreamFrame: func(payload []byte) error {
					cache.mu.Lock()
					defer cache.mu.Unlock()
					if gjson.GetBytes(payload, "generate").Exists() && !gjson.GetBytes(payload, "generate").Bool() {
						if len(cache.res) != 0 {
							return fmt.Errorf("prewarm reserved funds: %v", cache.res)
						}
						return nil
					}
					if len(cache.res) != 1 {
						return fmt.Errorf("executing turn reservations = %d, want 1", len(cache.res))
					}
					inputPrice, outputPrice := 2e-6, 10e-6
					if gjson.GetBytes(payload, "model").String() == "gpt-6-astra" {
						inputPrice, outputPrice = 60e-6, 300e-6
					}
					// ctx_pool's wsjson Encoder adds a transport newline; admission
					// estimates the JSON payload, not that serialization delimiter.
					bodyBytes := len(bytes.TrimSuffix(payload, []byte("\n")))
					want := float64(bodyBytes/4)*inputPrice + float64(gjson.GetBytes(payload, "max_output_tokens").Int())*outputPrice
					for _, amount := range cache.res {
						if amount < want-1e-9 || amount > want+1e-9 {
							return fmt.Errorf("reservation %.9f != final outgoing estimate %.9f", amount, want)
						}
					}
					return nil
				},
				afterTurn: func(int) {
					require.Eventually(t, func() bool { return cache.count() == 0 }, time.Second, time.Millisecond, "idle connection must not reserve funds")
				},
			})
			require.Len(t, got.upstreamPayloads, 3)
			require.EqualValues(t, 2, cache.reserves.Load(), "prewarm skipped; two executions reserve separately")
			require.Zero(t, cache.count())
		})
	}
}

func TestOpenAIResponsesWebSocket_InflightControlFrameAndSpentBalance(t *testing.T) {
	cache := &observedWSInflightCache{handlerInflightCache: newHandlerInflightCache(10)}
	runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		ingressMode: service.OpenAIWSIngressModePassthrough, inflightCache: cache,
		firstPayload:            `{"type":"response.create","model":"gpt-6.1-sol","input":[],"max_output_tokens":100}`,
		midPayload:              `{"type":"session.update","session":{"model":"gpt-6.1-sol"}}`,
		secondPayload:           `{"type":"response.create","input":[],"max_output_tokens":100}`,
		secondTurnCloseExpected: true, closeReason: "billing check failed",
		afterTurn: func(int) {
			require.Eventually(t, func() bool { return cache.count() == 0 }, time.Second, time.Millisecond)
			cache.mu.Lock()
			cache.balance = 0
			cache.mu.Unlock()
		},
		onUpstreamFrame: func(payload []byte) error {
			if gjson.GetBytes(payload, "type").String() == "session.update" && cache.count() != 0 {
				return errors.New("control frame reserved funds")
			}
			return nil
		},
	})
	require.EqualValues(t, 1, cache.reserves.Load())
	require.Zero(t, cache.count())
}

func TestOpenAIWSInflightTurnAbandonPanicAndConcurrentClose(t *testing.T) {
	for _, mode := range []string{"drop", "panic", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			cache := newHandlerInflightCache(1)
			cfg := &config.Config{}
			cfg.Billing.InflightReservation.Enabled = true
			billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			var turn openAIWSInflightTurn
			require.NoError(t, turn.begin(1, func() (context.Context, func(), error) {
				return reserveInflightBalanceCtx(context.Background(), billing, &countingEstimator{cost: .5, priced: true}, &service.APIKey{User: &service.User{ID: 1}}, nil, tokenInflightEstimate("test", nil))
			}))
			ctx, done := turn.take(1, context.Background())
			task, abandon := wrapUsageRecordTaskContext(ctx, func(context.Context) { panic("billing failure") })
			done()
			if mode == "panic" {
				require.Panics(t, func() { task(context.Background()) })
			} else {
				abandon()
			}
			var wg sync.WaitGroup
			for i := 0; i < 4; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); turn.close(); abandon() }()
			}
			wg.Wait()
			require.Zero(t, cache.count())
			require.ErrorIs(t, turn.begin(2, func() (context.Context, func(), error) { return nil, nil, context.Canceled }), context.Canceled)
			require.NoError(t, turn.begin(2, func() (context.Context, func(), error) { return context.Background(), inflightNoop, nil }))
			require.Error(t, turn.begin(3, func() (context.Context, func(), error) {
				return nil, nil, errors.New("must not acquire overlapping turn")
			}))
			turn.close()
		})
	}
}
