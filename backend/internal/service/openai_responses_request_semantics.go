package service

import (
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// OpenAIResponsesRequestSemantics describes one create, never an entire socket.
// Kind is diagnostic client intent; only the actual input control item proves
// native compaction. Neither value grants access or changes pricing.
type OpenAIResponsesRequestSemantics struct {
	Kind               string
	Prewarm            bool
	NativeCompactionV2 bool
}

func ClassifyOpenAIResponsesRequest(body []byte) OpenAIResponsesRequestSemantics {
	var turnMetadata string
	if metadata := gjson.GetBytes(body, "client_metadata."+openAIWSTurnMetadataHeader); metadata.Type == gjson.String {
		turnMetadata = metadata.String()
	}
	return classifyOpenAIResponsesRequestSignals(
		gjson.GetBytes(body, "type").String(),
		gjson.GetBytes(body, "generate").Type == gjson.False,
		HasCompactionTriggerInInput(body),
		turnMetadata,
	)
}

// Share precedence with map-based OAuth transforms without serializing the
// full history again: control > prewarm > native trigger > metadata intent.
func classifyOpenAIResponsesRequestSignals(event string, prewarm, nativeCompaction bool, turnMetadata string) OpenAIResponsesRequestSemantics {
	semantic := OpenAIResponsesRequestSemantics{Kind: "turn"}
	if event != "" && event != "response.create" {
		return OpenAIResponsesRequestSemantics{Kind: "control"}
	}
	if prewarm {
		return OpenAIResponsesRequestSemantics{Kind: "prewarm", Prewarm: true}
	}
	if nativeCompaction {
		return OpenAIResponsesRequestSemantics{Kind: "compaction", NativeCompactionV2: true}
	}
	// The embedded JSON is the canonical per-request snapshot, not a child
	// object and not an overlay to merge with the handshake or preceding turn.
	switch kind := gjson.Get(turnMetadata, "request_kind").String(); kind {
	case "turn", "compaction", "memory":
		semantic.Kind = strings.Clone(kind)
		// A metadata-only prewarm claim must not suppress inference accounting.
	}
	return semantic
}

// normalizeOpenAIWSInitialTurnMetadata supports header-only older clients on
// the first create. Presence (including null/empty) wins over the header; later
// creates must never resurrect a removed field from the initial handshake.
func normalizeOpenAIWSInitialTurnMetadata(body []byte, handshake string, turn int) ([]byte, error) {
	if turn != 1 {
		return body, nil
	}
	return normalizeOpenAIRequestTurnMetadata(body, handshake)
}

// HTTP headers belong to the current request; WS callers must use the guarded
// first-create wrapper above. Explicit body presence always takes precedence.
func normalizeOpenAIRequestTurnMetadata(body []byte, header string) ([]byte, error) {
	if strings.TrimSpace(header) == "" {
		return body, nil
	}
	cm := gjson.GetBytes(body, "client_metadata")
	if cm.Exists() && !cm.IsObject() {
		return body, nil
	}
	if cm.Get(openAIWSTurnMetadataHeader).Exists() {
		return body, nil
	}
	return sjson.SetBytes(body, "client_metadata."+openAIWSTurnMetadataHeader, strings.TrimSpace(header))
}

// Responses stream_options are not Chat Completions stream_options. Preserve
// the client's reasoning_summary_delivery (and future Responses extensions)
// without enabling it implicitly. Only the Chat-only include_usage is removed.
func normalizeCodexResponsesStreamOptionsMap(body map[string]any) bool {
	options, ok := body["stream_options"].(map[string]any)
	if !ok {
		return false
	}
	if _, exists := options["include_usage"]; !exists {
		return false
	}
	delete(options, "include_usage")
	if len(options) == 0 {
		delete(body, "stream_options")
	}
	return true
}

func normalizeCodexResponsesStreamOptionsBody(body []byte) ([]byte, bool, error) {
	options := gjson.GetBytes(body, "stream_options")
	if !options.IsObject() || !options.Get("include_usage").Exists() {
		return body, false, nil
	}
	next, err := sjson.DeleteBytes(body, "stream_options.include_usage")
	if err != nil {
		return body, false, err
	}
	if len(gjson.GetBytes(next, "stream_options").Map()) == 0 {
		next, err = sjson.DeleteBytes(next, "stream_options")
	}
	return next, true, err
}
