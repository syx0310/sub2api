//go:build integration

package repository

import (
	"context"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestMigration238FilesAreDistinctAndIdempotent(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	for _, filename := range []string{"238_opencode_go_platform.sql", "238_purge_unlimited_user_platform_quotas.sql"} {
		var count int
		require.NoError(t, tx.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations WHERE filename = $1", filename).Scan(&count))
		require.Equal(t, 1, count, "migration tracking uses the complete filename, not the numeric prefix")
	}

	var userID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, role, status, balance, concurrency)
VALUES ('migration-238@example.com', 'test-hash', 'user', 'active', 0, 1) RETURNING id
`).Scan(&userID))
	_, err := tx.ExecContext(ctx, `
INSERT INTO user_platform_quotas (user_id, platform, daily_limit_usd, weekly_limit_usd, monthly_limit_usd, daily_usage_usd, deleted_at)
VALUES
 ($1, 'openai', NULL, NULL, NULL, 9, NULL),
 ($1, 'anthropic', 0, NULL, NULL, 8, NULL),
 ($1, 'gemini', NULL, 20, NULL, 7, NULL),
 ($1, 'minimax', NULL, NULL, 30, 6, NULL),
 ($1, 'opencode_go', 10, NULL, NULL, 5, NOW()),
 ($1, 'grok', NULL, NULL, NULL, 4, NOW())
`, userID)
	require.NoError(t, err)
	platformSQL, err := dbmigrations.FS.ReadFile("238_opencode_go_platform.sql")
	require.NoError(t, err)
	purgeSQL, err := dbmigrations.FS.ReadFile("238_purge_unlimited_user_platform_quotas.sql")
	require.NoError(t, err)
	for run := range 2 {
		_, err = tx.ExecContext(ctx, string(platformSQL))
		require.NoError(t, err)
		result, execErr := tx.ExecContext(ctx, string(purgeSQL))
		require.NoError(t, execErr)
		if run == 1 {
			affected, countErr := result.RowsAffected()
			require.NoError(t, countErr)
			require.Zero(t, affected, "second purge is a no-op")
		}
		var count int
		var usage float64
		require.NoError(t, tx.QueryRowContext(ctx, "SELECT count(*), sum(daily_usage_usd) FROM user_platform_quotas WHERE user_id = $1", userID).Scan(&count, &usage))
		require.Equal(t, 4, count, "retain every configured quota, including zero and soft-deleted rows")
		require.Equal(t, float64(26), usage, "retained usage counters must not be reset")
	}
	for _, constraint := range []string{
		"user_platform_quotas_platform_check", "composite_model_routes_target_platform_check",
		"channel_monitors_provider_check", "channel_monitor_request_templates_provider_check",
	} {
		var definition string
		require.NoError(t, tx.QueryRowContext(ctx, "SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = $1", constraint).Scan(&definition))
		require.Contains(t, definition, "opencode_go")
		require.Contains(t, definition, "minimax")
	}
}
