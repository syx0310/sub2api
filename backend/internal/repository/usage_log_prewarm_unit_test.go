//go:build unit

package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageLogPrewarmInsertShapes(t *testing.T) {
	log := &service.UsageLog{UserID: 1, APIKeyID: 2, RequestID: "prewarm", Model: "gpt-6-astra", Prewarm: true, CreatedAt: time.Now()}
	prepared := prepareUsageLogInsert(log)
	index := len(prepared.args) - 2
	require.Equal(t, true, prepared.args[index])
	require.Equal(t, "boolean", usageLogInsertArgTypes[index])
	require.Contains(t, usageLogSelectColumns, "prewarm")
	key := usageLogBatchKey(log.RequestID, log.APIKeyID)
	query, _ := buildUsageLogBatchInsertQuery([]string{key}, map[string]usageLogInsertPrepared{key: prepared})
	require.Contains(t, query, "prewarm")
	query, _ = buildUsageLogBestEffortInsertQuery([]usageLogInsertPrepared{prepared})
	require.Contains(t, query, "prewarm")
}
