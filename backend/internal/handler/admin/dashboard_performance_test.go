package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type dashboardPerformanceRepoStub struct {
	service.UsageLogRepository
	calls int
}

func (r *dashboardPerformanceRepoStub) GetDashboardStats(context.Context) (*usagestats.DashboardStats, error) {
	r.calls++
	return &usagestats.DashboardStats{Rpm: 2, Tpm: 20, ActualCostPerMinute: .0123}, nil
}

func TestDashboardPerformanceCostStatsAndCachedSnapshot(t *testing.T) {
	t.Cleanup(resetDashboardReadCachesForTest)
	resetDashboardReadCachesForTest()
	gin.SetMode(gin.TestMode)
	repo := &dashboardPerformanceRepoStub{}
	handler := NewDashboardHandler(service.NewDashboardService(repo, nil, nil, nil), nil)
	router := gin.New()
	router.GET("/stats", handler.GetStats)
	router.GET("/snapshot", handler.GetSnapshotV2)
	snapshotURL := "/snapshot?include_trend=false&include_model_stats=false&include_group_stats=false&include_users_trend=false"
	for _, path := range []string{"/stats", snapshotURL, snapshotURL} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var response struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
		stats := response.Data
		if path != "/stats" {
			var ok bool
			stats, ok = response.Data["stats"].(map[string]any)
			require.True(t, ok)
		}
		require.Equal(t, float64(2), stats["rpm"])
		require.Equal(t, float64(20), stats["tpm"])
		require.Equal(t, .0123, stats["actual_cost_per_minute"])
	}
	require.Equal(t, 2, repo.calls, "cost shares the existing snapshot cache, not a separate query")
}
