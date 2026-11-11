package service

import (
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
)

func openAIWSWebSearchDeclared(payload []byte) bool {
	fields := gjson.GetMany(openAIWSPayloadStringView(payload), "tools", "input")
	if gjsonToolsContainWebSearch(fields[0]) {
		return true
	}
	for _, item := range fields[1].Array() {
		if item.Get("type").String() == openAIAdditionalToolsItemType && gjsonToolsContainWebSearch(item.Get("tools")) {
			return true
		}
	}
	return false
}

// A new socket cannot resolve connection-local references. Only the client
// owns the full history: never manufacture a full request by dropping anchors.
func openAIWSSearchActivationCanReconnect(payload []byte) bool {
	if strings.TrimSpace(gjson.GetBytes(payload, "previous_response_id").String()) != "" {
		return false
	}
	input := gjson.GetBytes(payload, "input")
	if !input.IsArray() && input.Type != gjson.String {
		return false
	}
	for _, item := range input.Array() {
		if item.Get("type").String() == "item_reference" {
			return false
		}
	}
	coverage := AnalyzeToolCallOutputContextCoverageBytes(payload)
	return !coverage.HasFunctionCallOutput || coverage.ContextCoversAllCallIDs
}

// Called under writeMu, only after the first response.create was written.
func (c *openAIWSConn) recordInitialWebSearch(value any) {
	var body []byte
	switch payload := value.(type) {
	case json.RawMessage:
		body = payload
	case []byte:
		body = payload
	case map[string]any:
		if payload["type"] != "response.create" {
			return
		}
		c.responseCreateSent = true
		c.initialWebSearch = openAIToolsContainWebSearch(payload["tools"])
		input, _ := payload["input"].([]any)
		for _, raw := range input {
			if item, ok := raw.(map[string]any); ok && item["type"] == openAIAdditionalToolsItemType && openAIToolsContainWebSearch(item["tools"]) {
				c.initialWebSearch = true
			}
		}
		return
	default:
		return
	}
	if gjson.GetBytes(body, "type").String() == "response.create" {
		c.responseCreateSent = true
		c.initialWebSearch = openAIWSWebSearchDeclared(body)
	}
}

func (l *openAIWSConnLease) initializedWithoutWebSearch() bool {
	c, err := l.activeConn()
	if err != nil {
		return false
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.responseCreateSent && !c.initialWebSearch
}
