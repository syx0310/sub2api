package handler

import "github.com/Wei-Shaw/sub2api/internal/service"

// The first frame, subsequent creates and rebuilt attempts all freeze the same
// fields before async accounting. No mutable connection-wide compact flag.
func newOpenAIWSTurnRequestSnapshot(turn int, model string, payload []byte, bodyBytes *int64) *openAIWSTurnRequestSnapshot {
	return &openAIWSTurnRequestSnapshot{
		turn:               turn,
		requestedModel:     model,
		requestPayloadHash: service.HashUsageRequestPayload(payload),
		requestBodyBytes:   bodyBytes,
		semantics:          service.ClassifyOpenAIResponsesRequest(payload),
	}
}
