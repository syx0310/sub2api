package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodexServiceTierIntersectionUsesIDsAndNeverNull(t *testing.T) {
	for _, tc := range []struct {
		name, first, second, want, wantLegacy string
	}{
		{"common_fast", `[{"id":"priority","name":"Fast"},{"id":"ultrafast","name":"Ultrafast"}]`, `[{"id":"priority","name":"Different label"}]`, `["priority"]`, `["fast"]`},
		{"reordered", `[{"id":"priority"},{"id":"ultrafast"}]`, `[{"id":"ultrafast"},{"id":"priority"}]`, `["priority","ultrafast"]`, `["fast"]`},
		{"ultrafast_without_fast", `[{"id":"priority"},{"id":"ultrafast"}]`, `[{"id":"ultrafast"}]`, `["ultrafast"]`, `[]`},
		{"unknown", `[{"id":"ultrafast"}]`, ``, `[]`, `[]`},
		{"empty", `[{"id":"ultrafast"}]`, `[]`, `[]`, `[]`},
		{"null", `[{"id":"ultrafast"}]`, `null`, `[]`, `[]`},
		{"invalid", `[{"id":"priority"}]`, `[{"id":""}]`, `[]`, `[]`},
		{"different", `[{"id":"priority"}]`, `[{"id":"ultrafast"}]`, `[]`, `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metadata := intersectUpstreamModelMetadata("gpt-6-astra", []UpstreamModelMetadata{
				{CodexToolCapabilities: map[string]json.RawMessage{"service_tiers": json.RawMessage(tc.first)}},
				{CodexToolCapabilities: map[string]json.RawMessage{"service_tiers": json.RawMessage(tc.second)}},
			})
			body, err := buildCodexModelsManifest([]string{"gpt-6-astra"}, nil, nil, nil, map[string]codexModelMetadataOverride{"gpt-6-astra": metadata})
			require.NoError(t, err)
			tiers := gjson.GetBytes(body, "models.0.service_tiers")
			require.True(t, tiers.IsArray(), string(body))
			ids := []string{}
			for _, tier := range tiers.Array() {
				ids = append(ids, tier.Get("id").String())
			}
			encoded, err := json.Marshal(ids)
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(encoded))
			require.JSONEq(t, tc.wantLegacy, gjson.GetBytes(body, "models.0.additional_speed_tiers").Raw)
		})
	}
}

func TestCodexServiceTiersNullIsExplicitEmptyNotPermission(t *testing.T) {
	fields := map[string]json.RawMessage{}
	require.True(t, applyCodexToolCapabilities(fields, map[string]json.RawMessage{"service_tiers": json.RawMessage("null")}, true))
	require.JSONEq(t, "[]", string(fields["service_tiers"]))
	require.False(t, applyCodexToolCapabilities(fields, map[string]json.RawMessage{"service_tiers": json.RawMessage(`[{"id":"ultrafast"}]`)}, false))
	require.JSONEq(t, "[]", string(fields["service_tiers"]))
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"compute_residency": "EU"}}
	require.NotContains(t, string(accountCodexToolCapabilities(account, "gpt-6-astra")["service_tiers"]), "ultrafast")
}
