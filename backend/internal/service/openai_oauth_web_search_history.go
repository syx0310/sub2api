package service

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// The ChatGPT internal Codex endpoint rejects a request whose input replays a
// hosted web_search_call item unless the request also declares the web_search
// tool; the stream then fails with "response protection is unavailable".
// Codex's local context compaction sends the full history with tools:[], so
// every compaction after a web search fails (#7927).
//
// Declare a cached-only web_search tool for such requests. Responses Lite
// rejects hosted tools at the top level, so Lite requests carry it in an
// input additional_tools item instead. Only add a declaration when tool use
// is already disabled or the request has an unambiguous no-tools intent.
// Cached search is still a callable tool, not just a history marker.

const (
	openAIWebSearchCallItemType      = "web_search_call"
	openAIAdditionalToolsItemType    = "additional_tools"
	openAICompactionTriggerItemType  = "compaction_trigger"
	openAIAdditionalToolsDefaultRole = "developer"
)

var openAIWebSearchHistoryTool = map[string]any{
	"type":                "web_search",
	"external_web_access": false,
}

func isOpenAIWebSearchToolType(toolType string) bool {
	return strings.HasPrefix(strings.TrimSpace(toolType), "web_search")
}

func openAIToolsContainWebSearch(rawTools any) bool {
	tools, ok := rawTools.([]any)
	if !ok {
		return false
	}
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if ok && isOpenAIWebSearchToolType(firstNonEmptyString(tool["type"])) {
			return true
		}
	}
	return false
}

// shouldPinOpenAIWebSearchHistoryToolChoice reports whether tool_choice may be
// forced to "none": only when the caller offered no tools, so the choice
// cannot meaningfully be anything else.
func shouldPinOpenAIWebSearchHistoryToolChoice(choice any) bool {
	switch typed := choice.(type) {
	case nil:
		return true
	case string:
		normalized := strings.ToLower(strings.TrimSpace(typed))
		return normalized == "" || normalized == "auto" || normalized == "none"
	default:
		return false
	}
}

func openAIWebSearchHistoryDeclarationPolicy(choice any, hasTools, hasAnchor, localCompaction bool) (allow, pinNone bool) {
	if value, ok := choice.(string); ok && value == "none" {
		return true, false
	}
	// An incremental request can inherit tools from its connection. Local
	// compaction sends no fresh tools, even when old declarations remain in
	// history; restrict it to a summary without enabling a new search.
	if !hasAnchor && (!hasTools || localCompaction) && shouldPinOpenAIWebSearchHistoryToolChoice(choice) {
		return true, true
	}
	return false, false
}

// openAIAdditionalToolsInsertIndex keeps a trailing compaction trigger last,
// as required by the remote compaction v2 wire format.
func openAIAdditionalToolsInsertIndex(itemTypes []string) int {
	if n := len(itemTypes); n > 0 && itemTypes[n-1] == openAICompactionTriggerItemType {
		return n - 1
	}
	return len(itemTypes)
}

// ensureOpenAIOAuthWebSearchToolForHistory is the map variant used by the
// Codex OAuth transform.
func ensureOpenAIOAuthWebSearchToolForHistory(reqBody map[string]any, responsesLite bool) bool {
	if reqBody == nil {
		return false
	}
	input, ok := reqBody["input"].([]any)
	if !ok {
		return false
	}
	hasWebSearchCall := false
	callerDeclaredTools := false
	hasCompactionTrigger := false
	itemTypes := make([]string, len(input))
	for i, rawItem := range input {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		itemTypes[i] = strings.TrimSpace(firstNonEmptyString(item["type"]))
		switch itemTypes[i] {
		case openAICompactionTriggerItemType:
			hasCompactionTrigger = true
		case openAIWebSearchCallItemType:
			hasWebSearchCall = true
		case openAIAdditionalToolsItemType:
			if openAIToolsContainWebSearch(item["tools"]) {
				return false
			}
			if tools, _ := item["tools"].([]any); len(tools) > 0 {
				callerDeclaredTools = true
			}
		}
	}
	if !hasWebSearchCall || openAIToolsContainWebSearch(reqBody["tools"]) {
		return false
	}
	tools, _ := reqBody["tools"].([]any)
	callerDeclaredTools = callerDeclaredTools || len(tools) > 0
	metadata, _ := reqBody["client_metadata"].(map[string]any)
	turnMetadata, _ := metadata[openAIWSTurnMetadataHeader].(string)
	localCompaction := !hasCompactionTrigger && len(tools) == 0 && gjson.Get(turnMetadata, "request_kind").String() == "compaction"
	hasAnchor := firstNonEmptyString(reqBody["previous_response_id"]) != ""
	allow, pinNone := openAIWebSearchHistoryDeclarationPolicy(reqBody["tool_choice"], callerDeclaredTools, hasAnchor, localCompaction)
	if !allow {
		return false
	}

	switch {
	case !responsesLite:
		reqBody["tools"] = append(tools, cloneOpenAIWebSearchHistoryTool())
	default:
		at := openAIAdditionalToolsInsertIndex(itemTypes)
		additional := map[string]any{
			"type":  openAIAdditionalToolsItemType,
			"role":  openAIAdditionalToolsDefaultRole,
			"tools": []any{cloneOpenAIWebSearchHistoryTool()},
		}
		next := make([]any, 0, len(input)+1)
		next = append(next, input[:at]...)
		next = append(next, additional)
		next = append(next, input[at:]...)
		reqBody["input"] = next
	}
	if pinNone {
		reqBody["tool_choice"] = "none"
	}
	return true
}

// ensureOpenAIOAuthWebSearchToolForHistoryBody is the raw-body variant used by
// the passthrough and WebSocket paths; it avoids decoding the whole body.
func ensureOpenAIOAuthWebSearchToolForHistoryBody(body []byte, responsesLite bool) ([]byte, bool, error) {
	if len(body) == 0 || !bytes.Contains(body, []byte(openAIWebSearchCallItemType)) {
		return body, false, nil
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, false, nil
	}
	items := input.Array()
	hasWebSearchCall := false
	callerDeclaredTools := false
	itemTypes := make([]string, len(items))
	for i, item := range items {
		itemTypes[i] = strings.TrimSpace(item.Get("type").String())
		switch itemTypes[i] {
		case openAIWebSearchCallItemType:
			hasWebSearchCall = true
		case openAIAdditionalToolsItemType:
			itemTools := item.Get("tools")
			if gjsonToolsContainWebSearch(itemTools) {
				return body, false, nil
			}
			if itemTools.IsArray() && len(itemTools.Array()) > 0 {
				callerDeclaredTools = true
			}
		}
	}
	if !hasWebSearchCall {
		return body, false, nil
	}
	tools := gjson.GetBytes(body, "tools")
	if gjsonToolsContainWebSearch(tools) {
		return body, false, nil
	}
	topLevelTools := tools.IsArray() && len(tools.Array()) > 0
	callerDeclaredTools = callerDeclaredTools || topLevelTools
	choice := gjson.GetBytes(body, "tool_choice")
	var choiceValue any
	if choice.Type == gjson.String {
		choiceValue = choice.String()
	} else if choice.Type != gjson.Null {
		return body, false, nil
	}
	semantic := ClassifyOpenAIResponsesRequest(body)
	localCompaction := semantic.Kind == "compaction" && !semantic.NativeCompactionV2 && !topLevelTools
	allow, pinNone := openAIWebSearchHistoryDeclarationPolicy(choiceValue, callerDeclaredTools, gjson.GetBytes(body, "previous_response_id").String() != "", localCompaction)
	if !allow {
		return body, false, nil
	}

	var (
		next []byte
		err  error
	)
	switch {
	case !responsesLite && topLevelTools:
		next, err = sjson.SetBytes(body, "tools.-1", cloneOpenAIWebSearchHistoryTool())
	case !responsesLite:
		next, err = sjson.SetBytes(body, "tools", []any{cloneOpenAIWebSearchHistoryTool()})
	default:
		next, err = insertOpenAIAdditionalToolsItemRaw(body, items, openAIAdditionalToolsInsertIndex(itemTypes))
	}
	if err != nil {
		return body, false, fmt.Errorf("declare web_search tool for web_search_call history: %w", err)
	}
	if pinNone {
		next, err = sjson.SetBytes(next, "tool_choice", "none")
		if err != nil {
			return body, false, fmt.Errorf("pin tool_choice for web_search_call history: %w", err)
		}
	}
	return next, true, nil
}

func insertOpenAIAdditionalToolsItemRaw(body []byte, items []gjson.Result, at int) ([]byte, error) {
	additional, err := marshalOpenAIUpstreamJSON(map[string]any{
		"type":  openAIAdditionalToolsItemType,
		"role":  openAIAdditionalToolsDefaultRole,
		"tools": []any{cloneOpenAIWebSearchHistoryTool()},
	})
	if err != nil {
		return nil, err
	}
	rawItems := make([][]byte, 0, len(items)+1)
	for i, item := range items {
		if i == at {
			rawItems = append(rawItems, additional)
		}
		rawItems = append(rawItems, []byte(item.Raw))
	}
	if at >= len(items) {
		rawItems = append(rawItems, additional)
	}
	raw := append([]byte{'['}, bytes.Join(rawItems, []byte{','})...)
	raw = append(raw, ']')
	return sjson.SetRawBytes(body, "input", raw)
}

func gjsonToolsContainWebSearch(tools gjson.Result) bool {
	if !tools.IsArray() {
		return false
	}
	for _, tool := range tools.Array() {
		if isOpenAIWebSearchToolType(tool.Get("type").String()) {
			return true
		}
	}
	return false
}

func cloneOpenAIWebSearchHistoryTool() map[string]any {
	tool := make(map[string]any, len(openAIWebSearchHistoryTool))
	for key, value := range openAIWebSearchHistoryTool {
		tool[key] = value
	}
	return tool
}
