package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/tidwall/gjson"
)

const (
	CodexAllowCreditUsageKey = "codex_allow_credit_usage"
	codexCreditStateKey      = "codex_credit_policy_state"
	codexCreditResumeKey     = "codex_credit_usage_resume_at"
	codexCreditFreshness     = 5 * time.Minute
	codexCreditCapacity      = 4096
	codexCreditLookupTimeout = 100 * time.Millisecond
)

// This is passive admission evidence, not a balance ledger or an upstream
// disable-credits parameter. No method in this file queries or buys credits.
type codexCreditState struct {
	Credits    *OpenAICredits `json:"credits"`
	CreditsAt  int64          `json:"credits_at"`
	Quota      map[string]any `json:"quota,omitempty"`
	QuotaAt    int64          `json:"quota_at,omitempty"`
	ObservedAt int64          `json:"observed_at"`
	BlockedAt  int64          `json:"blocked_at,omitempty"`
}

type codexCreditEntry struct {
	state          codexCreditState
	lastWrite      time.Time
	writing        bool
	critical       uint64
	quotaRevision  uint64
	persistedQuota uint64
}

type codexCreditObservations struct {
	mu      sync.Mutex
	entries map[int64]*codexCreditEntry
	writes  chan struct{}
}

type codexCreditServiceContextKey struct{}

func (s *OpenAIGatewayService) codexCreditAccountSnapshot(ctx context.Context, id int64) (*Account, error) {
	// A passive observation must not inherit an unbounded DB fallback timeout
	// and stall a WS reader or its error-drain path when the cache is unavailable.
	lookupCtx, cancel := context.WithTimeout(ctx, codexCreditLookupTimeout)
	defer cancel()
	return s.schedulerSnapshot.GetAccount(lookupCtx, id)
}

func codexCreditStateFromExtra(extra map[string]any) codexCreditState {
	var state codexCreditState
	if value := extra[codexCreditStateKey]; value != nil {
		if raw, err := json.Marshal(value); err == nil {
			_ = json.Unmarshal(raw, &state)
		}
	}
	var display openAICreditsSnapshot
	if value := extra[openaiQuotaCreditsKey]; value != nil {
		if raw, err := json.Marshal(value); err == nil && json.Unmarshal(raw, &display) == nil && display.FetchedAt*1000 > state.CreditsAt {
			state.Credits, state.CreditsAt = display.Credits, display.FetchedAt*1000
		}
	}
	return state
}

func (s *OpenAIGatewayService) codexCreditState(account *Account) codexCreditState {
	state := codexCreditStateFromExtra(account.Extra)
	if s == nil {
		return state
	}
	s.codexCredits.mu.Lock()
	defer s.codexCredits.mu.Unlock()
	if entry := s.codexCredits.entries[account.ID]; entry != nil {
		if entry.state.QuotaAt >= state.QuotaAt {
			state.Quota, state.QuotaAt = entry.state.Quota, entry.state.QuotaAt
		}
		if entry.state.CreditsAt >= state.CreditsAt {
			state.Credits, state.CreditsAt = entry.state.Credits, entry.state.CreditsAt
		}
		if entry.state.BlockedAt > state.BlockedAt {
			state.BlockedAt = entry.state.BlockedAt
		}
	}
	return state
}

func codexCreditResumeAt(account *Account) int64 {
	value, _ := resolveAccountExtraNumber(account.Extra, codexCreditResumeKey)
	return int64(value)
}

func codexCreditsAvailable(state codexCreditState, now time.Time) bool {
	age := now.Sub(time.UnixMilli(state.CreditsAt))
	return state.Credits != nil && state.CreditsAt > 0 && age >= 0 && age < codexCreditFreshness && codexCreditsPositive(state.Credits)
}

func codexCreditsPositive(credits *OpenAICredits) bool {
	if credits == nil {
		return false
	}
	if credits.Unlimited {
		return true
	}
	if !credits.HasCredits {
		return false
	}
	// The native client permits an absent balance. Keep decimal strings exact,
	// but do not interpret a contradictory, empty, or malformed balance as money.
	if credits.Balance == nil {
		return true
	}
	if len(*credits.Balance) > 128 {
		return false
	}
	balance, err := strconv.ParseFloat(*credits.Balance, 64)
	return err == nil && !math.IsInf(balance, 0) && balance > 0
}

// Materialize an immutable view for quota checks. A shadow uses its own quota
// window but inherits the credential owner's permission and credit evidence.
func codexCreditPolicyView(ctx context.Context, account *Account) (*Account, codexCreditState) {
	var s *OpenAIGatewayService
	if ctx != nil {
		s, _ = ctx.Value(codexCreditServiceContextKey{}).(*OpenAIGatewayService)
	}
	state := codexCreditStateFromExtra(account.Extra)
	if s != nil {
		state = s.codexCreditState(account)
	}
	quotaUpdated, _ := parseTime(fmt.Sprint(account.Extra["codex_usage_updated_at"]))
	overlayQuota := state.QuotaAt > 0 && state.QuotaAt >= quotaUpdated.UnixMilli()
	if !account.IsShadow() && !overlayQuota {
		return account, state
	}
	view := *account
	view.Extra = maps.Clone(account.Extra)
	if view.Extra == nil {
		view.Extra = make(map[string]any)
	}
	if overlayQuota {
		for key, value := range state.Quota {
			view.Extra[key] = value
		}
	}
	if account.IsShadow() && account.ParentAccountID != nil {
		// Never treat a shadow row's copied toggle as independent authorization.
		delete(view.Extra, CodexAllowCreditUsageKey)
		delete(view.Extra, codexCreditResumeKey)
		if s != nil && s.schedulerSnapshot != nil {
			if parent, err := s.codexCreditAccountSnapshot(ctx, *account.ParentAccountID); err == nil && parent != nil {
				view.Extra[CodexAllowCreditUsageKey] = parent.Extra[CodexAllowCreditUsageKey]
				view.Extra[codexCreditResumeKey] = parent.Extra[codexCreditResumeKey]
				ownerState := s.codexCreditState(parent)
				state.Credits, state.CreditsAt = ownerState.Credits, ownerState.CreditsAt
				if ownerState.BlockedAt > state.BlockedAt {
					state.BlockedAt = ownerState.BlockedAt
				}
			}
		}
	}
	return &view, state
}

func codexCreditQuotaDecision(account *Account, state codexCreditState, now time.Time) (bool, openAIQuotaAutoPauseDecision) {
	if !account.IsOpenAIOAuthLike() {
		return false, openAIQuotaAutoPauseDecision{}
	}
	allowed := resolveAccountExtraBool(account.Extra, CodexAllowCreditUsageKey)
	for _, window := range []string{"5h", "7d"} {
		// Unlike a heuristic pause, known exhaustion must not age into permission
		// to spend credits. Only a real quota reset or a newer observation clears it.
		if readOpenAIQuotaUsedPercent(account.Extra, window) < 100 || openAIQuotaWindowReset(account.Extra, window, now) {
			continue
		}
		reason := "codex_credit_usage_disabled"
		if allowed {
			switch {
			case state.BlockedAt > codexCreditResumeAt(account):
				reason = "codex_credit_usage_manual_resume_required"
			case !codexCreditsAvailable(state, now):
				reason = "codex_credit_usage_unknown_or_empty"
			default:
				continue
			}
		}
		return true, openAIQuotaAutoPauseDecision{window: window, threshold: 1, utilization: readOpenAIQuotaUsedPercent(account.Extra, window) / 100, reason: reason}
	}
	return false, openAIQuotaAutoPauseDecision{}
}

// CheckOpenAICreditPolicy also gates later turns on an already-open socket.
// It reads the existing scheduler cache, never /wham/usage or a purchase API.
func (s *OpenAIGatewayService) CheckOpenAICreditPolicy(ctx context.Context, account *Account) error {
	if s == nil || account == nil || !account.IsOpenAIOAuthLike() {
		return nil
	}
	if s.schedulerSnapshot != nil {
		latest, err := s.codexCreditAccountSnapshot(ctx, account.ID)
		if err != nil {
			return fmt.Errorf("credit policy snapshot unavailable: %w", err)
		}
		if latest != nil {
			account = latest
		}
	}
	ctx = s.withOpenAIQuotaAutoPauseContext(ctx)
	view, state := codexCreditPolicyView(ctx, account)
	now := time.Now()
	if paused, decision := codexCreditQuotaDecision(view, state, now); paused {
		return infraerrors.Forbidden(decision.reason, "Codex subscription quota is exhausted; the account credit policy does not allow this request")
	}
	threshold5h, threshold7d := resolveOpenAIQuotaAutoPauseThresholds(ctx, view)
	for window, threshold := range map[string]float64{"5h": threshold5h, "7d": threshold7d} {
		if threshold <= 0 || threshold >= 1 || resolveAccountExtraBool(view.Extra, "auto_pause_"+window+"_disabled") {
			continue
		}
		if utilization, ok := resolveOpenAIQuotaUtilization(view.Extra, window, now); ok && utilization >= threshold {
			return infraerrors.Forbidden("codex_manual_quota_threshold", "Codex account manual quota threshold denies this request")
		}
	}
	return nil
}

func parseCodexCreditsHeaders(headers http.Header) (*OpenAICredits, bool) {
	has, e1 := strconv.ParseBool(strings.TrimSpace(headers.Get("x-codex-credits-has-credits")))
	unlimited, e2 := strconv.ParseBool(strings.TrimSpace(headers.Get("x-codex-credits-unlimited")))
	if e1 != nil || e2 != nil {
		return nil, false
	}
	credits := &OpenAICredits{HasCredits: has, Unlimited: unlimited}
	if balance := strings.TrimSpace(headers.Get("x-codex-credits-balance")); balance != "" {
		if len(balance) > 128 {
			balance = "invalid"
		}
		balance = strings.Clone(balance)
		credits.Balance = &balance
	}
	return credits, true
}

func codexCreditEventHeaders(payload []byte) http.Header {
	// Avoid copying/parsing every output delta (or large image frame).
	kind := gjson.GetBytes(payload, "type").String()
	if kind != "codex.rate_limits" && kind != "error" {
		return nil
	}
	root := gjson.ParseBytes(payload)
	headers := make(http.Header)
	if kind == "error" {
		// OAuth WS errors can carry the same passive headers as an HTTP error.
		// Read only the bounded, known fields; never retain arbitrary headers.
		for _, key := range []string{"has-credits", "unlimited", "balance"} {
			name := "x-codex-credits-" + key
			value := codexCreditErrorHeader(root.Get("headers"), name)
			if value.Exists() && len(value.String()) <= 128 {
				headers.Set(name, value.String())
			} else if key == "balance" && value.Exists() {
				headers.Set(name, "invalid")
			}
		}
		for _, prefix := range []string{"x-codex-", "x-codex-bengalfox-"} {
			for _, window := range []string{"primary", "secondary"} {
				for _, field := range []string{"used-percent", "reset-after-seconds", "window-minutes"} {
					name := prefix + window + "-" + field
					value := codexCreditErrorHeader(root.Get("headers"), name)
					if value.Exists() && len(value.String()) <= 32 {
						headers.Set(name, value.String())
					}
				}
			}
		}
		return headers
	}
	for _, name := range []string{"has_credits", "unlimited"} {
		value := root.Get("credits." + name)
		if value.Type == gjson.True || value.Type == gjson.False {
			headers.Set("x-codex-credits-"+strings.ReplaceAll(name, "_", "-"), value.Raw)
		}
	}
	if value := root.Get("credits.balance"); value.Exists() && value.Type != gjson.Null {
		balance := "invalid"
		switch value.Type {
		case gjson.String:
			balance = value.Str
		case gjson.Number:
			// Some OAuth-compatible peers use a JSON number. Preserve its exact
			// decimal spelling instead of dropping an explicitly reported zero.
			balance = value.Raw
		}
		if len(balance) > 128 || strings.TrimSpace(balance) == "" {
			balance = "invalid"
		}
		headers.Set("x-codex-credits-balance", balance)
	}
	setCodexCreditEventQuotaHeaders(headers, root.Get("rate_limits"))
	return headers
}

func codexCreditErrorHeader(object gjson.Result, name string) gjson.Result {
	value := object.Get(name)
	if !value.Exists() {
		value = object.Get(http.CanonicalHeaderKey(name))
	}
	if value.IsArray() {
		value = value.Get("0")
	}
	return value
}

func setCodexCreditEventQuotaHeaders(headers http.Header, limits gjson.Result) {
	for _, window := range []string{"primary", "secondary"} {
		for _, field := range []string{"used_percent", "reset_after_seconds", "window_minutes"} {
			value := limits.Get(window + "." + field)
			if value.Type == gjson.Number {
				headers.Set("x-codex-"+window+"-"+strings.ReplaceAll(field, "_", "-"), value.Raw)
			}
		}
		// Native Codex uses an absolute reset_at, unlike HTTP's reset-after.
		if at := limits.Get(window + ".reset_at"); at.Type == gjson.Number && at.Int() > 0 {
			seconds := at.Int() - time.Now().Unix()
			if seconds < 0 {
				seconds = 0
			}
			if seconds <= 366*24*60*60 {
				headers.Set("x-codex-"+window+"-reset-after-seconds", strconv.FormatInt(seconds, 10))
			}
		}
	}
}

func codexCreditLimitMatches(account *Account, name string) bool {
	name = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "_", "-")
	if !account.IsShadow() {
		return name == "" || name == "codex"
	}
	return account.QuotaDimension == QuotaDimensionSpark &&
		(name == "codex-spark" || name == "bengalfox" || name == "codex-bengalfox" || name == "gpt-5.3-codex-spark")
}

func (s *OpenAIGatewayService) observeCodexCreditEvent(ctx context.Context, account *Account, payload []byte) {
	if account == nil || !account.IsOpenAIOAuthLike() {
		return
	}
	headers := codexCreditEventHeaders(payload)
	if headers == nil {
		return
	}
	limit := gjson.GetBytes(payload, "metered_limit_name").String()
	if limit == "" {
		limit = gjson.GetBytes(payload, "limit_name").String()
	}
	// Unknown per-feature windows must not contaminate the normal or Spark quota.
	quotaMatches := codexCreditLimitMatches(account, limit)
	if !quotaMatches {
		for key := range headers {
			if strings.HasPrefix(strings.ToLower(key), "x-codex-primary-") || strings.HasPrefix(strings.ToLower(key), "x-codex-secondary-") {
				delete(headers, key)
			}
		}
		// OAuth can bundle feature quotas in an object; /wham uses an array.
		// Select only this shadow's quota, never the parent's main window.
		additional := gjson.GetBytes(payload, "additional_rate_limits")
		seen := 0
		additional.ForEach(func(key, value gjson.Result) bool {
			seen++
			if seen > 8 {
				return false
			}
			name := key.String()
			if additional.IsArray() {
				for _, field := range []string{"metered_feature", "metered_limit_name", "limit_name", "name"} {
					if candidate := value.Get(field).String(); candidate != "" {
						name = candidate
						break
					}
				}
			}
			if !account.IsShadow() || !codexCreditLimitMatches(account, name) {
				return true
			}
			limits := value.Get("rate_limit")
			if !limits.IsObject() {
				limits = value
			}
			setCodexCreditEventQuotaHeaders(headers, limits)
			quotaMatches = true
			return false
		})
	}
	s.observeCodexCreditHeaders(ctx, account, headers, quotaMatches)
}

func (s *OpenAIGatewayService) observeCodexCreditHeaders(ctx context.Context, account *Account, headers http.Header, quotaMatches bool) {
	if s == nil || account == nil || account.ID <= 0 || !account.IsOpenAIOAuthLike() {
		return
	}
	credits, present := parseCodexCreditsHeaders(headers)
	var quota map[string]any
	quotaHeaders := headers
	if !quotaMatches && account.IsShadow() && account.QuotaDimension == QuotaDimensionSpark {
		quotaHeaders = make(http.Header)
		for _, window := range []string{"primary", "secondary"} {
			for _, field := range []string{"used-percent", "reset-after-seconds", "window-minutes"} {
				if value := headers.Get("x-codex-bengalfox-" + window + "-" + field); value != "" {
					quotaHeaders.Set("x-codex-"+window+"-"+field, value)
				}
			}
		}
		quotaMatches = len(quotaHeaders) > 0
	}
	if quotaMatches {
		if parsed := ParseCodexRateLimitHeaders(quotaHeaders); parsed != nil {
			quota = buildCodexUsageExtraUpdates(parsed, time.Now())
		}
	}
	if !present && len(quota) == 0 {
		return
	}
	if account.IsShadow() && account.ParentAccountID != nil && present {
		if s.schedulerSnapshot != nil {
			if parent, err := s.codexCreditAccountSnapshot(ctx, *account.ParentAccountID); err == nil && parent != nil {
				s.storeCodexCreditObservation(parent, credits, true, nil)
			}
		}
		// A canceled/missing parent lookup must not overwrite a durable exhaustion
		// latch with a newly manufactured empty owner state. Missing evidence stays
		// unknown; the shadow's own quota can still be observed independently.
		present = false
	}
	s.storeCodexCreditObservation(account, credits, present, quota)
}

func (s *OpenAIGatewayService) observeCodexCreditSSEBody(ctx context.Context, account *Account, body []byte) {
	if account == nil || !account.IsOpenAIOAuthLike() {
		return
	}
	// Non-streaming callers may still receive a native OAuth SSE body upstream.
	// Iterate without allocating a second body copy or a slice for every line.
	for line := range bytes.SplitSeq(body, []byte{'\n'}) {
		if data, ok := bytes.CutPrefix(line, []byte("data:")); ok {
			s.observeCodexCreditEvent(ctx, account, bytes.TrimSpace(data))
		}
	}
}

func (s *OpenAIGatewayService) storeCodexCreditObservation(account *Account, credits *OpenAICredits, present bool, quota map[string]any) {
	if !present && len(quota) == 0 {
		return
	}
	now := time.Now()
	c := &s.codexCredits
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[int64]*codexCreditEntry)
		c.writes = make(chan struct{}, 4)
	}
	entry := c.entries[account.ID]
	if entry == nil {
		if len(c.entries) >= codexCreditCapacity {
			var oldestID int64
			oldest := int64(^uint64(0) >> 1)
			for id, item := range c.entries {
				if !item.writing && item.state.ObservedAt < oldest {
					oldestID, oldest = id, item.state.ObservedAt
				}
			}
			if oldestID == 0 {
				c.mu.Unlock()
				return
			}
			delete(c.entries, oldestID)
		}
		entry = &codexCreditEntry{state: codexCreditStateFromExtra(account.Extra)}
		c.entries[account.ID] = entry
	}
	before := entry.state
	if present {
		entry.state.Credits, entry.state.CreditsAt = credits, now.UnixMilli()
	}
	merged := maps.Clone(entry.state.Quota)
	if merged == nil {
		merged = make(map[string]any)
	}
	for key, value := range quota {
		merged[key] = value
	}
	entry.state.Quota, entry.state.ObservedAt = merged, now.UnixMilli()
	if len(quota) > 0 {
		entry.state.QuotaAt = now.UnixMilli()
		entry.quotaRevision++
	}
	// Keep a credit-exhaustion latch even when a later passive frame has money.
	// Only the administrator's off->on transition authorizes resuming overages.
	if present && !codexCreditsPositive(credits) && entry.state.BlockedAt <= codexCreditResumeAt(account) {
		entry.state.BlockedAt = now.UnixMilli()
		if entry.state.BlockedAt <= codexCreditResumeAt(account) {
			entry.state.BlockedAt = codexCreditResumeAt(account) + 1
		}
	}
	critical := before.BlockedAt != entry.state.BlockedAt
	for _, window := range []string{"5h", "7d"} {
		critical = critical || (readOpenAIQuotaUsedPercent(before.Quota, window) >= 100) != (readOpenAIQuotaUsedPercent(merged, window) >= 100)
	}
	if critical {
		entry.critical++
	}
	if s.accountRepo == nil || entry.writing || (!critical && now.Sub(entry.lastWrite) < 30*time.Second) {
		c.mu.Unlock()
		return
	}
	select {
	case c.writes <- struct{}{}:
	default:
		c.mu.Unlock()
		return
	}
	entry.writing, entry.lastWrite = true, now
	c.mu.Unlock()
	go func() {
		defer func() { <-c.writes }()
		writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Coalesce observations, including exhaustion arriving during a write.
		// Bound both concurrent workers and each worker's write count/time.
		for attempt := 0; attempt < 2; attempt++ {
			c.mu.Lock()
			state, revision := entry.state, entry.critical
			quotaRevision := entry.quotaRevision
			writeQuota := quotaRevision != entry.persistedQuota
			c.mu.Unlock()
			updates := map[string]any{codexCreditStateKey: state}
			if writeQuota {
				// Reuse the existing quota display/cache too, so passive WS data
				// does not leave the UI stale and provoke an unnecessary probe.
				for key, value := range state.Quota {
					updates[key] = value
				}
			}
			if state.CreditsAt > 0 {
				updates[openaiQuotaCreditsKey] = openAICreditsSnapshot{Credits: state.Credits, FetchedAt: state.CreditsAt / 1000}
			}
			// Deliberately do not call the auto-reset workflow or its notifications.
			err := s.accountRepo.UpdateExtra(writeCtx, account.ID, updates)
			c.mu.Lock()
			if err == nil && writeQuota {
				entry.persistedQuota = quotaRevision
			}
			pending := entry.critical != revision
			if err != nil || (pending && attempt == 1) {
				entry.lastWrite = time.Time{}
			}
			if err != nil || !pending || attempt == 1 {
				entry.writing = false
				c.mu.Unlock()
				return
			}
			c.mu.Unlock()
		}
	}()
}

func normalizeCodexCreditPolicyExtra(platform, accountType string, shadow bool, extra map[string]any, previous *Account) (map[string]any, error) {
	if extra == nil {
		return nil, nil
	}
	extra = maps.Clone(extra)
	delete(extra, codexCreditStateKey)
	delete(extra, codexCreditResumeKey)
	if previous != nil {
		for _, key := range []string{codexCreditStateKey, codexCreditResumeKey} {
			if v, ok := previous.Extra[key]; ok {
				extra[key] = v
			}
		}
	}
	if value, ok := extra[CodexAllowCreditUsageKey]; ok {
		enabled, valid := value.(bool)
		if !valid || platform != PlatformOpenAI || accountType != AccountTypeOAuth || shadow {
			return nil, infraerrors.BadRequest("INVALID_CODEX_CREDIT_POLICY", "codex_allow_credit_usage must be a boolean on an OpenAI OAuth parent account")
		}
		if previous != nil && enabled && !resolveAccountExtraBool(previous.Extra, CodexAllowCreditUsageKey) {
			resume := time.Now().UnixMilli()
			if blocked := codexCreditStateFromExtra(previous.Extra).BlockedAt; blocked > resume {
				resume = blocked
			}
			extra[codexCreditResumeKey] = resume
		}
	}
	return extra, nil
}
