package service

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const openAIWSThreadScopeContextKey = "openai_ws_thread_scope_v1"

type openAIWSThreadScope struct {
	hash     string
	explicit bool
	threadID string
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
	scope := openAIWSExecutionIdentity(c, body, getAPIKeyIDFromContext(c), uuid.NewString)
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
