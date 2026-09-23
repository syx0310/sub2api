package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Only actual input control items affect effort. A compacted history establishes
// a new baseline; strings inside user/tool content are never interpreted.
func lastOpenAIConfigurationEffort(body []byte) (index int, effort string, present bool) {
	index = -1
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return
	}
	input.ForEach(func(i, item gjson.Result) bool {
		switch item.Get("type").String() {
		case "compaction", "compaction_summary", "context_compaction":
			index, effort, present = -1, "", false
		case "configuration_update":
			index, present = int(i.Int()), true
			effort = ""
			if field := item.Get("reasoning.effort"); field.Type == gjson.String {
				effort = strings.TrimSpace(field.String())
			}
		}
		return true
	})
	return
}

func lastOpenAIConfigurationEffortMap(input any) (effort string, present bool) {
	items, _ := input.([]any)
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		switch item["type"] {
		case "compaction", "compaction_summary", "context_compaction":
			effort, present = "", false
		case "configuration_update":
			present = true
			reasoning, _ := item["reasoning"].(map[string]any)
			effort, _ = reasoning["effort"].(string)
			effort = strings.TrimSpace(effort)
		}
	}
	return
}

func applyOpenAIConfigurationEffortValue(raw, model, maxEffort string, mappings []ReasoningEffortMapping, overLimit string) (string, error) {
	// Reuse the existing mapping/deny/rank logic on a bounded scalar request;
	// never overwrite the cache-preserving top-level baseline in the real body.
	// Match ApplyReasoningEffortPolicy's no-policy fast path. Resolving a UI
	// preset here without a policy would inject an unnecessary inherited update
	// and could downgrade Sol's Ultra from max to Astra's xhigh.
	if strings.TrimSpace(maxEffort) == "" && len(mappings) == 0 {
		return raw, nil
	}
	value := raw
	if strings.EqualFold(value, "ultra") {
		if isOpenAIGPT6Model(model) {
			value, _ = normalizeGPT6ReasoningEffort(value, model)
		} else {
			value = "xhigh"
		}
	}
	if (maxEffort != "" || len(mappings) > 0) && normalizeReasoningEffortMappingSource(value) == "" {
		return "", &ReasoningEffortMappingDeniedError{Requested: "unrecognized configuration_update effort"}
	}
	scalar, _ := json.Marshal(map[string]any{"model": model, "reasoning": map[string]any{"effort": value}})
	next, _, err := ApplyReasoningEffortPolicy(scalar, maxEffort, mappings, overLimit)
	if err != nil {
		return "", err
	}
	return gjson.GetBytes(next, "reasoning.effort").String(), nil
}

func applyOpenAIConfigurationEffortPolicy(body []byte, index int, raw, model, maxEffort string, mappings []ReasoningEffortMapping, overLimit string) ([]byte, bool, error) {
	value, err := applyOpenAIConfigurationEffortValue(raw, model, maxEffort, mappings, overLimit)
	if err != nil || value == raw {
		return body, false, err
	}
	next, err := sjson.SetBytes(body, fmt.Sprintf("input.%d.reasoning.effort", index), value)
	return next, err == nil, err
}

// appendOpenAIInheritedEffortUpdate applies a changed policy to an inherited
// override without editing already-sent history or deleting its response anchor.
func appendOpenAIInheritedEffortUpdate(body []byte, effort string, parentTailConfig bool) ([]byte, error) {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, fmt.Errorf("%w: inherited policy requires an input array", errOpenAIWSReasoningContinuation)
	}
	items := input.Array()
	position := 0
	if parentTailConfig {
		// Adjacent updates are invalid. A normal new item can separate them;
		// compaction_trigger must remain the final item, so it cannot do so.
		if len(items) == 0 || items[0].Get("type").String() == "compaction_trigger" || items[0].Get("type").String() == "configuration_update" {
			return body, fmt.Errorf("%w: cannot safely separate configuration updates", errOpenAIWSReasoningContinuation)
		}
		position = 1
	}
	update, _ := json.Marshal(map[string]any{"type": "configuration_update", "reasoning": map[string]any{"effort": effort}})
	result := make([]json.RawMessage, 0, len(items)+1)
	for i := 0; i <= len(items); i++ {
		if i == position {
			result = append(result, update)
		}
		if i < len(items) {
			result = append(result, json.RawMessage(items[i].Raw))
		}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return body, err
	}
	return sjson.SetRawBytes(body, "input", raw)
}

func openAIInputEndsInConfiguration(body []byte) bool {
	input := gjson.GetBytes(body, "input")
	lastIsConfiguration := false
	if input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool {
			lastIsConfiguration = item.Get("type").String() == "configuration_update"
			return true
		})
	}
	return lastIsConfiguration
}

func openAIInputHasNoNewItems(body []byte) bool {
	input := gjson.GetBytes(body, "input")
	if !input.Exists() || input.Type == gjson.Null {
		return true
	}
	if !input.IsArray() {
		return false
	}
	empty := true
	input.ForEach(func(_, _ gjson.Result) bool { empty = false; return false })
	return empty
}

func openAIInputHasCompactedHistory(body []byte) bool {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return false
	}
	found := false
	input.ForEach(func(_, item gjson.Result) bool {
		switch item.Get("type").String() {
		case "compaction", "compaction_summary", "context_compaction":
			found = true
		}
		return !found
	})
	return found
}
