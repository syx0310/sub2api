//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestGroupUsageSnapshotSurvivesConcurrentHistoricalDelete(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	useGroupUsageRepositoryTestTimezone(t, "Asia/Shanghai")
	today := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
	setup := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer func() { _ = setup.Rollback() }()
	_, err := setup.ExecContext(ctx, `
		INSERT INTO groups (id) VALUES (10);
		INSERT INTO users (id) VALUES (1);
		INSERT INTO usage_logs (id, user_id, group_id, actual_cost, created_at) VALUES
			(1, 1, 10, 2, TIMESTAMPTZ '2026-08-12 12:00:00+08'),
			(2, 1, 10, 3, TIMESTAMPTZ '2026-08-13 12:00:00+08'),
			(3, 1, 10, 4, TIMESTAMPTZ '2026-08-14 12:00:00+08');
		INSERT INTO usage_group_daily_rollups (bucket_date, group_id, actual_cost, computed_at) VALUES
			(DATE '2026-08-12', 10, 2, NOW()), (DATE '2026-08-13', 10, 3, NOW());
		UPDATE usage_group_rollup_state SET closed_before = DATE '2026-08-14',
			retained_from = TIMESTAMPTZ '2026-08-12 00:00:00+08' WHERE id = 1;
	`)
	require.NoError(t, err)
	require.NoError(t, setup.Commit())

	reader, err := integrationDB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	require.NoError(t, err)
	defer func() { _ = reader.Rollback() }()
	require.NoError(t, setGroupUsageRollupTriggerSearchPath(ctx, reader, pq.QuoteIdentifier(schema)))
	snapshot := &usageLogRepository{sql: reader}
	state, err := snapshot.readGroupUsageRollupSnapshot(ctx, "Asia/Shanghai", "2026-08-14")
	require.NoError(t, err)
	require.True(t, state.valid)
	require.Equal(t, "2026-08-14", state.closedBefore)

	// The trigger invalidates a historical bucket between the reader's two
	// queries. A single snapshot must retain the pre-delete total, not mix data.
	writer := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer func() { _ = writer.Rollback() }()
	_, err = writer.ExecContext(ctx, "DELETE FROM usage_logs WHERE id = 2")
	require.NoError(t, err)
	require.NoError(t, writer.Commit())
	before, err := snapshot.GetAllGroupUsageSummary(ctx, today)
	require.NoError(t, err)
	require.Len(t, before, 1)
	require.InDelta(t, 9, before[0].TotalCost, 0.0000001)
	require.NoError(t, reader.Commit())

	fresh := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer func() { _ = fresh.Rollback() }()
	current := &usageLogRepository{sql: fresh}
	after, err := current.GetAllGroupUsageSummary(ctx, today)
	require.NoError(t, err)
	require.Len(t, after, 1)
	require.InDelta(t, 6, after[0].TotalCost, 0.0000001)
}
