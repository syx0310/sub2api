//go:build unit

package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type scopedConcurrencyCache struct {
	service.ConcurrencyCache
	calls int
}

func (*scopedConcurrencyCache) TrackAPIKeySlot(context.Context, int64, string) error   { return nil }
func (*scopedConcurrencyCache) ReleaseAPIKeySlot(context.Context, int64, string) error { return nil }
func (s *scopedConcurrencyCache) GetAPIKeyConcurrencyBatch(_ context.Context, ids []int64) (map[int64]int, error) {
	s.calls++
	counts := make(map[int64]int, len(ids))
	for _, id := range ids {
		counts[id] = 3
	}
	return counts, nil
}
func (s *scopedConcurrencyCache) GetActiveAPIKeyConcurrency(context.Context) (*service.APIKeyConcurrencySnapshot, error) {
	s.calls++
	return &service.APIKeyConcurrencySnapshot{Counts: map[int64]int{7: 3}, Complete: true, CollectedAt: time.Now().UTC()}, nil
}

func TestScopedAdminConcurrencyRegisteredRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, permission := range []string{"", "users.read", "ops.read"} {
		t.Run("permission="+permission, func(t *testing.T) {
			keys := &scopedRouteKeys{}
			settings := service.NewSettingService(nil, nil)
			settings.SetScopedAdminKeyRepository(keys)
			policy := adminaccess.Policy{}
			if permission != "" {
				policy.GlobalPermissions = []string{permission}
			}
			key, _, err := settings.CreateScopedAdminKey(t.Context(), service.ScopedAdminKeyInput{Name: "concurrency reader", Enabled: true, Policy: policy}, 9)
			require.NoError(t, err)
			users := service.NewUserService(&scopedRouteUsers{}, nil, nil, nil)
			cache := &scopedConcurrencyCache{}
			h := &handler.Handlers{Admin: &handler.AdminHandlers{
				User:    adminhandler.NewUserHandler(nil, service.NewConcurrencyService(cache), nil, nil, nil, nil, nil),
				Setting: adminhandler.NewSettingHandler(settings, nil, nil, nil, nil, nil, nil),
			}}
			r := gin.New()
			RegisterAdminRoutes(r.Group("/api/v1"), h, middleware.NewAdminAuthMiddleware(nil, users, settings, nil), middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }), middleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() }), nil, nil)
			for _, endpoint := range []struct{ method, path, body string }{
				{http.MethodGet, "/api/v1/admin/api-keys/concurrency", ""},
				{http.MethodPost, "/api/v1/admin/api-keys/concurrency/query", `{"api_key_ids":[7]}`},
			} {
				request := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(endpoint.body))
				request.Header.Set("x-api-key", key)
				request.Header.Set("Content-Type", "application/json")
				recorder := httptest.NewRecorder()
				r.ServeHTTP(recorder, request)
				if permission != "ops.read" {
					require.Equal(t, http.StatusForbidden, recorder.Code)
					continue
				}
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				var response struct {
					Data map[string]json.RawMessage `json:"data"`
				}
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
				require.Len(t, response.Data, 4, "only availability, completeness, timestamp and ID counts")
				require.JSONEq(t, `{"7":3}`, string(response.Data["items"]))
				require.NotContains(t, recorder.Body.String(), key)
			}
			if permission == "ops.read" {
				require.Equal(t, 2, cache.calls)
			} else {
				require.Zero(t, cache.calls)
			}
		})
	}
}
