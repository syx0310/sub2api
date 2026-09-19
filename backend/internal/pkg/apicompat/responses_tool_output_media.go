package apicompat

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"
)

type responsesToolOutputMedia struct {
	callID string
	image  map[string]any
}

// LiftResponsesToolOutputMedia moves image parts out of Responses tool outputs
// and into a following user message. Native Responses endpoints such as
// DeepSeek accept function_call_output.output as a string, but Codex view_image
// returns an array containing input_image. Keeping the image inside the tool
// output makes the upstream report "No tool output found for tool call ...".
func LiftResponsesToolOutputMedia(input any) (any, bool, error) {
	items, ok := input.([]any)
	if !ok {
		return input, false, nil
	}

	rewritten := make([]any, 0, len(items)+1)
	changed := false

	// Keep contiguous parallel outputs together. Instructions between outputs
	// are a semantic boundary, not movable annotations: reject a conversion
	// that would need to move later tool results across that boundary.
	for index := 0; index < len(items); {
		item, ok := items[index].(map[string]any)
		if !ok || !isResponsesToolOutputItem(item) {
			rewritten = append(rewritten, items[index])
			index++
			continue
		}

		batchStart := index
		outputs := make([]any, 0)
		trailing := make([]any, 0)
		pending := make([]responsesToolOutputMedia, 0)
		batchChanged := false
		crossesInstruction := false

		for index < len(items) {
			rawItem := items[index]
			item, ok := rawItem.(map[string]any)
			if !ok {
				break
			}
			if isResponsesToolOutputItem(item) {
				crossesInstruction = crossesInstruction || len(trailing) > 0
				rewrittenItem, media, didRewrite, err := liftResponsesToolOutputMediaItem(item)
				if err != nil {
					return input, false, err
				}
				if didRewrite {
					batchChanged = true
					pending = append(pending, media...)
				}
				outputs = append(outputs, rewrittenItem)
				index++
				continue
			}
			if isResponsesToolBatchInstruction(item) {
				trailing = append(trailing, rawItem)
				index++
				continue
			}
			break
		}

		if !batchChanged {
			rewritten = append(rewritten, items[batchStart:index]...)
			continue
		}
		if crossesInstruction {
			return input, false, fmt.Errorf("DeepSeek tool media conversion cannot move parallel results across system/developer instructions; use an upstream supporting native multimodal tool outputs")
		}

		rewritten = append(rewritten, outputs...)
		if len(pending) > 0 {
			rewritten = append(rewritten, buildResponsesToolOutputMediaMessage(pending))
		}
		rewritten = append(rewritten, trailing...)
		changed = true
	}

	if !changed {
		return input, false, nil
	}
	return rewritten, true, nil
}

func liftResponsesToolOutputMediaItem(item map[string]any) (any, []responsesToolOutputMedia, bool, error) {
	// A string is opaque tool data, even when it happens to contain JSON or a
	// data URL. Only native Responses content arrays describe image parts.
	parts, ok := item["output"].([]any)
	if !ok {
		return item, nil, false, nil
	}
	callID := strings.TrimSpace(stringValue(item["call_id"]))
	output := make([]any, 0, len(parts))
	var lifted []responsesToolOutputMedia
	unsupported := false
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok {
			unsupported = true
			continue
		}
		if _, encrypted := part["encrypted_content"]; encrypted {
			unsupported = true
		}
		switch part["type"] {
		case "input_text":
			output = append(output, part)
		case "input_image":
			url, valid := part["image_url"].(string)
			if !valid || strings.TrimSpace(url) == "" || part["file_id"] != nil {
				return item, nil, false, fmt.Errorf("DeepSeek tool image conversion requires an inline image_url; file references cannot be resolved by this bridge")
			}
			// Retain detail and future image fields; do not marshal the large image
			// into a string merely to parse it again as the upstream helper did.
			lifted = append(lifted, responsesToolOutputMedia{callID: callID, image: maps.Clone(part)})
			output = append(output, map[string]any{"type": "input_text", "text": toolOutputMediaMarker})
		default:
			unsupported = true
		}
	}
	if len(lifted) == 0 {
		return item, nil, false, nil
	}
	if unsupported || callID == "" {
		return item, nil, false, fmt.Errorf("DeepSeek tool media conversion cannot preserve this mixed content or missing call_id; use native Responses")
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return item, nil, false, fmt.Errorf("encode DeepSeek tool output: %w", err)
	}
	rewritten := maps.Clone(item)
	rewritten["output"] = string(encoded)
	return rewritten, lifted, true, nil
}

func buildResponsesToolOutputMediaMessage(pending []responsesToolOutputMedia) map[string]any {
	content := make([]map[string]any, 0, len(pending)*2)
	lastCallID := ""
	for _, media := range pending {
		if media.callID != lastCallID {
			text := "Tool output media"
			if media.callID != "" {
				text = fmt.Sprintf(toolOutputMediaAttribution, media.callID)
			}
			content = append(content, map[string]any{
				"type": "input_text",
				"text": text,
			})
			lastCallID = media.callID
		}
		content = append(content, media.image)
	}

	return map[string]any{
		"type":    "message",
		"role":    "user",
		"content": content,
	}
}

func isResponsesToolBatchInstruction(item map[string]any) bool {
	itemType := strings.TrimSpace(stringValue(item["type"]))
	if itemType != "" && itemType != "message" {
		return false
	}
	role := strings.TrimSpace(stringValue(item["role"]))
	return role == "developer" || role == "system"
}

func isResponsesToolOutputItem(item map[string]any) bool {
	switch strings.TrimSpace(stringValue(item["type"])) {
	case "function_call_output", "custom_tool_call_output",
		"tool_search_output", "tool_search_call_output", "mcp_tool_call_output":
		return true
	default:
		return false
	}
}
