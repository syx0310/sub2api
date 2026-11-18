package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIRequestViewScalarsOwnStorage(t *testing.T) {
	body := []byte(`{"model":"gpt-6.2-sol","stream":true,"prompt_cache_key":"cache-owned","previous_response_id":"resp_owned","service_tier":"priority","reasoning":{"effort":"high"}}`)
	view := newOpenAIRequestView(body)
	// Model/tier/effort survive into billing. They must not alias the borrowed
	// JSON body and pin all of its image/history bytes in the async queue.
	for i := range body {
		body[i] = 'x'
	}
	require.Equal(t, "gpt-6.2-sol", view.Model)
	require.True(t, view.Stream)
	require.Equal(t, "cache-owned", view.PromptCacheKey)
	require.Equal(t, "resp_owned", view.PreviousResponseID)
	require.Equal(t, "priority", view.ServiceTier)
	require.Equal(t, "high", view.ReasoningEffort)
}
