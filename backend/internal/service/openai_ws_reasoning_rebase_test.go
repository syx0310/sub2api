package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestWSReasoningInferredAnchorRebasesWithoutDoubleMapping(t *testing.T) {
	for _, override := range []bool{false, true} {
		session := reasoningTestSession()
		first := `{"model":"gpt-6-astra","reasoning":{"effort":"high"},"input":[]}`
		if override {
			first = `{"model":"gpt-6-astra","reasoning":{"effort":"low"},"input":[{"type":"configuration_update","reasoning":{"effort":"xhigh"}}]}`
		}
		prepareReasoningTestFrame(t, session, first, nil)
		completeReasoningTestFrame(session, "resp_parent")
		policy := &OpenAIWSIngressHooks{ReasoningEffortMappings: []ReasoningEffortMapping{{From: "high", To: "medium"}, {From: "medium", To: "low"}}}
		body, frame, err := session.prepare([]byte(`{"model":"gpt-6-astra","reasoning":{"effort":"high"},"input":[{"type":"function_call_output","call_id":"call_a","output":"ok"}]}`), "gpt-6-astra", policy)
		require.NoError(t, err)
		body, err = sjson.SetBytes(body, "previous_response_id", "resp_parent")
		require.NoError(t, err)
		body, frame, err = session.rebaseInferredParent(frame, body, "gpt-6-astra", policy)
		require.NoError(t, err)
		require.Equal(t, "medium", gjson.GetBytes(body, "reasoning.effort").String())
		session.activate(frame, body, "gpt-6-astra", "gpt-6-astra")
		result := &OpenAIForwardResult{ReasoningEffort: extractOpenAIReasoningEffortFromBody(body, "gpt-6-astra")}
		session.stamp(result)
		want := "medium"
		if override {
			want = "xhigh"
		}
		require.Equal(t, want, *result.ReasoningEffort)
	}
}

func TestWSReasoningUltraUsesMappedModelAndPublicPolicyScope(t *testing.T) {
	for _, wire := range []string{"public-alias", "gpt-6-sol"} {
		body := []byte(`{"model":"` + wire + `","input":[{"type":"configuration_update","reasoning":{"effort":"ultra"}}]}`)
		session := reasoningTestSession()
		out, frame, err := session.prepare(body, "public-alias", &OpenAIWSIngressHooks{MaxReasoningEffort: "max"}, "gpt-6-sol")
		require.NoError(t, err)
		require.Equal(t, "max", gjson.GetBytes(out, "input.0.reasoning.effort").String())
		session.activate(frame, out, "gpt-6-sol", "public-alias")
		result := &OpenAIForwardResult{}
		session.stamp(result)
		require.Equal(t, "max", *result.ReasoningEffort)
	}
}

func TestWSReasoningPublicAliasMappingsSurviveUpstreamResolution(t *testing.T) {
	for _, config := range []bool{false, true} {
		body := []byte(`{"model":"gpt-6-sol","reasoning":{"effort":"high"},"input":[]}`)
		path := "reasoning.effort"
		if config {
			body = []byte(`{"model":"gpt-6-sol","input":[{"type":"configuration_update","reasoning":{"effort":"high"}}]}`)
			path = "input.0.reasoning.effort"
		}
		session := reasoningTestSession()
		out, _, err := session.prepare(body, "public-alias", &OpenAIWSIngressHooks{ReasoningEffortMappings: []ReasoningEffortMapping{{From: "high", To: "medium", Model: "public-alias", MatchType: "exact"}}}, "gpt-6-sol")
		require.NoError(t, err)
		require.Equal(t, "medium", gjson.GetBytes(out, path).String())
		require.Equal(t, "gpt-6-sol", gjson.GetBytes(out, "model").String())
	}
	for model, effort := range map[string]string{"gpt-6-sol": "max", "gpt-6-luna": "max", "gpt-6-astra": "xhigh", "gpt-6.1-sol": "xhigh"} {
		got, err := applyOpenAIConfigurationEffortValue("ultra", "public-alias", "max", nil, "", model)
		require.NoError(t, err)
		require.Equal(t, effort, got)
	}
}

func TestWSReasoningCompactionSummaryRetiresOverride(t *testing.T) {
	session := reasoningTestSession()
	prepareReasoningTestFrame(t, session, `{"input":[{"type":"configuration_update","reasoning":{"effort":"xhigh"}}]}`, nil)
	completeReasoningTestFrame(session, "resp_before")
	prepareReasoningTestFrame(t, session, `{"previous_response_id":"resp_before","input":[{"type":"compaction_trigger"}]}`, nil)
	session.observeTerminal([]byte(`{"type":"response.output_item.done","item":{"type":"compaction_summary","encrypted_content":"opaque"}}`))
	completeReasoningTestFrame(session, "resp_after")
	parent, ok := session.cache.get(session.scope, "resp_after")
	require.True(t, ok)
	require.False(t, parent.override)
}

func TestWSReasoningDisabledIsNotANumericEffort(t *testing.T) {
	value, err := applyOpenAIConfigurationEffortValue("disabled", "gpt-6-astra", "", nil, "")
	require.NoError(t, err)
	require.Equal(t, "disabled", value)
	_, err = applyOpenAIConfigurationEffortValue("disabled", "gpt-6-astra", "high", nil, "")
	require.ErrorContains(t, err, "cannot be compared")
}
