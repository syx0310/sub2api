package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type adminAPIKeyConcurrencyCacheStub struct {
	service.ConcurrencyCache
	counts      map[int64]int
	countErr    error
	requested   []int64
	snapshot    *service.APIKeyConcurrencySnapshot
	snapshotErr error
}

func (s *adminAPIKeyConcurrencyCacheStub) TrackAPIKeySlot(context.Context, int64, string) error {
	return nil
}

func (s *adminAPIKeyConcurrencyCacheStub) ReleaseAPIKeySlot(context.Context, int64, string) error {
	return nil
}

func (s *adminAPIKeyConcurrencyCacheStub) GetAPIKeyConcurrencyBatch(_ context.Context, ids []int64) (map[int64]int, error) {
	s.requested = append([]int64(nil), ids...)
	if s.countErr != nil {
		return nil, s.countErr
	}
	result := make(map[int64]int, len(ids))
	for _, id := range ids {
		result[id] = s.counts[id]
	}
	return result, nil
}

func (s *adminAPIKeyConcurrencyCacheStub) GetActiveAPIKeyConcurrency(context.Context) (*service.APIKeyConcurrencySnapshot, error) {
	return s.snapshot, s.snapshotErr
}

func newAPIKeyConcurrencyHandlerRouter(cache *adminAPIKeyConcurrencyCacheStub) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := NewUserHandler(newStubAdminService(), service.NewConcurrencyService(cache), nil, nil, nil, nil, nil)
	router.POST("/api/v1/admin/api-keys/concurrency/query", h.QueryAPIKeyConcurrency)
	router.GET("/api/v1/admin/api-keys/concurrency", h.GetActiveAPIKeyConcurrency)
	router.GET("/api/v1/admin/users/:id/api-keys", h.GetUserAPIKeys)
	return router
}

func decodeAdminResponseData(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	return body.Data
}

func TestQueryAPIKeyConcurrency_DeduplicatesAndReturnsExactCounts(t *testing.T) {
	cache := &adminAPIKeyConcurrencyCacheStub{counts: map[int64]int{1: 3, 2: 0}}
	router := newAPIKeyConcurrencyHandlerRouter(cache)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/api-keys/concurrency/query", bytes.NewBufferString(`{"api_key_ids":[1,1,2]}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, []int64{1, 2}, cache.requested)
	data := decodeAdminResponseData(t, recorder)
	require.Equal(t, true, data["available"])
	require.Equal(t, true, data["complete"])
	require.Equal(t, map[string]any{"1": float64(3), "2": float64(0)}, data["items"])
}

func TestQueryAPIKeyConcurrency_RejectsInvalidAndUnavailableRequests(t *testing.T) {
	t.Run("non-positive ID", func(t *testing.T) {
		router := newAPIKeyConcurrencyHandlerRouter(&adminAPIKeyConcurrencyCacheStub{})
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/api-keys/concurrency/query", bytes.NewBufferString(`{"api_key_ids":[0]}`))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
	})

	t.Run("more than 500 IDs", func(t *testing.T) {
		router := newAPIKeyConcurrencyHandlerRouter(&adminAPIKeyConcurrencyCacheStub{})
		ids := make([]int64, 501)
		for i := range ids {
			ids[i] = int64(i + 1)
		}
		body, err := json.Marshal(map[string]any{"api_key_ids": ids})
		require.NoError(t, err)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/api-keys/concurrency/query", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
	})

	t.Run("redis unavailable", func(t *testing.T) {
		router := newAPIKeyConcurrencyHandlerRouter(&adminAPIKeyConcurrencyCacheStub{countErr: errors.New("redis down")})
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/api-keys/concurrency/query", bytes.NewBufferString(`{"api_key_ids":[1]}`))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		require.Contains(t, recorder.Body.String(), "API_KEY_CONCURRENCY_UNAVAILABLE")
	})
}

func TestGetActiveAPIKeyConcurrency_ReturnsSparseSnapshot(t *testing.T) {
	collectedAt := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	router := newAPIKeyConcurrencyHandlerRouter(&adminAPIKeyConcurrencyCacheStub{snapshot: &service.APIKeyConcurrencySnapshot{
		Counts:      map[int64]int{7: 2},
		Complete:    false,
		CollectedAt: collectedAt,
	}})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/api-keys/concurrency", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	data := decodeAdminResponseData(t, recorder)
	require.Equal(t, false, data["complete"])
	require.Equal(t, map[string]any{"7": float64(2)}, data["items"])
	require.Equal(t, collectedAt.Format(time.RFC3339), data["collected_at"])
}

func TestGetUserAPIKeys_OverlaysCurrentPageConcurrency(t *testing.T) {
	cache := &adminAPIKeyConcurrencyCacheStub{counts: map[int64]int{10: 4}}
	router := newAPIKeyConcurrencyHandlerRouter(cache)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users/1/api-keys", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, []int64{10}, cache.requested)
	data := decodeAdminResponseData(t, recorder)
	items := data["items"].([]any)
	require.Equal(t, float64(4), items[0].(map[string]any)["current_concurrency"])
}
