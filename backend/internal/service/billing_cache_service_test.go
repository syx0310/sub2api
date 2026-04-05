package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type billingCacheWorkerStub struct {
	balanceUpdates      int64
	subscriptionUpdates int64
}

func (b *billingCacheWorkerStub) GetUserBalance(ctx context.Context, userID int64) (float64, error) {
	return 0, errors.New("not implemented")
}

func (b *billingCacheWorkerStub) SetUserBalance(ctx context.Context, userID int64, balance float64) error {
	atomic.AddInt64(&b.balanceUpdates, 1)
	return nil
}

func (b *billingCacheWorkerStub) DeductUserBalance(ctx context.Context, userID int64, amount float64) error {
	atomic.AddInt64(&b.balanceUpdates, 1)
	return nil
}

func (b *billingCacheWorkerStub) InvalidateUserBalance(ctx context.Context, userID int64) error {
	return nil
}

func (b *billingCacheWorkerStub) GetSubscriptionCache(ctx context.Context, userID, groupID int64) (*SubscriptionCacheData, error) {
	return nil, errors.New("not implemented")
}

func (b *billingCacheWorkerStub) SetSubscriptionCache(ctx context.Context, userID, groupID int64, data *SubscriptionCacheData) error {
	atomic.AddInt64(&b.subscriptionUpdates, 1)
	return nil
}

func (b *billingCacheWorkerStub) UpdateSubscriptionUsage(ctx context.Context, userID, groupID int64, cost float64) error {
	atomic.AddInt64(&b.subscriptionUpdates, 1)
	return nil
}

func (b *billingCacheWorkerStub) InvalidateSubscriptionCache(ctx context.Context, userID, groupID int64) error {
	return nil
}

func (b *billingCacheWorkerStub) GetAPIKeyRateLimit(ctx context.Context, keyID int64) (*APIKeyRateLimitCacheData, error) {
	return nil, errors.New("not implemented")
}

func (b *billingCacheWorkerStub) SetAPIKeyRateLimit(ctx context.Context, keyID int64, data *APIKeyRateLimitCacheData) error {
	return nil
}

func (b *billingCacheWorkerStub) UpdateAPIKeyRateLimitUsage(ctx context.Context, keyID int64, cost float64) error {
	return nil
}

func (b *billingCacheWorkerStub) InvalidateAPIKeyRateLimit(ctx context.Context, keyID int64) error {
	return nil
}

func TestBillingCacheServiceQueueHighLoad(t *testing.T) {
	cache := &billingCacheWorkerStub{}
	svc := NewBillingCacheService(cache, nil, nil, nil, &config.Config{})
	t.Cleanup(svc.Stop)

	start := time.Now()
	for i := 0; i < cacheWriteBufferSize*2; i++ {
		svc.QueueDeductBalance(1, 1)
	}
	require.Less(t, time.Since(start), 2*time.Second)

	svc.QueueUpdateSubscriptionUsage(1, 2, 1.5)

	require.Eventually(t, func() bool {
		return atomic.LoadInt64(&cache.balanceUpdates) > 0
	}, 2*time.Second, 10*time.Millisecond)

	require.Eventually(t, func() bool {
		return atomic.LoadInt64(&cache.subscriptionUpdates) > 0
	}, 2*time.Second, 10*time.Millisecond)
}

func TestBillingCacheServiceEnqueueAfterStopReturnsFalse(t *testing.T) {
	cache := &billingCacheWorkerStub{}
	svc := NewBillingCacheService(cache, nil, nil, nil, &config.Config{})
	svc.Stop()

	enqueued := svc.enqueueCacheWrite(cacheWriteTask{
		kind:   cacheWriteDeductBalance,
		userID: 1,
		amount: 1,
	})
	require.False(t, enqueued)
}

func TestRelayModeSkipsBalanceCheck(t *testing.T) {
	cache := &billingCacheWorkerStub{}
	cfg := &config.Config{RunMode: config.RunModeRelay}
	svc := NewBillingCacheService(cache, nil, nil, nil, cfg)
	t.Cleanup(svc.Stop)

	user := &User{ID: 1, Balance: 0} // zero balance
	apiKey := &APIKey{ID: 1}          // no rate limits

	err := svc.CheckBillingEligibility(context.Background(), user, apiKey, nil, nil)
	require.NoError(t, err, "relay mode should skip balance check")
}

func TestRelayModeEnforcesAPIKeyRateLimits(t *testing.T) {
	rateLimitData := &APIKeyRateLimitCacheData{
		Usage1d:  10.0,                    // usage exceeds limit
		Window1d: time.Now().Unix() - 100, // window started recently (not expired)
	}
	rateLimitCache := &rateLimitCacheStub{data: rateLimitData}

	cfg := &config.Config{RunMode: config.RunModeRelay}
	svc := NewBillingCacheService(rateLimitCache, nil, nil, nil, cfg)
	t.Cleanup(svc.Stop)

	user := &User{ID: 1, Balance: 0}
	apiKey := &APIKey{
		ID:          1,
		RateLimit1d: 5.0, // limit is 5, usage is 10 → should be rejected
	}

	err := svc.CheckBillingEligibility(context.Background(), user, apiKey, nil, nil)
	require.Error(t, err, "relay mode should enforce API key rate limits")
}

// rateLimitCacheStub implements BillingCacheWorker with custom rate limit behavior
type rateLimitCacheStub struct {
	billingCacheWorkerStub
	data *APIKeyRateLimitCacheData
}

func (r *rateLimitCacheStub) GetAPIKeyRateLimit(ctx context.Context, keyID int64) (*APIKeyRateLimitCacheData, error) {
	if r.data != nil {
		return r.data, nil
	}
	return nil, errors.New("not found")
}
