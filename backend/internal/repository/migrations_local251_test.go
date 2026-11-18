//go:build unit

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestMigrations251UpgradeAndRepeat(t *testing.T) {
	dsn := os.Getenv("SCOPED_ADMIN_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated SCOPED_ADMIN_TEST_DSN")
	}
	base, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = base.Close() })
	// Historical migrations intentionally inspect public; an isolated schema
	// is not a faithful upgrade fixture. Use a separate disposable database.
	database := fmt.Sprintf("merge_251_%d", time.Now().UnixNano())
	_, err = base.Exec(`CREATE DATABASE ` + database)
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn+" dbname="+database)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close(); _, _ = base.Exec(`DROP DATABASE ` + database) })
	old, all := fstest.MapFS{}, fstest.MapFS{}
	entries, err := migrations.FS.ReadDir(".")
	require.NoError(t, err)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		data, err := migrations.FS.ReadFile(entry.Name())
		require.NoError(t, err)
		all[entry.Name()] = &fstest.MapFile{Data: data}
		if !strings.HasPrefix(entry.Name(), "251_") {
			old[entry.Name()] = all[entry.Name()]
		}
	}
	ctx := context.Background()
	require.NoError(t, applyMigrationsFS(ctx, db, old))
	require.NoError(t, applyMigrationsFS(ctx, db, all))
	require.NoError(t, applyMigrationsFS(ctx, db, all))
	for _, name := range []string{"251_group_force_openai_ultrafast.sql", "251_group_usage_rollup_nonblocking_build.sql"} {
		var checksum string
		require.NoError(t, db.QueryRow(`SELECT checksum FROM schema_migrations WHERE filename=$1`, name).Scan(&checksum))
		require.Equal(t, fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(string(all[name].Data))))), checksum)
	}
	var defaultValue string
	require.NoError(t, db.QueryRow(`SELECT column_default FROM information_schema.columns WHERE table_schema='public' AND table_name='groups' AND column_name='force_openai_ultrafast'`).Scan(&defaultValue))
	require.Contains(t, defaultValue, "false", "migration must not opt existing groups into a paid tier")
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='usage_group_rollup_state' AND column_name IN ('expired_before','building_until','build_dirty_from')`).Scan(&count))
	require.Equal(t, 3, count)
}
