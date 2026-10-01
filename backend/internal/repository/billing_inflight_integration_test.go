//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestInflightReservationRealRedisAtomicLifecycle(t *testing.T) {
	rdb := testRedis(t)
	cache := &billingCache{rdb: rdb}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const requests = 16
	type result struct {
		id      string
		allowed bool
		err     error
	}
	results := make(chan result, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("request-%d", i)
			allowed, _, err := cache.ReserveInflightBalance(ctx, 1, id, .3, 1, time.Minute)
			results <- result{id: id, allowed: allowed, err: err}
		}()
	}
	wg.Wait()
	close(results)
	admitted := []string{}
	for result := range results {
		require.NoError(t, result.err)
		if result.allowed {
			admitted = append(admitted, result.id)
		}
	}
	require.Len(t, admitted, 3, "atomic reservations must not oversubscribe the supplied balance")
	zkey, hkey := billingInflightKeys(1)
	require.EqualValues(t, 3, rdb.ZCard(ctx, zkey).Val())
	require.EqualValues(t, 3, rdb.HLen(ctx, hkey).Val())
	require.True(t, rdb.HExists(ctx, hkey, admitted[0]).Val())
	alive, err := cache.RenewInflightBalance(ctx, 1, admitted[0], time.Minute)
	require.NoError(t, err)
	require.True(t, alive)
	// Set only this isolated member's deadline into the past; avoid sleeps and
	// assert that renewal cannot resurrect it or leave its amount behind.
	require.NoError(t, rdb.ZAdd(ctx, zkey, redis.Z{Member: admitted[0], Score: 1}).Err())
	alive, err = cache.RenewInflightBalance(ctx, 1, admitted[0], time.Minute)
	require.NoError(t, err)
	require.False(t, alive)
	require.False(t, rdb.HExists(ctx, hkey, admitted[0]).Val())
	for _, id := range admitted {
		require.NoError(t, cache.ReleaseInflightBalance(ctx, 1, id))
		require.NoError(t, cache.ReleaseInflightBalance(ctx, 1, id))
	}
	require.Zero(t, rdb.ZCard(ctx, zkey).Val())
	require.Zero(t, rdb.HLen(ctx, hkey).Val())
}

func TestAPIKeyCreateCountRealRedisKeepsFixedWindow(t *testing.T) {
	rdb := testRedis(t)
	cache := &apiKeyCache{rdb: rdb}
	ctx := context.Background()
	count, err := cache.IncrementCreateCount(ctx, 1, time.Hour)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	key := apiKeyCreateCountKey(1)
	require.NoError(t, rdb.PExpire(ctx, key, time.Minute).Err())
	count, err = cache.IncrementCreateCount(ctx, 1, time.Hour)
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
	ttl, err := rdb.PTTL(ctx, key).Result()
	require.NoError(t, err)
	require.Positive(t, ttl)
	require.LessOrEqual(t, ttl, time.Minute, "a subsequent create must not restart the hour window")
}
