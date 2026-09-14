package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func reasoningTestSession() *openAIWSReasoningSession {
	return &openAIWSReasoningSession{ctx: context.Background(), cache: &openAIWSReasoningCache{}, scope: "account:1/thread:A", ttl: time.Minute}
}

func prepareReasoningTestFrame(t *testing.T, session *openAIWSReasoningSession, body string, hooks *OpenAIWSIngressHooks) []byte {
	t.Helper()
	out, frame, err := session.prepare([]byte(body), "gpt-6-astra", hooks)
	require.NoError(t, err)
	session.activate(frame, out, "gpt-6-astra", "gpt-6-astra")
	return out
}

func completeReasoningTestFrame(session *openAIWSReasoningSession, responseID string) {
	session.observeTerminal([]byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"output":[{"type":"message"}]}}`, responseID)))
}

func TestWSReasoningConfigurationInheritanceAndPolicy(t *testing.T) {
	session := reasoningTestSession()
	policy := &OpenAIWSIngressHooks{MaxReasoningEffort: "high"}
	first := `{"model":"gpt-6-astra","reasoning":{"effort":"low"},"input":[{"type":"configuration_update","reasoning":{"effort":"xhigh"}},{"role":"user","content":"solve"}]}`
	out := prepareReasoningTestFrame(t, session, first, policy)
	require.Equal(t, "low", gjson.GetBytes(out, "reasoning.effort").String(), "preserve the pinned prefix")
	require.Equal(t, "high", gjson.GetBytes(out, "input.0.reasoning.effort").String())
	result := &OpenAIForwardResult{}
	session.stamp(result)
	require.Equal(t, "xhigh", *result.RequestedReasoningEffort)
	require.Equal(t, "high", *result.ReasoningEffort)
	completeReasoningTestFrame(session, "resp_1")
	second := `{"model":"gpt-6-astra","previous_response_id":"resp_1","reasoning":{"effort":"low"},"input":[{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`
	out = prepareReasoningTestFrame(t, session, second, policy)
	require.Equal(t, second, string(out), "stable policy must not inject repeated overrides")
	result = &OpenAIForwardResult{}
	session.stamp(result)
	require.Equal(t, "xhigh", *result.RequestedReasoningEffort)
	require.Equal(t, "high", *result.ReasoningEffort)
	completeReasoningTestFrame(session, "resp_2")
	denied := &OpenAIWSIngressHooks{MaxReasoningEffort: "medium", MaxReasoningEffortOverLimit: "deny"}
	_, _, err := session.prepare([]byte(second), "gpt-6-astra", denied)
	require.Error(t, err)
	var limit *ReasoningEffortOverLimitError
	require.ErrorAs(t, err, &limit)
	_, _, err = session.prepare([]byte(first), "gpt-6-astra", denied)
	require.ErrorAs(t, err, &limit)
}

func TestWSReasoningCompactionAndFreshBaseline(t *testing.T) {
	session := reasoningTestSession()
	prepareReasoningTestFrame(t, session, `{"reasoning":{"effort":"low"},"input":[{"type":"configuration_update","reasoning":{"effort":"xhigh"}},{"role":"user","content":"task"}]}`, nil)
	completeReasoningTestFrame(session, "resp_1")
	compact := `{"reasoning":{"effort":"low"},"previous_response_id":"resp_1","input":[{"type":"compaction_trigger"}]}`
	prepareReasoningTestFrame(t, session, compact, nil)
	result := &OpenAIForwardResult{}
	session.stamp(result)
	require.Equal(t, "xhigh", *result.ReasoningEffort)
	session.observeTerminal([]byte(`{"type":"response.failed","response":{"id":"resp_failed"}}`))
	_, found := session.cache.get(session.scope, "resp_failed")
	require.False(t, found)
	parent, found := session.cache.get(session.scope, "resp_1")
	require.True(t, found)
	require.True(t, parent.override)
	session.observeTerminal([]byte(`{"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"opaque"}}`))
	completeReasoningTestFrame(session, "resp_compact")
	prepareReasoningTestFrame(t, session, `{"reasoning":{"effort":"medium"},"previous_response_id":"resp_compact","input":[{"role":"user","content":"continue"}]}`, nil)
	result = &OpenAIForwardResult{ReasoningEffort: openAIWSTrimmedStringPtr("medium")}
	session.stamp(result)
	require.Equal(t, "medium", *result.ReasoningEffort)
	// A client-owned compacted full window is also authoritative, not a reason
	// to inherit an old cached override into an unrelated new chain.
	out := prepareReasoningTestFrame(t, session, `{"reasoning":{"effort":"medium"},"input":[{"type":"configuration_update","reasoning":{"effort":"xhigh"}},{"type":"compaction","encrypted_content":"opaque"},{"role":"user","content":"new"}]}`, nil)
	require.Equal(t, "medium", *extractOpenAIReasoningEffortFromBody(out, "gpt-6-astra"))
}

func TestWSReasoningInvalidCompactionDoesNotRetireOverrides(t *testing.T) {
	for _, items := range []int{0, 2} {
		t.Run(fmt.Sprint(items), func(t *testing.T) {
			session := reasoningTestSession()
			prepareReasoningTestFrame(t, session, `{"input":[{"type":"configuration_update","reasoning":{"effort":"xhigh"}}]}`, nil)
			completeReasoningTestFrame(session, "resp_parent")
			prepareReasoningTestFrame(t, session, `{"reasoning":{"effort":"low"},"previous_response_id":"resp_parent","input":[{"type":"compaction_trigger"}]}`, nil)
			for range items {
				session.observeTerminal([]byte(`{"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"opaque"}}`))
			}
			completeReasoningTestFrame(session, "resp_invalid_compact")
			prepareReasoningTestFrame(t, session, `{"reasoning":{"effort":"low"},"previous_response_id":"resp_invalid_compact","input":[]}`, nil)
			result := &OpenAIForwardResult{}
			session.stamp(result)
			require.Equal(t, "xhigh", *result.ReasoningEffort)
		})
	}
}

func TestWSReasoningUnknownParentAndScopeIsolation(t *testing.T) {
	session := reasoningTestSession()
	prepareReasoningTestFrame(t, session, `{"input":[{"type":"configuration_update","reasoning":{"effort":"xhigh"}}]}`, nil)
	completeReasoningTestFrame(session, "resp_parent")
	body := []byte(`{"reasoning":{"effort":"low"},"previous_response_id":"resp_parent","input":[]}`)
	for _, scope := range []string{"account:1/thread:B", "account:2/thread:A"} {
		other := &openAIWSReasoningSession{ctx: context.Background(), cache: session.cache, scope: scope, ttl: time.Minute}
		_, _, err := other.prepare(body, "gpt-6-astra", &OpenAIWSIngressHooks{MaxReasoningEffort: "high"})
		require.ErrorContains(t, err, "state is unavailable")
		out, frame, err := other.prepare(body, "gpt-6-astra", nil)
		require.NoError(t, err)
		require.Equal(t, body, out)
		other.activate(frame, out, "gpt-6-astra", "gpt-6-astra")
		result := &OpenAIForwardResult{ReasoningEffort: openAIWSTrimmedStringPtr("low")}
		other.stamp(result)
		require.Nil(t, result.ReasoningEffort, "unknown inheritance must not report the top-level baseline")
	}
}

func TestWSReasoningPolicyChangeDoesNotRewriteHistory(t *testing.T) {
	session := reasoningTestSession()
	prepareReasoningTestFrame(t, session, `{"reasoning":{"effort":"low"},"input":[{"type":"configuration_update","reasoning":{"effort":"xhigh"}},{"role":"user","content":"initial"}]}`, nil)
	completeReasoningTestFrame(session, "resp_1")
	body := `{"reasoning":{"effort":"low"},"previous_response_id":"resp_1","input":[{"role":"user","content":"new input"},{"type":"compaction_trigger"}]}`
	out := prepareReasoningTestFrame(t, session, body, &OpenAIWSIngressHooks{MaxReasoningEffort: "high"})
	require.Equal(t, "low", gjson.GetBytes(out, "reasoning.effort").String())
	require.Equal(t, "resp_1", gjson.GetBytes(out, "previous_response_id").String())
	require.Equal(t, "configuration_update", gjson.GetBytes(out, "input.0.type").String())
	require.Equal(t, "new input", gjson.GetBytes(out, "input.1.content").String())
	require.Equal(t, "compaction_trigger", gjson.GetBytes(out, "input.2.type").String())
}

func TestWSReasoningWarmupAndEmptyDelta(t *testing.T) {
	session := reasoningTestSession()
	prepareReasoningTestFrame(t, session, `{"generate":false,"reasoning":{"effort":"low"},"input":[]}`, nil)
	ms := 1
	result := &OpenAIForwardResult{FirstTokenMs: &ms}
	session.stamp(result)
	require.Nil(t, result.FirstTokenMs)
	completeReasoningTestFrame(session, "resp_prewarm")
	body := `{"reasoning":{"effort":"low"},"previous_response_id":"resp_prewarm","input":[]}`
	out := prepareReasoningTestFrame(t, session, body, &OpenAIWSIngressHooks{MaxReasoningEffort: "high"})
	require.Equal(t, body, string(out))
}

func TestWSReasoningCacheIsBoundedAndExpires(t *testing.T) {
	cache := &openAIWSReasoningCache{}
	for i := 0; i <= openAIWSReasoningMaxEntries; i++ {
		cache.put("scope", fmt.Sprint(i), openAIWSReasoningValue{known: true}, time.Minute)
	}
	require.Len(t, cache.entries, openAIWSReasoningMaxEntries)
	cache.put("scope", "expired", openAIWSReasoningValue{known: true}, -time.Second)
	_, found := cache.get("scope", "expired")
	require.False(t, found)
	cache.put("scope", "other", openAIWSReasoningValue{known: true}, time.Minute)
	_, found = cache.get("other-scope", "other")
	require.False(t, found)
}

func TestWSReasoningLiveParentSurvivesSharedCacheEviction(t *testing.T) {
	session := reasoningTestSession()
	prepareReasoningTestFrame(t, session, `{"reasoning":{"effort":"low"},"input":[{"type":"configuration_update","reasoning":{"effort":"xhigh"}}]}`, nil)
	completeReasoningTestFrame(session, "resp_active")
	clear(session.cache.entries)
	prepareReasoningTestFrame(t, session, `{"reasoning":{"effort":"low"},"previous_response_id":"resp_active","input":[]}`, &OpenAIWSIngressHooks{MaxReasoningEffort: "xhigh"})
	result := &OpenAIForwardResult{}
	session.stamp(result)
	require.Equal(t, "xhigh", *result.ReasoningEffort)
}

func TestWSReasoningMissingAncestryIsRetryableNotPolicyDenial(t *testing.T) {
	session := reasoningTestSession()
	_, _, err := session.prepare([]byte(`{"previous_response_id":"resp_missing","input":[]}`), "gpt-6-astra", &OpenAIWSIngressHooks{MaxReasoningEffort: "high"})
	require.ErrorIs(t, err, errOpenAIWSReasoningContinuation)
	var closeErr *OpenAIWSClientCloseError
	require.ErrorAs(t, openAIWSReasoningPolicyCloseError(err), &closeErr)
	require.Equal(t, coderws.StatusTryAgainLater, closeErr.statusCode)
}

func TestWSReasoningCacheConcurrentThreads(t *testing.T) {
	cache := &openAIWSReasoningCache{}
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			scope := fmt.Sprint(i)
			for j := range 64 {
				id := fmt.Sprint(j)
				cache.put(scope, id, openAIWSReasoningValue{requested: scope, known: true}, time.Minute)
				value, found := cache.get(scope, id)
				if !found || value.requested != scope {
					t.Errorf("cross-thread state: %s/%s", scope, id)
				}
			}
		})
	}
	wg.Wait()
}

func TestWSReasoningConfigurationMappingsFollowActiveControl(t *testing.T) {
	body := []byte(`{"model":"client-astra","reasoning":{"effort":"low"},"input":[{"type":"configuration_update","reasoning":{"effort":"high"}},{"role":"user","content":"configuration_update xhigh is only text"},{"type":"configuration_update","reasoning":{"effort":"xhigh"}}]}`)
	mappings := []ReasoningEffortMapping{{From: "xhigh", To: "medium", MatchType: "exact", Model: "client-astra"}}
	out, changed, err := ApplyReasoningEffortPolicy(body, "high", mappings, "")
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "low", gjson.GetBytes(out, "reasoning.effort").String())
	require.Equal(t, "high", gjson.GetBytes(out, "input.0.reasoning.effort").String(), "earlier cached history stays intact")
	require.Equal(t, "medium", gjson.GetBytes(out, "input.2.reasoning.effort").String())
	mappings[0].To = "deny"
	_, _, err = ApplyReasoningEffortPolicy(body, "high", mappings, "")
	var denied *ReasoningEffortMappingDeniedError
	require.ErrorAs(t, err, &denied)
}

func TestWSReasoningInheritedPolicyNeverMakesAdjacentControls(t *testing.T) {
	session := reasoningTestSession()
	prepareReasoningTestFrame(t, session, `{"generate":false,"reasoning":{"effort":"low"},"input":[{"type":"configuration_update","reasoning":{"effort":"xhigh"}}]}`, nil)
	session.observeTerminal([]byte(`{"type":"response.completed","response":{"id":"resp_warmup","output":[]}}`))
	hooks := &OpenAIWSIngressHooks{MaxReasoningEffort: "high"}
	_, _, err := session.prepare([]byte(`{"previous_response_id":"resp_warmup","input":[]}`), "gpt-6-astra", hooks)
	require.ErrorIs(t, err, errOpenAIWSReasoningContinuation)
	out := prepareReasoningTestFrame(t, session, `{"previous_response_id":"resp_warmup","input":[{"role":"user","content":"continue"},{"type":"compaction_trigger"}]}`, hooks)
	require.Equal(t, "user", gjson.GetBytes(out, "input.0.role").String())
	require.Equal(t, "configuration_update", gjson.GetBytes(out, "input.1.type").String())
	require.Equal(t, "compaction_trigger", gjson.GetBytes(out, "input.2.type").String())
}

func BenchmarkWSReasoningPrepare100KiB(b *testing.B) {
	body := []byte(`{"model":"gpt-6-astra","reasoning":{"effort":"low"},"input":[{"role":"user","content":"` + strings.Repeat("x", 100*1024) + `"},{"type":"configuration_update","reasoning":{"effort":"xhigh"}}]}`)
	session := reasoningTestSession()
	hooks := &OpenAIWSIngressHooks{MaxReasoningEffort: "high"}
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for b.Loop() {
		if _, _, err := session.prepare(body, "gpt-6-astra", hooks); err != nil {
			b.Fatal(err)
		}
	}
}
