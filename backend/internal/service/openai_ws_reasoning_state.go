package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const openAIWSReasoningMaxEntries = 4096

var errOpenAIWSReasoningContinuation = errors.New("reasoning continuation state is unavailable; reconnect to resend the full request")

func openAIWSReasoningPolicyCloseError(err error) error {
	status := coderws.StatusPolicyViolation
	if errors.Is(err, errOpenAIWSReasoningContinuation) {
		status = coderws.StatusTryAgainLater
	}
	return NewOpenAIWSClientCloseError(status, err.Error(), err)
}

// A small summary of accepted state, not a copy of conversation history.
type openAIWSReasoningValue struct {
	requested, policyEffort     string
	known, override, tailConfig bool
}

type openAIWSReasoningBinding struct {
	value     openAIWSReasoningValue
	expiresAt time.Time
}

type openAIWSReasoningAccepted struct {
	key string
	openAIWSReasoningBinding
}

type openAIWSReasoningCache struct {
	mu      sync.Mutex
	entries map[string]openAIWSReasoningBinding
}

func (c *openAIWSReasoningCache) get(scope, responseID string) (openAIWSReasoningValue, bool) {
	key := openAIWSReasoningKey(scope, responseID)
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if ok && time.Now().Before(entry.expiresAt) {
		return entry.value, true
	}
	delete(c.entries, key)
	return openAIWSReasoningValue{}, false
}

func (c *openAIWSReasoningCache) put(scope, responseID string, value openAIWSReasoningValue, ttl time.Duration) {
	if scope == "" || responseID == "" || len(value.requested) > maxReasoningEffortValueLen || len(value.policyEffort) > maxReasoningEffortValueLen {
		return
	}
	key := openAIWSReasoningKey(scope, responseID)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]openAIWSReasoningBinding)
	}
	// gjson strings can be slices of a large input buffer. Copy these bounded
	// scalars so a tiny cache entry never retains the complete conversation.
	value.requested = strings.Clone(value.requested)
	value.policyEffort = strings.Clone(value.policyEffort)
	ensureBindingCapacity(c.entries, key, openAIWSReasoningMaxEntries)
	c.entries[key] = openAIWSReasoningBinding{value: value, expiresAt: time.Now().Add(ttl)}
}

func openAIWSReasoningKey(scope, responseID string) string {
	// Hash untrusted response IDs too, keeping cache keys bounded.
	return fmt.Sprintf("%x", sha256.Sum256([]byte(scope+"\x00"+responseID)))
}

// Frame values become immutable before the upstream write. Both executors use
// the same requested/effective effort for usage and timeout decisions.
type openAIWSReasoningFrame struct {
	value     openAIWSReasoningValue
	effective string
	tracked   bool
	semantics OpenAIResponsesRequestSemantics
}

type openAIWSReasoningSession struct {
	ctx    context.Context
	cache  *openAIWSReasoningCache
	scope  string
	ttl    time.Duration
	active atomic.Pointer[openAIWSReasoningFrame]
	// Pin one accepted parent per live socket so unrelated traffic evicting the
	// bounded cross-connection cache cannot interrupt an active serial session.
	last            atomic.Pointer[openAIWSReasoningAccepted]
	outputSeen      atomic.Bool
	compactionItems atomic.Uint32
}

func (s *OpenAIGatewayService) newOpenAIWSReasoningSession(ctx context.Context, c *gin.Context, account *Account, first []byte) *openAIWSReasoningSession {
	return &openAIWSReasoningSession{
		ctx: ctx, cache: &s.openaiWSReasoningCache,
		scope: openAIWSThreadStateHash(c, first, account), ttl: s.openAIWSResponseStickyTTL(),
	}
}

func (s *openAIWSReasoningSession) prepare(body []byte, model string, hooks *OpenAIWSIngressHooks) ([]byte, *openAIWSReasoningFrame, error) {
	frame := &openAIWSReasoningFrame{semantics: ClassifyOpenAIResponsesRequest(body)}
	frame.value.known = true
	maxEffort, overLimit := "", ""
	var mappings []ReasoningEffortMapping
	if hooks != nil {
		maxEffort, overLimit, mappings = hooks.MaxReasoningEffort, hooks.MaxReasoningEffortOverLimit, hooks.ReasoningEffortMappings
	}
	prev := strings.TrimSpace(gjson.GetBytes(body, "previous_response_id").String())
	var parent openAIWSReasoningValue
	var found bool
	if prev != "" && s.cache != nil {
		if last := s.last.Load(); last != nil && last.key == openAIWSReasoningKey(s.scope, prev) && time.Now().Before(last.expiresAt) {
			parent, found = last.value, true
		} else {
			parent, found = s.cache.get(s.scope, prev)
		}
		found = found && parent.known
	}
	_, explicit, hasUpdate := lastOpenAIConfigurationEffort(body)
	reset := openAIInputHasCompactedHistory(body)
	next := body
	var err error
	switch {
	case hasUpdate:
		frame.tracked, frame.value.override = true, true
		frame.value.requested = explicit
		next, _, err = ApplyReasoningEffortPolicy(body, maxEffort, mappings, overLimit)
		_, frame.value.policyEffort, _ = lastOpenAIConfigurationEffort(next)
	case prev != "" && !reset && !found:
		// The top-level effort is NOT evidence that the referenced conversation
		// has no override. Never report it as the actual effort or bypass a cap.
		frame.tracked, frame.value.known = true, false
		if maxEffort != "" || len(mappings) > 0 {
			err = errOpenAIWSReasoningContinuation
		}
	case prev != "" && !reset && parent.override:
		frame.tracked = true
		frame.value = parent
		var desired string
		desired, err = applyOpenAIConfigurationEffortValue(parent.requested, model, maxEffort, mappings, overLimit)
		if err == nil && desired != parent.policyEffort {
			next, err = appendOpenAIInheritedEffortUpdate(body, desired, parent.tailConfig)
		}
		frame.value.policyEffort = desired
	default:
		if requested := CanonicalRequestedReasoningEffort(body, model); requested != nil {
			frame.value.requested = *requested
		}
		next, _, err = ApplyReasoningEffortPolicy(body, maxEffort, mappings, overLimit)
		frame.value.policyEffort = explicitRequestedReasoningEffortFromBody(next)
	}
	if err != nil {
		return body, nil, err
	}
	frame.value.requested = strings.Clone(frame.value.requested)
	frame.value.policyEffort = strings.Clone(frame.value.policyEffort)
	frame.value.tailConfig = openAIInputEndsInConfiguration(next)
	if openAIInputHasNoNewItems(next) && prev != "" && found {
		frame.value.tailConfig = parent.tailConfig
	}
	return next, frame, nil
}

func (s *openAIWSReasoningSession) activate(frame *openAIWSReasoningFrame, body []byte, upstreamModel, requestModel string) {
	if frame == nil {
		return
	}
	next := *frame
	if next.tracked {
		raw := next.value.policyEffort
		if isOpenAIGPT6Model(upstreamModel) {
			raw, _ = normalizeGPT6ReasoningEffort(raw, upstreamModel)
		}
		next.effective = normalizeOpenAIReasoningEffortForModel(raw, upstreamModel)
	} else if effort := extractOpenAIReasoningEffortFromBody(body, upstreamModel, requestModel); effort != nil {
		next.effective = *effort
	}
	s.active.Store(&next)
	s.outputSeen.Store(false)
	s.compactionItems.Store(0)
}

func (s *openAIWSReasoningSession) observeTerminal(body []byte) {
	event := gjson.GetBytes(body, "type").String()
	if event == "response.output_item.done" {
		if item := gjson.GetBytes(body, "item"); item.IsObject() {
			s.outputSeen.Store(true)
			if item.Get("type").String() == "compaction" && item.Get("encrypted_content").Type == gjson.String && s.compactionItems.Load() < 2 {
				// Saturate at two: Codex requires exactly one valid compaction
				// output item, not merely a successful terminal event.
				s.compactionItems.Add(1)
			}
		}
		return
	}
	success := event == "response.completed" || event == "response.done"
	steered := event == "response.incomplete" && gjson.GetBytes(body, "response.incomplete_details.reason").String() == "steered"
	if !success && !steered {
		return
	}
	frame := s.active.Load()
	if frame == nil || s.cache == nil {
		return
	}
	id := gjson.GetBytes(body, "response.id").String()
	value := frame.value
	if output := gjson.GetBytes(body, "response.output"); s.outputSeen.Load() || (output.IsArray() && output.Get("0").Exists()) {
		value.tailConfig = false
	}
	if event == "response.completed" && frame.semantics.NativeCompactionV2 && s.compactionItems.Load() == 1 {
		// Failed compaction does not retire overrides. A successful compacted
		// response allows the next create to establish a fresh top-level pin.
		value = openAIWSReasoningValue{known: true}
	}
	if id == "" || len(value.requested) > maxReasoningEffortValueLen || len(value.policyEffort) > maxReasoningEffortValueLen {
		return
	}
	withOpenAIWSActiveOwner(s.ctx, func() {
		s.cache.put(s.scope, id, value, s.ttl)
		s.last.Store(&openAIWSReasoningAccepted{
			key:                      openAIWSReasoningKey(s.scope, id),
			openAIWSReasoningBinding: openAIWSReasoningBinding{value: value, expiresAt: time.Now().Add(s.ttl)},
		})
	})
}

func (s *openAIWSReasoningSession) beginAutomatic() {
	if previous := s.active.Load(); previous != nil {
		next := *previous
		next.semantics = OpenAIResponsesRequestSemantics{Kind: "turn"}
		next.value.tailConfig = false
		s.active.Store(&next)
		s.outputSeen.Store(false)
		s.compactionItems.Store(0)
	}
}

func (s *openAIWSReasoningSession) stamp(result *OpenAIForwardResult) {
	if result == nil {
		return
	}
	if frame := s.active.Load(); frame != nil {
		if frame.tracked {
			result.ReasoningEffort = openAIWSTrimmedStringPtr(frame.effective)
			result.RequestedReasoningEffort = openAIWSTrimmedStringPtr(NormalizeMaxReasoningEffort(frame.value.requested))
		}
		if frame.semantics.Prewarm {
			result.FirstTokenMs = nil
		}
	}
}

func (s *openAIWSReasoningSession) stampPassthrough(meta *openAIWSPassthroughUsageMeta) {
	if frame := s.active.Load(); frame != nil && frame.tracked {
		meta.reasoningEffort.Store(openAIWSTrimmedStringPtr(frame.effective))
		meta.requestedReasoningEffort.Store(openAIWSTrimmedStringPtr(NormalizeMaxReasoningEffort(frame.value.requested)))
	}
}
