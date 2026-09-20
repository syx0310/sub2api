package service

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// CodexTurnStateLengths is diagnostic data only. It must never contain the
// opaque token, or affect routing, retries, billing, or forwarded bytes.
// Nil means unobserved/not applicable; zero means observed but absent/empty.
type CodexTurnStateLengths struct {
	RequestHeaderBytes    *int `json:"request_header_bytes"`
	RequestMetadataBytes  *int `json:"request_metadata_bytes"`
	ResponseHeaderBytes   *int `json:"response_header_bytes"`
	ResponseMetadataBytes *int `json:"response_metadata_bytes"`
}

func (v CodexTurnStateLengths) Empty() bool {
	return v.RequestHeaderBytes == nil && v.RequestMetadataBytes == nil &&
		v.ResponseHeaderBytes == nil && v.ResponseMetadataBytes == nil
}

// Display returns a detached, optional admin-only view. The pointed-to integers
// are immutable: observers replace pointers rather than modifying their values.
func (v CodexTurnStateLengths) Display() *CodexTurnStateLengths {
	if v.Empty() {
		return nil
	}
	return &v
}

const codexTurnStateLengthsContextKey = "codex_turn_state_lengths"

func clearCodexTurnStateLengthObservation(c *gin.Context) {
	if c != nil {
		c.Set(codexTurnStateLengthsContextKey, (*codexTurnStateLengthObserver)(nil))
	}
}

type codexTurnStateRequestLengthKey struct{}

// Carry only the measured integer from the exact bytes used by the HTTP
// builder; never reread/retain a request body just for diagnostics.
func (s *OpenAIGatewayService) codexTurnStateRequestContext(ctx context.Context, account *Account, payload []byte) context.Context {
	if ctx == nil || account == nil || !account.IsOpenAI() || (s != nil && s.cfg != nil && s.cfg.Gateway.DisableCodexTurnStateLengthObservation) {
		return ctx
	}
	return context.WithValue(ctx, codexTurnStateRequestLengthKey{}, codexTurnStateMetadataBytes(payload))
}

// One observer belongs to one upstream attempt/response, never a pooled socket
// or an account. Only four integers are retained, not headers or payloads.
type codexTurnStateLengthObserver struct {
	mu    sync.Mutex
	value CodexTurnStateLengths
}

func (s *OpenAIGatewayService) newCodexTurnStateLengthObserver(account *Account) *codexTurnStateLengthObserver {
	if account == nil || !account.IsOpenAI() || (s != nil && s.cfg != nil && s.cfg.Gateway.DisableCodexTurnStateLengthObservation) {
		return nil
	}
	return &codexTurnStateLengthObserver{}
}

func codexTurnStateHeaderBytes(headers http.Header) *int {
	n := len(strings.TrimSpace(headers.Get(openAICodexTurnStateHeader)))
	return &n
}

func codexTurnStateMetadataBytes(payload []byte) *int {
	value := gjson.GetBytes(payload, "client_metadata.x-codex-turn-state")
	if value.Exists() && value.Type != gjson.String {
		return nil // Do not stringify invalid metadata into a pretend token.
	}
	n := len(value.Str)
	return &n
}

func (o *codexTurnStateLengthObserver) request(headers http.Header, payload []byte) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if headers != nil {
		o.value.RequestHeaderBytes = codexTurnStateHeaderBytes(headers)
	}
	o.value.RequestMetadataBytes = codexTurnStateMetadataBytes(payload)
}

func (o *codexTurnStateLengthObserver) responseHeaders(headers http.Header) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.value.ResponseHeaderBytes = codexTurnStateHeaderBytes(headers)
}

func (o *codexTurnStateLengthObserver) requestObject(headers http.Header, payload map[string]any) {
	if o == nil {
		return
	}
	o.request(headers, nil)
	metadata, ok := payload["client_metadata"].(map[string]any)
	if !ok {
		return
	}
	value, present := metadata[openAICodexTurnStateHeader]
	if !present {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if text, valid := value.(string); valid {
		n := len(text)
		o.value.RequestMetadataBytes = &n
	} else {
		o.value.RequestMetadataBytes = nil
	}
}

func (o *codexTurnStateLengthObserver) event(payload []byte, eventType string) {
	if o == nil || eventType != "response.metadata" {
		return
	}
	headers := gjson.GetBytes(payload, "headers")
	if !headers.IsObject() {
		return
	}
	n := 0
	valid := true
	headers.ForEach(func(key, value gjson.Result) bool {
		if !strings.EqualFold(key.Str, openAICodexTurnStateHeader) {
			return true
		}
		// Match Codex's header representation (string, or first string in an array).
		if value.IsArray() {
			value = value.Get("0")
		}
		valid = value.Type == gjson.String
		if valid {
			n = len(value.Str)
		}
		return false
	})
	if !valid {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	// Keep the first non-empty declaration in this response, not the latest
	// event. This is observed data, not a claim about the client's active token.
	if o.value.ResponseMetadataBytes == nil || *o.value.ResponseMetadataBytes == 0 {
		o.value.ResponseMetadataBytes = &n
	}
}

func (o *codexTurnStateLengthObserver) snapshot() CodexTurnStateLengths {
	if o == nil {
		return CodexTurnStateLengths{}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.value
}

func codexTurnStateObserverFromContext(c *gin.Context) *codexTurnStateLengthObserver {
	if c == nil {
		return nil
	}
	raw, _ := c.Get(codexTurnStateLengthsContextKey)
	o, _ := raw.(*codexTurnStateLengthObserver)
	return o
}

func observedCodexTurnStateLengths(c *gin.Context) CodexTurnStateLengths {
	return codexTurnStateObserverFromContext(c).snapshot()
}

// ObservedCodexTurnStateLengths takes a value-only snapshot before an HTTP
// handler queues asynchronous usage recording. Never retain Gin in that task.
func ObservedCodexTurnStateLengths(c *gin.Context) CodexTurnStateLengths {
	return observedCodexTurnStateLengths(c)
}

func (s *OpenAIGatewayService) observeCodexTurnStateHTTPRequest(c *gin.Context, account *Account, request *http.Request, payload []byte) *codexTurnStateLengthObserver {
	o := s.newCodexTurnStateLengthObserver(account)
	if request != nil && o != nil {
		o.value.RequestHeaderBytes = codexTurnStateHeaderBytes(request.Header)
		if value, ok := request.Context().Value(codexTurnStateRequestLengthKey{}).(*int); ok {
			o.value.RequestMetadataBytes = value
		} else {
			o.value.RequestMetadataBytes = codexTurnStateMetadataBytes(payload)
		}
	}
	if c != nil {
		c.Set(codexTurnStateLengthsContextKey, o) // Replace even when disabled/non-OpenAI.
	}
	return o
}
