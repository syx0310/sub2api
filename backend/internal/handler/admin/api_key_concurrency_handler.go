package admin

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

const maxAPIKeyConcurrencyQueryIDs = 500

type apiKeyConcurrencyQueryRequest struct {
	APIKeyIDs []int64 `json:"api_key_ids"`
}

type apiKeyConcurrencyResponse struct {
	Available   bool          `json:"available"`
	Complete    bool          `json:"complete"`
	CollectedAt time.Time     `json:"collected_at"`
	Items       map[int64]int `json:"items"`
}

// QueryAPIKeyConcurrency returns exact counts for a bounded caller-supplied set.
// POST /api/v1/admin/api-keys/concurrency/query
func (h *UserHandler) QueryAPIKeyConcurrency(c *gin.Context) {
	var req apiKeyConcurrencyQueryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if len(req.APIKeyIDs) > maxAPIKeyConcurrencyQueryIDs {
		response.BadRequest(c, "api_key_ids may contain at most 500 entries")
		return
	}

	ids := make([]int64, 0, len(req.APIKeyIDs))
	seen := make(map[int64]struct{}, len(req.APIKeyIDs))
	for _, id := range req.APIKeyIDs {
		if id <= 0 {
			response.BadRequest(c, "api_key_ids must contain only positive IDs")
			return
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if h == nil || h.concurrencyService == nil {
		writeAPIKeyConcurrencyUnavailable(c, nil)
		return
	}

	counts, err := h.concurrencyService.GetAPIKeyConcurrencyBatchExact(c.Request.Context(), ids)
	if err != nil {
		writeAPIKeyConcurrencyUnavailable(c, err)
		return
	}
	response.Success(c, apiKeyConcurrencyResponse{
		Available:   true,
		Complete:    true,
		CollectedAt: time.Now().UTC(),
		Items:       counts,
	})
}

// GetActiveAPIKeyConcurrency returns a sparse index-backed snapshot containing
// only keys whose regular-request or Live-turn concurrency is non-zero.
// GET /api/v1/admin/api-keys/concurrency
func (h *UserHandler) GetActiveAPIKeyConcurrency(c *gin.Context) {
	if h == nil || h.concurrencyService == nil {
		writeAPIKeyConcurrencyUnavailable(c, nil)
		return
	}
	snapshot, err := h.concurrencyService.GetActiveAPIKeyConcurrency(c.Request.Context())
	if err != nil {
		writeAPIKeyConcurrencyUnavailable(c, err)
		return
	}
	response.Success(c, apiKeyConcurrencyResponse{
		Available:   true,
		Complete:    snapshot.Complete,
		CollectedAt: snapshot.CollectedAt,
		Items:       snapshot.Counts,
	})
}

func writeAPIKeyConcurrencyUnavailable(c *gin.Context, err error) {
	if err != nil {
		slog.Error("admin API-key concurrency unavailable", "error", err)
	}
	response.ErrorWithDetails(
		c,
		http.StatusServiceUnavailable,
		"API key concurrency is temporarily unavailable",
		"API_KEY_CONCURRENCY_UNAVAILABLE",
		nil,
	)
}
