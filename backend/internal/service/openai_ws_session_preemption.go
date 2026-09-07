package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var errOpenAIWSSessionPreempted = errors.New("openai ws session preempted by newer request")

const (
	openAIWSSessionPreemptOwnerTTL      = 2 * time.Hour
	openAIWSSessionPreemptWatchInterval = 2 * time.Second
	openAIWSSessionPreemptCachePrefix   = "wspreempt:"
)

// OpenAIWSSessionPreemptionCache is an optional GatewayCache capability. The
// production Redis cache implements all operations atomically; cache stubs do
// not need to implement it for ordinary gateway tests.
type OpenAIWSSessionPreemptionCache interface {
	ClaimOpenAIResponsesSessionWindow(ctx context.Context, groupID int64, sessionHash string, owner []byte, ttl time.Duration) ([]byte, error)
	CompareAndRefreshOpenAIResponsesSessionWindow(ctx context.Context, groupID int64, sessionHash string, expected []byte, ttl time.Duration) (bool, error)
	CompareAndDeleteOpenAIResponsesSessionWindow(ctx context.Context, groupID int64, sessionHash string, expected []byte) (bool, error)
}

func NewOpenAIWSSessionPreemptedError() error {
	return errOpenAIWSSessionPreempted
}

type openAIWSSessionPreemptKey struct {
	groupID     int64
	apiKeyID    int64
	sessionHash string
}

type openAIWSSessionPreemptContextKey struct{}

type openAIWSSessionPreemptRegistration struct {
	mu     sync.Mutex
	active bool
	detach func()
}

// Serialize cache writes with ownership loss. A canceled socket must not write
// stale turn-state/connection hints after its replacement has claimed the thread.
func withOpenAIWSActiveOwner(ctx context.Context, write func()) {
	if ctx == nil || ctx.Err() != nil {
		return
	}
	if registration, _ := ctx.Value(openAIWSSessionPreemptContextKey{}).(*openAIWSSessionPreemptRegistration); registration != nil {
		registration.mu.Lock()
		defer registration.mu.Unlock()
		if !registration.active || ctx.Err() != nil {
			return
		}
	}
	write()
}

// BeginOpenAIWSIngressSessionPreemption keeps a persistent inbound WS session
// registered across upstream retry attempts. Nested forwarding calls reuse the
// registration so returning from one attempt cannot create a preemption gap.
func (s *OpenAIGatewayService) BeginOpenAIWSIngressSessionPreemption(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	firstClientMessage []byte,
	forwardModels ...string,
) (context.Context, func(), bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	scope := resolveOpenAIWSThreadScope(c, firstClientMessage)
	forwardModel := ""
	if len(forwardModels) > 0 {
		forwardModel = forwardModels[0]
	}
	route, routeErr := s.resolveOpenAIWSIngressRoute(account, firstClientMessage, forwardModel)
	registration, _ := ctx.Value(openAIWSSessionPreemptContextKey{}).(*openAIWSSessionPreemptRegistration)
	if routeErr != nil || route.mode == OpenAIWSIngressModePassthrough || !scope.explicit ||
		account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth {
		if registration != nil {
			// A failover can change the effective transport. Remove the old owner
			// without canceling the inbound connection now running as a relay.
			registration.detach()
		}
		return ctx, func() {}, false
	}
	if registration != nil {
		registration.mu.Lock()
		active := registration.active
		registration.mu.Unlock()
		if active || isOpenAIWSSessionPreempted(ctx) {
			return ctx, func() {}, true
		}
	}

	preemptGroupID := getOpenAIGroupIDFromContext(c)
	preemptCtx, cleanup, armed, preemptedPrevious := s.beginOpenAIWSSessionPreemptContext(
		ctx,
		account,
		preemptGroupID,
		getAPIKeyIDFromContext(c),
		scope.hash,
		false,
	)
	if !armed {
		return ctx, func() {}, false
	}
	if preemptedPrevious {
		withOpenAIWSActiveOwner(preemptCtx, func() {
			if stateStore := s.getOpenAIWSStateStore(); stateStore != nil {
				stateHash := openAIWSThreadStateHash(c, firstClientMessage, account)
				stateStore.DeleteSessionTurnState(preemptGroupID, stateHash)
				stateStore.DeleteSessionConn(preemptGroupID, stateHash)
			}
		})
	}
	return preemptCtx, cleanup, true
}

func newOpenAIWSSessionPreemptKey(groupID, apiKeyID int64, sessionHash string) (openAIWSSessionPreemptKey, bool) {
	sessionHash = strings.TrimSpace(sessionHash)
	if groupID <= 0 || apiKeyID <= 0 || sessionHash == "" {
		return openAIWSSessionPreemptKey{}, false
	}
	return openAIWSSessionPreemptKey{groupID: groupID, apiKeyID: apiKeyID, sessionHash: sessionHash}, true
}

func openAIWSSessionPreemptCacheHash(apiKeyID int64, sessionHash string) string {
	return fmt.Sprintf("%s%d:%s", openAIWSSessionPreemptCachePrefix, apiKeyID, strings.TrimSpace(sessionHash))
}

type openAIWSSessionPreemptEntry struct {
	generation uint64
	cancel     func()
}

type openAIWSSessionPreemptRegistry struct {
	mu     sync.Mutex
	next   uint64
	active map[openAIWSSessionPreemptKey]openAIWSSessionPreemptEntry
}

func (r *openAIWSSessionPreemptRegistry) Begin(key openAIWSSessionPreemptKey, cancel func()) (cleanup func(), preemptedPrevious bool) {
	if r == nil || strings.TrimSpace(key.sessionHash) == "" {
		return func() {}, false
	}
	r.mu.Lock()
	if r.active == nil {
		r.active = make(map[openAIWSSessionPreemptKey]openAIWSSessionPreemptEntry)
	}
	r.next++
	generation := r.next
	previous, hadPrevious := r.active[key]
	r.active[key] = openAIWSSessionPreemptEntry{generation: generation, cancel: cancel}
	r.mu.Unlock()
	if hadPrevious && previous.cancel != nil {
		previous.cancel()
	}
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		current, ok := r.active[key]
		if ok && current.generation == generation {
			delete(r.active, key)
		}
	}, hadPrevious
}

func (s *OpenAIGatewayService) beginOpenAIWSSessionPreemptContext(
	ctx context.Context,
	account *Account,
	groupID, apiKeyID int64,
	sessionHash string,
	httpIngressWSOneShot bool,
) (context.Context, func(), bool, bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth || httpIngressWSOneShot {
		return ctx, func() {}, false, false
	}
	key, ok := newOpenAIWSSessionPreemptKey(groupID, apiKeyID, sessionHash)
	if !ok {
		return ctx, func() {}, false, false
	}

	preemptCtx, cancel := context.WithCancelCause(ctx)
	registration := &openAIWSSessionPreemptRegistration{active: true}
	preemptCtx = context.WithValue(preemptCtx, openAIWSSessionPreemptContextKey{}, registration)
	ownerToken := uuid.NewString()
	preempt := func() {
		registration.mu.Lock()
		defer registration.mu.Unlock()
		if registration.active {
			registration.active = false
			cancel(errOpenAIWSSessionPreempted)
			logOpenAIWSModeInfo("ingress_ws_thread_preempted group_id=%d api_key_id=%d account_id=%d thread_hash=%s reason=newer_same_thread_connection",
				key.groupID, key.apiKeyID, account.ID, truncateOpenAIWSLogValue(key.sessionHash, 24))
		}
	}
	previousRemoteOwner, remoteClaimed := s.claimOpenAIWSSessionPreemptOwner(ctx, key, ownerToken)
	preemptedPrevious := remoteClaimed && previousRemoteOwner != "" && previousRemoteOwner != ownerToken
	cleanupLocal, hadLocalPrevious := s.openaiWSSessionPreemptions.Begin(key, preempt)
	preemptedPrevious = preemptedPrevious || hadLocalPrevious
	stopWatch := func() {}
	if remoteClaimed {
		stopWatch = s.watchOpenAIWSSessionPreemptOwner(preemptCtx, key, ownerToken, preempt)
	}

	var detachOnce sync.Once
	registration.detach = func() {
		detachOnce.Do(func() {
			registration.mu.Lock()
			registration.active = false
			registration.mu.Unlock()
			stopWatch()
			cleanupLocal()
			if remoteClaimed {
				s.releaseOpenAIWSSessionPreemptOwner(context.Background(), key, ownerToken)
			}
		})
	}
	return preemptCtx, func() {
		registration.detach()
		cancel(nil)
	}, true, preemptedPrevious
}

func (s *OpenAIGatewayService) openAIWSSessionPreemptionCache() OpenAIWSSessionPreemptionCache {
	if s == nil || s.cache == nil {
		return nil
	}
	cache, _ := s.cache.(OpenAIWSSessionPreemptionCache)
	return cache
}

func (s *OpenAIGatewayService) claimOpenAIWSSessionPreemptOwner(ctx context.Context, key openAIWSSessionPreemptKey, ownerToken string) (string, bool) {
	cache := s.openAIWSSessionPreemptionCache()
	if cache == nil || strings.TrimSpace(ownerToken) == "" {
		return "", false
	}
	cacheCtx, cancel := context.WithTimeout(ctx, openAIWSStateStoreRedisTimeout)
	defer cancel()
	previous, err := cache.ClaimOpenAIResponsesSessionWindow(
		cacheCtx,
		key.groupID,
		openAIWSSessionPreemptCacheHash(key.apiKeyID, key.sessionHash),
		[]byte(strings.TrimSpace(ownerToken)),
		openAIWSSessionPreemptOwnerTTL,
	)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(previous)), true
}

func (s *OpenAIGatewayService) releaseOpenAIWSSessionPreemptOwner(ctx context.Context, key openAIWSSessionPreemptKey, ownerToken string) {
	cache := s.openAIWSSessionPreemptionCache()
	if cache == nil || strings.TrimSpace(ownerToken) == "" {
		return
	}
	cacheCtx, cancel := context.WithTimeout(ctx, openAIWSStateStoreRedisTimeout)
	defer cancel()
	_, _ = cache.CompareAndDeleteOpenAIResponsesSessionWindow(
		cacheCtx,
		key.groupID,
		openAIWSSessionPreemptCacheHash(key.apiKeyID, key.sessionHash),
		[]byte(strings.TrimSpace(ownerToken)),
	)
}

func (s *OpenAIGatewayService) watchOpenAIWSSessionPreemptOwner(ctx context.Context, key openAIWSSessionPreemptKey, ownerToken string, onLost func()) func() {
	cache := s.openAIWSSessionPreemptionCache()
	if cache == nil || onLost == nil || strings.TrimSpace(ownerToken) == "" {
		return func() {}
	}
	stopCh := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(openAIWSSessionPreemptWatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				cacheCtx, cancel := context.WithTimeout(context.Background(), openAIWSStateStoreRedisTimeout)
				owned, err := cache.CompareAndRefreshOpenAIResponsesSessionWindow(
					cacheCtx,
					key.groupID,
					openAIWSSessionPreemptCacheHash(key.apiKeyID, key.sessionHash),
					[]byte(strings.TrimSpace(ownerToken)),
					openAIWSSessionPreemptOwnerTTL,
				)
				cancel()
				if err == nil && !owned {
					onLost()
					return
				}
			}
		}
	}()
	return func() { once.Do(func() { close(stopCh) }) }
}

func isOpenAIWSSessionPreempted(ctx context.Context) bool {
	return ctx != nil && errors.Is(context.Cause(ctx), errOpenAIWSSessionPreempted)
}

func IsOpenAIWSSessionPreemptedError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errOpenAIWSSessionPreempted) {
		return true
	}
	var fallbackErr *openAIWSFallbackError
	return errors.As(err, &fallbackErr) && fallbackErr != nil && strings.TrimPrefix(strings.TrimSpace(fallbackErr.Reason), "prewarm_") == "session_preempted"
}
