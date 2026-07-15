//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func assertActualCostInvariant(t *testing.T, total, input, output, cacheCreation, cacheRead, other float64) {
	t.Helper()
	require.InDelta(t, total, input+output+cacheCreation+cacheRead+other, 1e-9)
}

func TestUsageLogActualCostBreakdownAggregatesAndFilters(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newUsageLogRepositoryWithSQL(client, tx)

	user := mustCreateUser(t, client, &service.User{Email: "actual-cost-aggregate@example.com"})
	keyA := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-actual-a-" + uuid.NewString(), Name: "a"})
	keyB := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-actual-b-" + uuid.NewString(), Name: "b"})
	account := mustCreateAccount(t, client, &service.Account{Name: "actual-cost-" + uuid.NewString()})
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	create := func(log *service.UsageLog) {
		t.Helper()
		log.UserID = user.ID
		log.AccountID = account.ID
		log.RequestID = uuid.NewString()
		_, err := repo.Create(ctx, log)
		require.NoError(t, err)
	}

	create(&service.UsageLog{
		APIKeyID: keyA.ID, Model: "model-a", RequestedModel: "model-a",
		InputTokens: 100, OutputTokens: 50, CacheCreationTokens: 20, CacheReadTokens: 10,
		InputCost: 2, OutputCost: 1, CacheCreationCost: 0.5, CacheReadCost: 0.5,
		ImageOutputCost: 1, TotalCost: 5, ActualCost: 10,
		RequestType: service.RequestTypeSync, CreatedAt: base,
	})
	create(&service.UsageLog{
		APIKeyID: keyA.ID, Model: "model-a", RequestedModel: "model-a",
		InputTokens: 50, OutputTokens: 25,
		InputCost: 1, OutputCost: 3, TotalCost: 4, ActualCost: 2,
		RequestType: service.RequestTypeStream, Stream: true, CreatedAt: base.Add(time.Hour),
	})
	create(&service.UsageLog{
		APIKeyID: keyB.ID, Model: "model-b", RequestedModel: "model-b",
		InputTokens: 10, InputCost: 7, TotalCost: 0, ActualCost: 3,
		RequestType: service.RequestTypeSync, CreatedAt: base.Add(2 * time.Hour),
	})
	create(&service.UsageLog{
		APIKeyID: keyA.ID, Model: "outside", RequestedModel: "outside",
		InputTokens: 999, InputCost: 1, TotalCost: 1, ActualCost: 1,
		RequestType: service.RequestTypeSync, CreatedAt: base.Add(24 * time.Hour),
	})

	start := base
	end := base.Add(24 * time.Hour)
	stats, err := repo.GetStatsWithFilters(ctx, usagestats.UsageLogFilters{
		UserID: user.ID, StartTime: &start, EndTime: &end,
	})
	require.NoError(t, err)
	require.Equal(t, int64(3), stats.TotalRequests)
	require.InDelta(t, 4.5, stats.InputActualCost, 1e-9)
	require.InDelta(t, 3.5, stats.OutputActualCost, 1e-9)
	require.InDelta(t, 1.0, stats.CacheCreationActualCost, 1e-9)
	require.InDelta(t, 1.0, stats.CacheReadActualCost, 1e-9)
	require.InDelta(t, 5.0, stats.OtherActualCost, 1e-9)
	require.InDelta(t, 15.0, stats.TotalActualCost, 1e-9)
	assertActualCostInvariant(t, stats.TotalActualCost, stats.InputActualCost, stats.OutputActualCost, stats.CacheCreationActualCost, stats.CacheReadActualCost, stats.OtherActualCost)

	keyStats, err := repo.GetStatsWithFilters(ctx, usagestats.UsageLogFilters{
		APIKeyID: keyA.ID, StartTime: &start, EndTime: &end,
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), keyStats.TotalRequests)
	require.InDelta(t, 12.0, keyStats.TotalActualCost, 1e-9)
	require.InDelta(t, 2.0, keyStats.OtherActualCost, 1e-9)

	requestType := int16(service.RequestTypeSync)
	syncStats, err := repo.GetStatsWithFilters(ctx, usagestats.UsageLogFilters{
		UserID: user.ID, RequestType: &requestType, StartTime: &start, EndTime: &end,
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), syncStats.TotalRequests)
	require.InDelta(t, 13.0, syncStats.TotalActualCost, 1e-9)
	require.InDelta(t, 5.0, syncStats.OtherActualCost, 1e-9)

	models, err := repo.GetModelStatsWithUsageFiltersBySource(ctx, start, end, usagestats.UsageLogFilters{UserID: user.ID}, usagestats.ModelSourceRequested)
	require.NoError(t, err)
	require.Len(t, models, 2)
	require.Equal(t, "model-a", models[0].Model)
	require.InDelta(t, 12.0, models[0].ActualCost, 1e-9)
	assertActualCostInvariant(t, models[0].ActualCost, models[0].InputActualCost, models[0].OutputActualCost, models[0].CacheCreationActualCost, models[0].CacheReadActualCost, models[0].OtherActualCost)
	require.Equal(t, "model-b", models[1].Model)
	require.InDelta(t, 3.0, models[1].OtherActualCost, 1e-9)

	filteredModels, err := repo.GetModelStatsWithUsageFiltersBySource(ctx, start, end, usagestats.UsageLogFilters{
		UserID: user.ID, Model: "model-b",
	}, usagestats.ModelSourceRequested)
	require.NoError(t, err)
	require.Len(t, filteredModels, 1)
	require.Equal(t, "model-b", filteredModels[0].Model)
}

func TestUsageLogActualCostBreakdownNullableAndPrecision(t *testing.T) {
	ctx := context.Background()
	expressions := buildActualCostBreakdownSQL("", "actual_cost")
	query := fmt.Sprintf(`
		WITH usage_rows(input_cost, output_cost, cache_creation_cost, cache_read_cost, total_cost, actual_cost) AS (
			VALUES
				(NULL::numeric, NULL::numeric, NULL::numeric, NULL::numeric, NULL::numeric, 1.25::numeric),
				(0.1::numeric, 0::numeric, 0::numeric, 0::numeric, 0.3::numeric, 0.2::numeric),
				(0::numeric, 0::numeric, 0::numeric, 0::numeric, 0::numeric, 0::numeric)
		)
		SELECT %s, %s, %s, %s, %s FROM usage_rows
	`, expressions.Input, expressions.Output, expressions.CacheCreation, expressions.CacheRead, expressions.Other)

	var input, output, cacheCreation, cacheRead, other float64
	require.NoError(t, scanSingleRow(ctx, integrationDB, query, nil, &input, &output, &cacheCreation, &cacheRead, &other))
	require.InDelta(t, 1.0/15.0, input, 1e-12)
	require.Zero(t, output)
	require.Zero(t, cacheCreation)
	require.Zero(t, cacheRead)
	require.InDelta(t, 1.25+2.0/15.0, other, 1e-12)
	assertActualCostInvariant(t, 1.45, input, output, cacheCreation, cacheRead, other)
}

func TestUsageLogActualCostBreakdownNormalizesHistoricalAnomalies(t *testing.T) {
	ctx := context.Background()
	expressions := buildActualCostBreakdownSQL("", "actual_cost")
	query := fmt.Sprintf(`
		WITH usage_rows(input_cost, output_cost, cache_creation_cost, cache_read_cost, total_cost, actual_cost) AS (
			VALUES
				(2::numeric, 0::numeric, 0::numeric, 0::numeric, 1::numeric, 1::numeric),
				(1::numeric, 1::numeric, 0::numeric, 0::numeric, 2::numeric, -0.5::numeric),
				(0.6::numeric, 0.6::numeric, 0::numeric, 0::numeric, 1::numeric, 1::numeric)
		)
		SELECT %s, %s, %s, %s, %s FROM usage_rows
	`, expressions.Input, expressions.Output, expressions.CacheCreation, expressions.CacheRead, expressions.Other)

	var input, output, cacheCreation, cacheRead, other float64
	require.NoError(t, scanSingleRow(ctx, integrationDB, query, nil, &input, &output, &cacheCreation, &cacheRead, &other))
	require.InDelta(t, 1.5, input, 1e-12)
	require.InDelta(t, 0.5, output, 1e-12)
	require.Zero(t, cacheCreation)
	require.Zero(t, cacheRead)
	require.InDelta(t, -0.5, other, 1e-12)
	assertActualCostInvariant(t, 1.5, input, output, cacheCreation, cacheRead, other)
}

func TestModelActualCostBreakdownPreservesAccountScopeSemantics(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newUsageLogRepositoryWithSQL(client, tx)

	user := mustCreateUser(t, client, &service.User{Email: "actual-cost-account@example.com"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-account-" + uuid.NewString(), Name: "account"})
	account := mustCreateAccount(t, client, &service.Account{Name: "actual-account-" + uuid.NewString()})
	rate := 2.0
	base := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	_, err := repo.Create(ctx, &service.UsageLog{
		UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, RequestID: uuid.NewString(),
		Model: "account-model", RequestedModel: "account-model",
		InputCost: 1, OutputCost: 3, TotalCost: 4, ActualCost: 2,
		AccountRateMultiplier: &rate, CreatedAt: base,
	})
	require.NoError(t, err)

	models, err := repo.GetModelStatsWithFilters(ctx, base, base.Add(time.Hour), 0, 0, account.ID, 0, nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.InDelta(t, 8.0, models[0].ActualCost, 1e-9)
	require.InDelta(t, 2.0, models[0].InputActualCost, 1e-9)
	require.InDelta(t, 6.0, models[0].OutputActualCost, 1e-9)
	assertActualCostInvariant(t, models[0].ActualCost, models[0].InputActualCost, models[0].OutputActualCost, models[0].CacheCreationActualCost, models[0].CacheReadActualCost, models[0].OtherActualCost)
}
