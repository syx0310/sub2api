package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newAPIKeyConcurrencyTestCache(t *testing.T) (*concurrencyCache, *miniredis.Miniredis) {
	t.Helper()
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache, ok := NewConcurrencyCache(client, 15, 900).(*concurrencyCache)
	require.True(t, ok)
	return cache, redisServer
}

func TestAPIKeyConcurrencyBatch_IsReadOnlyAndIncludesLiveTurns(t *testing.T) {
	cache, _ := newAPIKeyConcurrencyTestCache(t)
	ctx := context.Background()

	require.NoError(t, cache.TrackAPIKeySlot(ctx, 11, "regular-1"))
	acquired, err := cache.AcquireLiveLease(ctx, 101, 5, 201, 5, 11, "live-1", false)
	require.NoError(t, err)
	require.True(t, acquired)

	now, err := cache.rdb.Time(ctx).Result()
	require.NoError(t, err)
	require.NoError(t, cache.rdb.ZAdd(ctx, apiKeySlotKey(12), redis.Z{
		Score:  float64(now.Unix() - int64(cache.slotTTLSeconds) - 1),
		Member: "expired-but-not-cleaned",
	}).Err())

	counts, err := cache.GetAPIKeyConcurrencyBatch(ctx, []int64{11, 12})
	require.NoError(t, err)
	require.Equal(t, map[int64]int{11: 2, 12: 0}, counts)
	remaining, err := cache.rdb.ZCard(ctx, apiKeySlotKey(12)).Result()
	require.NoError(t, err)
	require.Equal(t, int64(1), remaining, "a monitoring read must not mutate slot sets")
}

func TestActiveAPIKeyConcurrency_IsSparseAndReportsWarmup(t *testing.T) {
	cache, redisServer := newAPIKeyConcurrencyTestCache(t)
	ctx := context.Background()

	require.NoError(t, cache.TrackAPIKeySlot(ctx, 21, "regular-1"))
	acquired, err := cache.AcquireOpenAIWSIngressLease(ctx, 22, 1, "idle-ingress")
	require.NoError(t, err)
	require.True(t, acquired)

	snapshot, err := cache.GetActiveAPIKeyConcurrency(ctx)
	require.NoError(t, err)
	require.Equal(t, map[int64]int{21: 1}, snapshot.Counts)
	require.False(t, snapshot.Complete, "the index needs one regular-slot TTL to cover pre-deployment slots")

	require.NoError(t, cache.ReleaseAPIKeySlot(ctx, 21, "regular-1"))
	snapshot, err = cache.GetActiveAPIKeyConcurrency(ctx)
	require.NoError(t, err)
	require.Empty(t, snapshot.Counts, "zero-count candidates must be filtered from the sparse result")

	redisNow, err := cache.rdb.Time(ctx).Result()
	require.NoError(t, err)
	redisServer.SetTime(redisNow.Add(15*time.Minute + time.Second))
	snapshot, err = cache.GetActiveAPIKeyConcurrency(ctx)
	require.NoError(t, err)
	require.Empty(t, snapshot.Counts)
	require.True(t, snapshot.Complete)
	indexed, err := cache.rdb.ZCard(ctx, apiKeyActiveIndexKey).Result()
	require.NoError(t, err)
	require.Equal(t, int64(1), indexed, "a sparse monitoring read must not bulk-delete expired index members")
	require.NoError(t, cache.CleanupExpiredAccountSlotKeys(ctx))
	indexed, err = cache.rdb.ZCard(ctx, apiKeyActiveIndexKey).Result()
	require.NoError(t, err)
	require.Zero(t, indexed, "the bounded background cleanup removes expired index members")
}

var _ service.APIKeyConcurrencySnapshotCache = (*concurrencyCache)(nil)
