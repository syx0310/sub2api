package apicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnthropicRootAllOfPreservesSamePropertyIntersection(t *testing.T) {
	schema, err := normalizeAnthropicInputSchema(json.RawMessage(`{
		"allOf":[
			{"type":"object","properties":{"n":{"type":"number","minimum":10}},"required":["n"]},
			{"type":"object","properties":{"n":{"maximum":20}}}
		]}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"object","properties":{"n":{"allOf":[{"type":"number","minimum":10},{"maximum":20}]}},"required":["n"]}`, string(schema))
}

func TestAnthropicRootAlternativePreservesExclusivity(t *testing.T) {
	schema, err := normalizeAnthropicInputSchema(json.RawMessage(`{"oneOf":[
		{"type":"object","properties":{"n":{"minimum":0}},"required":["n"]},
		{"type":"object","properties":{"n":{"maximum":10}},"required":["n"]}
	]}`))
	require.NoError(t, err)
	// In particular n=5 must still match two alternatives and therefore fail.
	require.JSONEq(t, `{"type":"object","properties":{"n":{"oneOf":[{"minimum":0},{"maximum":10}]}},"required":["n"]}`, string(schema))
}

func TestAnthropicRootUnionRejectsLossyConstraints(t *testing.T) {
	for name, schema := range map[string]string{
		"optional oneOf":            `{"oneOf":[{"properties":{"x":{"const":1}}},{"properties":{"x":{"const":2}}}]}`,
		"correlation":               `{"anyOf":[{"properties":{"x":{"const":1},"y":{"const":1}}},{"properties":{"x":{"const":2},"y":{"const":2}}}]}`,
		"closed branches":           `{"allOf":[{"properties":{"x":{}},"additionalProperties":false},{"properties":{"y":{}}}]}`,
		"branch constraint":         `{"allOf":[{"type":"object","minProperties":2}]}`,
		"references":                `{"allOf":[{"properties":{"x":{"$ref":"#/allOf/0"}}}]}`,
		"malformed":                 `{"anyOf":[]}`,
		"null branch":               `{"anyOf":[null]}`,
		"large integer distinction": `{"anyOf":[{"properties":{"x":{"const":9007199254740992},"y":{"const":1}}},{"properties":{"x":{"const":9007199254740993},"y":{"const":2}}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ResponsesToAnthropicRequest(&ResponsesRequest{
				Input: json.RawMessage(`"hello"`),
				Tools: []ResponsesTool{{Type: "function", Name: "test_tool", Parameters: json.RawMessage(schema)}},
			})
			require.ErrorContains(t, err, "cannot be represented faithfully")
		})
	}
}

func TestAnthropicRootUnionBoundsRecursion(t *testing.T) {
	schema := strings.Repeat(`{"allOf":[`, 34) + `{"type":"object"}` + strings.Repeat(`]}`, 34)
	_, err := normalizeAnthropicInputSchema(json.RawMessage(schema))
	require.ErrorContains(t, err, "complexity limit")
}
