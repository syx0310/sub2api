package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const openAIWSThreadScopeContextKey = "openai_ws_thread_scope_v1"

type openAIWSThreadScope struct {
	hash     string
	explicit bool
}

// resolveOpenAIWSThreadScope freezes the caller's identity before any outbound
// fingerprint/model rewrite. Root session and prompt cache identity deliberately
// remain separate: Codex siblings share them while owning distinct sockets.
func resolveOpenAIWSThreadScope(c *gin.Context, body []byte) openAIWSThreadScope {
	if c == nil {
		return openAIWSThreadScope{}
	}
	if value, ok := c.Get(openAIWSThreadScopeContextKey); ok {
		if scope, ok := value.(openAIWSThreadScope); ok {
			return scope
		}
	}
	sessionID, threadID := "", ""
	if c.Request != nil {
		sessionID = extractClientSessionID(c.Request.Header)
		threadID = strings.TrimSpace(c.GetHeader("thread-id"))
		if threadID == "" {
			threadID = strings.TrimSpace(c.GetHeader("thread_id"))
		}
	}
	if len(body) > 0 && (sessionID == "" || threadID == "") {
		fields := gjson.GetManyBytes(body, "client_metadata.session_id", "client_metadata.thread_id")
		if sessionID == "" && fields[0].Type == gjson.String {
			sessionID = strings.TrimSpace(fields[0].String())
		}
		if threadID == "" && fields[1].Type == gjson.String {
			threadID = strings.TrimSpace(fields[1].String())
		}
	}
	scope := openAIWSThreadScope{explicit: threadID != ""}
	identity := threadID
	if identity == "" {
		// A shared root session or shared prompt_cache_key cannot prove exclusive
		// ownership. Anonymous callers get request/connection-local state only.
		identity = uuid.NewString()
	}
	encoded, _ := json.Marshal([]any{getOpenAIGroupIDFromContext(c), getAPIKeyIDFromContext(c), sessionID, identity, scope.explicit})
	sum := sha256.Sum256(encoded)
	scope.hash = "thread:v1:" + hex.EncodeToString(sum[:])
	c.Set(openAIWSThreadScopeContextKey, scope)
	return scope
}

// Transport caches are both tenant/thread and upstream-account scoped. The
// owner registry intentionally omits account ID so same-thread account failover
// cannot leave an older inbound connection competing with its replacement.
func openAIWSThreadStateHash(c *gin.Context, body []byte, account *Account) string {
	scope := resolveOpenAIWSThreadScope(c, body)
	if scope.hash == "" || account == nil {
		return ""
	}
	return fmt.Sprintf("%s:account:%d", scope.hash, account.ID)
}

func openAIWSPoolThreadScope(c *gin.Context, account *Account) string {
	if !resolveOpenAIWSThreadScope(c, nil).explicit {
		// Anonymous requests retain stateless transport pooling, but never share
		// thread-state hints or claim ownership from a common session/cache key.
		return ""
	}
	return openAIWSThreadStateHash(c, nil, account)
}
