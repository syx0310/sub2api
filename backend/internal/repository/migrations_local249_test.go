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

func TestMigrations249MCPAndIngressCoexistPostgres(t *testing.T) {
	dsn := os.Getenv("SCOPED_ADMIN_TEST_DSN")
	if dsn == "" {
		t.Skip("需要独立 SCOPED_ADMIN_TEST_DSN")
	}
	base, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = base.Close() })
	const ingressFile = "249_request_ingress_observation.sql"
	const mcpFile = "249_mcp_oauth.sql"
	ingress, err := migrations.FS.ReadFile(ingressFile)
	require.NoError(t, err)
	mcp, err := migrations.FS.ReadFile(mcpFile)
	require.NoError(t, err)
	for _, upgrade := range []bool{false, true} {
		t.Run(fmt.Sprintf("local_249_already_applied=%t", upgrade), func(t *testing.T) {
			schema := fmt.Sprintf("merge_249_%d", time.Now().UnixNano())
			_, err := base.Exec(`CREATE SCHEMA ` + schema)
			require.NoError(t, err)
			db, err := sql.Open("postgres", dsn+" search_path="+schema)
			require.NoError(t, err)
			t.Cleanup(func() {
				_ = db.Close()
				_, _ = base.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
			})
			_, err = db.Exec(`CREATE TABLE users(id BIGINT PRIMARY KEY);
				CREATE TABLE usage_logs(id BIGINT PRIMARY KEY, codex_turn_state_request_header_bytes BIGINT);
				CREATE TABLE ops_error_logs(id BIGINT PRIMARY KEY);
				INSERT INTO usage_logs VALUES (1, 42)`)
			require.NoError(t, err)
			ctx := context.Background()
			if upgrade {
				require.NoError(t, applyMigrationsFS(ctx, db, fstest.MapFS{ingressFile: {Data: ingress}}))
				_, err = db.Exec(`UPDATE usage_logs SET request_ingress='{"host":"old.example","source":"host","peer_ip":"10.0.0.1"}'`)
				require.NoError(t, err)
			}
			all := fstest.MapFS{ingressFile: {Data: ingress}, mcpFile: {Data: mcp}}
			require.NoError(t, applyMigrationsFS(ctx, db, all))
			require.NoError(t, applyMigrationsFS(ctx, db, all))
			var count int
			require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count))
			require.Equal(t, 2, count, "full filename, not prefix/max version, identifies a migration")
			for name, body := range map[string][]byte{ingressFile: ingress, mcpFile: mcp} {
				var checksum string
				require.NoError(t, db.QueryRow(`SELECT checksum FROM schema_migrations WHERE filename=$1`, name).Scan(&checksum))
				require.Equal(t, fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(string(body))))), checksum)
			}
			for _, table := range []string{"mcp_oauth_grants", "mcp_oauth_codes", "mcp_oauth_credentials"} {
				require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM `+table).Scan(&count))
				require.Zero(t, count, "migration must not grant access")
			}
			var legacy int64
			var host sql.NullString
			require.NoError(t, db.QueryRow(`SELECT codex_turn_state_request_header_bytes, request_ingress->>'host' FROM usage_logs WHERE id=1`).Scan(&legacy, &host))
			require.EqualValues(t, 42, legacy, "historical observer data remains intact for rollback")
			require.Equal(t, upgrade, host.Valid)
			if upgrade {
				require.Equal(t, "old.example", host.String)
			}
		})
	}
}
