package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	openAIWSThreadIDHeader = "thread-id"
	openAIWSWindowIDHeader = "x-codex-window-id"
	// Prewarm and compaction reuse the main turn's continuation. Detached
	// memory and guardian requests must not claim that same execution lane.
	openAIWSRequestKindTurn       = "turn"
	openAIWSRequestKindPrewarm    = "prewarm"
	openAIWSRequestKindCompaction = "compaction"
)

type openAIWSExecutionMetadata struct {
	clientMetadata gjson.Result
	turnMetadata   gjson.Result
	turnFromBody   bool
}

// Body metadata is the canonical per-request snapshot. Presence, including
// null/empty, prevents a stale handshake snapshot from being substituted.
// Extract client_metadata once: scanning a large input for each missing identity
// projection (and again for anonymous requests) is expensive under race as well
// as in production. The snapshot never retains the full request body.
func readOpenAIWSExecutionMetadata(c *gin.Context, body []byte) openAIWSExecutionMetadata {
	metadata := openAIWSExecutionMetadata{clientMetadata: gjson.GetBytes(body, "client_metadata")}
	field := metadata.clientMetadata.Get(openAIWSTurnMetadataHeader)
	if field.Exists() {
		metadata.turnFromBody = true
		if field.Type == gjson.String {
			metadata.turnMetadata = gjson.Parse(field.Str)
		}
		return metadata
	}
	if c != nil && c.Request != nil {
		metadata.turnMetadata = gjson.Parse(c.GetHeader(openAIWSTurnMetadataHeader))
	}
	return metadata
}

// Modern body projections precede legacy headers. Session-only requests are
// intentionally not exclusive identities: Codex siblings share a root session.
func resolveOpenAIWSClientThreadID(c *gin.Context, body []byte) string {
	return readOpenAIWSExecutionMetadata(c, body).threadID(c)
}

func (m openAIWSExecutionMetadata) threadID(c *gin.Context) string {
	if m.turnFromBody {
		if id := m.turnMetadata.Get("thread_id"); id.Exists() {
			if id.Type == gjson.String {
				return strings.TrimSpace(id.Str)
			}
			return ""
		}
	}
	if id := m.clientMetadata.Get("thread_id"); id.Exists() {
		if id.Type == gjson.String {
			return strings.TrimSpace(id.Str)
		}
		return ""
	}
	if c != nil && c.Request != nil {
		for _, header := range []string{openAIWSThreadIDHeader, "thread_id"} {
			if id := strings.TrimSpace(c.GetHeader(header)); id != "" {
				return id
			}
		}
	}
	if id := strings.TrimSpace(m.turnMetadata.Get("thread_id").String()); id != "" {
		return id
	}
	if c != nil && c.Request != nil {
		window := strings.TrimSpace(c.GetHeader(openAIWSWindowIDHeader))
		if window != "" {
			return strings.TrimSpace(strings.SplitN(window, ":", 2)[0])
		}
	}
	return ""
}

func (m openAIWSExecutionMetadata) subagent(c *gin.Context) string {
	if subagent := m.clientMetadata.Get(openAISubagentHeader); subagent.Exists() {
		return strings.ToLower(strings.TrimSpace(subagent.String()))
	}
	if c != nil && c.Request != nil {
		return strings.ToLower(strings.TrimSpace(c.GetHeader(openAISubagentHeader)))
	}
	return ""
}

func resolveOpenAIWSExecutionLane(c *gin.Context, body []byte) string {
	return readOpenAIWSExecutionMetadata(c, body).lane(c)
}

func (m openAIWSExecutionMetadata) lane(c *gin.Context) string {
	requestKind := strings.ToLower(strings.TrimSpace(m.turnMetadata.Get("request_kind").String()))
	switch requestKind {
	case "", openAIWSRequestKindTurn, openAIWSRequestKindPrewarm, openAIWSRequestKindCompaction:
	default:
		return "kind=" + requestKind
	}
	if strings.TrimSpace(m.turnMetadata.Get("thread_id").String()) == "" {
		if subagent := m.subagent(c); subagent != "" {
			return "subagent=" + subagent
		}
	}
	return ""
}

// The shared constructor is used by preemption, pool affinity, HTTP forwarding
// and reasoning/turn state. Keep the fork's existing main-lane hashes stable.
func openAIWSExecutionIdentity(c *gin.Context, body []byte, apiKeyID int64, newAnonymousID func() string) openAIWSThreadScope {
	metadata := readOpenAIWSExecutionMetadata(c, body)
	threadID := metadata.threadID(c)
	lane := metadata.lane(c)
	sessionID := ""
	if session := metadata.turnMetadata.Get("session_id"); session.Type == gjson.String {
		sessionID = strings.TrimSpace(session.Str)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(metadata.clientMetadata.Get("session_id").String())
	}
	if sessionID == "" && c != nil && c.Request != nil {
		sessionID = extractClientSessionID(c.Request.Header)
	}
	scope := openAIWSThreadScope{explicit: threadID != "", threadID: strings.Clone(threadID)}
	identity := threadID
	if identity == "" {
		if newAnonymousID != nil {
			identity = newAnonymousID()
		}
		if identity == "" {
			return scope
		}
	}
	fields := []any{getOpenAIGroupIDFromContext(c), apiKeyID, sessionID, identity, scope.explicit}
	if lane != "" {
		fields = append(fields, lane)
	}
	encoded, _ := json.Marshal(fields)
	sum := sha256.Sum256(encoded)
	scope.hash = "thread:v1:" + hex.EncodeToString(sum[:])
	return scope
}

func resolveOpenAIWSExecutionScope(c *gin.Context, body []byte, apiKeyID int64) (scope, threadID string) {
	identity := openAIWSExecutionIdentity(c, body, apiKeyID, nil)
	return identity.hash, identity.threadID
}
