//go:build integration

package repository

import (
	"context"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestV028MigrationsCoexistWithTurnStateLengths(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	var count int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations
WHERE filename IN ('239_add_usage_log_codex_turn_state_lengths.sql', '239_channel_reasoning_effort_multipliers.sql')`).Scan(&count))
	require.Equal(t, 2, count, "migration identity is the full filename, not its numeric prefix")
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns
WHERE table_schema = 'public' AND table_name = 'usage_logs'
AND column_name IN ('codex_turn_state_request_header_bytes', 'codex_turn_state_request_metadata_bytes',
                   'codex_turn_state_response_header_bytes', 'codex_turn_state_response_metadata_bytes')
AND is_nullable = 'YES' AND column_default IS NULL`).Scan(&count))
	require.Equal(t, 4, count, "unknown lengths must remain distinct from observed zero")
}

func TestV028ReasoningMultiplierUpgradeAndReplay(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	// Session-local tables shadow the migrated public tables. This exercises
	// the old schema without changing any shared fixture or business database.
	_, err := tx.ExecContext(ctx, `
CREATE TEMP TABLE channel_model_pricing (id BIGINT PRIMARY KEY, max_reasoning_effort_multiplier NUMERIC);
CREATE TEMP TABLE channel_account_stats_model_pricing (id BIGINT PRIMARY KEY);
CREATE TEMP TABLE groups (id BIGINT PRIMARY KEY, model_pricing JSONB);
INSERT INTO channel_model_pricing VALUES (1, 3), (2, NULL), (3, 1);
INSERT INTO groups VALUES
 (1, '[{"models":["claude-fable-5-1"],"max_reasoning_effort_multiplier":3}]'),
 (2, '[{"max_reasoning_effort_multiplier":3,"reasoning_effort_multipliers":{}}]'),
 (3, '[{"max_reasoning_effort_multiplier":3,"reasoning_effort_multipliers":{"low":2}}]'),
 (4, '[{"max_reasoning_effort_multiplier":null}]');`)
	require.NoError(t, err)
	migration, err := dbmigrations.FS.ReadFile("239_channel_reasoning_effort_multipliers.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migration))
	require.NoError(t, err)

	for id, want := range map[int]string{1: `{"max":3}`, 2: `{}`, 3: `{"max":1}`} {
		var got string
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT reasoning_effort_multipliers FROM channel_model_pricing WHERE id=$1`, id).Scan(&got))
		require.JSONEq(t, want, got)
	}
	_, err = tx.ExecContext(ctx, `UPDATE channel_model_pricing SET reasoning_effort_multipliers='{}' WHERE id=1`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	var cleared string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT reasoning_effort_multipliers FROM channel_model_pricing WHERE id=1`).Scan(&cleared))
	require.JSONEq(t, `{}`, cleared, "replay must not revive a cleared legacy max multiplier")
	for id, want := range map[int]string{
		1: `[{"models":["claude-fable-5-1"],"reasoning_effort_multipliers":{"max":3}}]`,
		2: `[{"reasoning_effort_multipliers":{}}]`,
		3: `[{"reasoning_effort_multipliers":{"low":2}}]`,
		4: `[{}]`,
	} {
		var got string
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT model_pricing FROM groups WHERE id=$1`, id).Scan(&got))
		require.JSONEq(t, want, got)
	}
}
