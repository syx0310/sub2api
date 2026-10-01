package service

import (
	"bytes"
	"encoding/json"
	"strings"
)

// parseCodexServiceTiers normalizes explicit null to no capabilities, never to
// inferred defaults. Encoding this slice always satisfies Codex's Vec contract.
func parseCodexServiceTiers(raw json.RawMessage) ([]configuredCodexServiceTier, bool) {
	var tiers []configuredCodexServiceTier
	if len(bytes.TrimSpace(raw)) == 0 || json.Unmarshal(raw, &tiers) != nil {
		return nil, false
	}
	unique := make([]configuredCodexServiceTier, 0, len(tiers))
	seen := make(map[string]struct{}, len(tiers))
	for _, tier := range tiers {
		tier.ID = strings.TrimSpace(tier.ID)
		if tier.ID == "" {
			return nil, false
		}
		if _, exists := seen[tier.ID]; exists {
			continue
		}
		seen[tier.ID] = struct{}{}
		unique = append(unique, tier)
	}
	return unique, true
}

// Intersect by wire ID, not label, ordering, or JSON formatting. An unknown
// candidate cannot grant a tier advertised only by a different account.
func intersectCodexServiceTiers(candidates []UpstreamModelMetadata) ([]configuredCodexServiceTier, bool) {
	common := []configuredCodexServiceTier{}
	declared := false
	for i, candidate := range candidates {
		raw := candidate.CodexToolCapabilities["service_tiers"]
		declared = declared || len(bytes.TrimSpace(raw)) > 0
		tiers, _ := parseCodexServiceTiers(raw)
		if i == 0 {
			common = append(common, tiers...)
			continue
		}
		ids := make(map[string]struct{}, len(tiers))
		for _, tier := range tiers {
			ids[tier.ID] = struct{}{}
		}
		kept := common[:0]
		for _, tier := range common {
			if _, ok := ids[tier.ID]; ok {
				kept = append(kept, tier)
			}
		}
		common = kept
	}
	return common, declared
}
