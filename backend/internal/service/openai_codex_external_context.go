package service

import (
	"strings"

	"github.com/tidwall/gjson"
)

// Codex AdditionalContextToolOutput is external user context, not the result of
// a model tool call. Keep its native representation across WS and HTTP replay.
// Deliberately do not exempt arbitrary orphan function results or malformed IDs.
func isCodexExternalContextName(namespace, name string) bool {
	if namespace != "agent" {
		return false
	}
	switch name {
	case "browser_info", "goal_objective", "untrusted_context", "untrusted_content":
		return true
	}
	return false
}

func isCodexExternalContextItem(item gjson.Result) bool {
	if item.Get("type").String() != "function_call_output" ||
		!isCodexExternalContextName(item.Get("namespace").String(), item.Get("name").String()) {
		return false
	}
	callID := item.Get("call_id")
	if callID.Exists() && (callID.Type != gjson.String || strings.TrimSpace(callID.String()) != "") {
		return false
	}
	output := item.Get("output")
	return output.Type == gjson.String && strings.TrimSpace(output.String()) != ""
}

func isCodexExternalContextMap(item map[string]any) bool {
	namespace, _ := item["namespace"].(string)
	name, _ := item["name"].(string)
	if item["type"] != "function_call_output" || !isCodexExternalContextName(namespace, name) {
		return false
	}
	if value, exists := item["call_id"]; exists {
		callID, ok := value.(string)
		if !ok || strings.TrimSpace(callID) != "" {
			return false
		}
	}
	output, ok := item["output"].(string)
	return ok && strings.TrimSpace(output) != ""
}
