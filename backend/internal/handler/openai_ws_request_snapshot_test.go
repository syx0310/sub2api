package handler

import (
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenAIWSNativeCompactionUsagePerTurn(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModeCtxPool, service.OpenAIWSIngressModePassthrough} {
		for _, compactFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/compactFirst=%v", mode, compactFirst), func(t *testing.T) {
				ordinary := `{"type":"response.create","model":"gpt-6-astra","input":[{"role":"user","content":"ordinary"}]}`
				compact := `{"type":"response.create","model":"gpt-6-astra","input":[{"type":"compaction_trigger"}]}`
				first, second := ordinary, compact
				if compactFirst {
					first, second = compact, ordinary
				}
				result := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
					ingressMode: mode, firstPayload: first, secondPayload: second, thirdPayload: ordinary,
				})
				require.Len(t, result.logs, 3)
				byID := make(map[string]*service.UsageLog)
				for _, log := range result.logs {
					byID[log.RequestID] = log
				}
				for i, want := range []bool{compactFirst, !compactFirst, false} {
					log := byID[fmt.Sprintf("resp_usage_e2e_%d", i+1)]
					require.NotNil(t, log)
					require.Equal(t, want, log.NativeCompactionV2)
					require.True(t, log.OpenAIWSMode, "native compaction is still Responses WS, not legacy compact HTTP")
					require.Equal(t, 2, log.InputTokens)
					require.Equal(t, 1, log.OutputTokens)
				}
			})
		}
	}
}

func TestWSTurnSnapshotCompactionDoesNotLeak(t *testing.T) {
	ordinary := []byte(`{"type":"response.create","model":"gpt-6-astra","input":[]}`)
	compact := []byte(`{"type":"response.create","model":"gpt-6-astra","previous_response_id":"resp_1","input":[{"type":"compaction_trigger"}]}`)
	first := newOpenAIWSTurnRequestSnapshot(1, "gpt-6-astra", ordinary, usageBodyBytesPtr(len(ordinary)))
	second := newOpenAIWSTurnRequestSnapshot(2, "gpt-6-astra", compact, usageBodyBytesPtr(len(compact)))
	third := newOpenAIWSTurnRequestSnapshot(3, "gpt-6-astra", ordinary, usageBodyBytesPtr(len(ordinary)))
	require.False(t, first.semantics.NativeCompactionV2)
	require.True(t, second.semantics.NativeCompactionV2)
	require.False(t, third.semantics.NativeCompactionV2)
	require.True(t, second.semantics.NativeCompactionV2, "later frames cannot change an async billing snapshot")
	rebuilt := newOpenAIWSTurnRequestSnapshot(1, "gpt-6-astra", compact, usageBodyBytesPtr(len(compact)))
	require.True(t, rebuilt.semantics.NativeCompactionV2)
	require.Equal(t, second.requestPayloadHash, rebuilt.requestPayloadHash)
}
