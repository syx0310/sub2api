//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func creditTestAccount() *Account {
	return &Account{ID: 101, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{}}
}

func TestCodexCreditPassiveParsingAndAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, credits string
		available     bool
	}{
		{"missing", `{}`, false},
		{"missing flag", `{"has_credits":true}`, false},
		{"string flags", `{"has_credits":"true","unlimited":false}`, false},
		{"exact decimal", `{"has_credits":true,"unlimited":false,"balance":"12345678901234567890.0123"}`, true},
		{"hidden balance", `{"has_credits":true,"unlimited":false}`, true},
		{"unlimited", `{"has_credits":false,"unlimited":true}`, true},
		{"empty", `{"has_credits":false,"unlimited":false,"balance":"0"}`, false},
		{"contradictory", `{"has_credits":true,"unlimited":false,"balance":"0"}`, false},
		{"numeric zero", `{"has_credits":true,"unlimited":false,"balance":0}`, false},
		{"numeric positive", `{"has_credits":true,"unlimited":false,"balance":12.25}`, true},
		{"invalid balance type", `{"has_credits":true,"unlimited":false,"balance":true}`, false},
		{"negative", `{"has_credits":true,"unlimited":false,"balance":"-1"}`, false},
		{"invalid", `{"has_credits":true,"unlimited":false,"balance":"NaN"}`, false},
		{"unbounded exponent", `{"has_credits":true,"unlimited":false,"balance":"1e999999999999999"}`, false},
		{"long balance", `{"has_credits":true,"unlimited":false,"balance":"` + strings.Repeat("9", 200) + `"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &OpenAIGatewayService{}
			a := creditTestAccount()
			a.Extra[CodexAllowCreditUsageKey] = true
			event := []byte(`{"type":"codex.rate_limits","credits":` + tc.credits + `,"rate_limits":{"primary":{"used_percent":100,"window_minutes":300,"reset_after_seconds":120}}}`)
			svc.observeCodexCreditEvent(context.Background(), a, event)
			state := svc.codexCreditState(a)
			require.Equal(t, tc.available, codexCreditsAvailable(state, time.Now()))
			ctx := svc.withOpenAIQuotaAutoPauseContext(context.Background())
			paused, _ := shouldAutoPauseOpenAIAccountByQuota(ctx, a)
			require.Equal(t, !tc.available, paused)
			if tc.name == "exact decimal" {
				require.Equal(t, "12345678901234567890.0123", *state.Credits.Balance)
			}
		})
	}
}

func TestCodexCreditDefaultUnknownStaleManualResumeAndThresholds(t *testing.T) {
	svc := &OpenAIGatewayService{}
	a := creditTestAccount()
	ctx := svc.withOpenAIQuotaAutoPauseContext(context.Background())
	require.NoError(t, svc.CheckOpenAICreditPolicy(ctx, a), "unknown quota does not trigger an active probe")
	quota := map[string]any{"codex_5h_used_percent": 100.0, "codex_5h_reset_at": time.Now().Add(time.Hour).Format(time.RFC3339)}
	svc.storeCodexCreditObservation(a, &OpenAICredits{HasCredits: true}, true, quota)
	require.ErrorContains(t, svc.CheckOpenAICreditPolicy(ctx, a), "does not allow")
	a.Extra[CodexAllowCreditUsageKey] = true
	require.NoError(t, svc.CheckOpenAICreditPolicy(ctx, a))
	for _, at := range []int64{time.Now().Add(-10 * time.Minute).UnixMilli(), time.Now().Add(time.Hour).UnixMilli()} {
		svc.codexCredits.mu.Lock()
		svc.codexCredits.entries[a.ID].state.CreditsAt = at
		svc.codexCredits.mu.Unlock()
		require.Error(t, svc.CheckOpenAICreditPolicy(ctx, a))
	}
	svc.storeCodexCreditObservation(a, &OpenAICredits{HasCredits: true}, true, nil)
	a.Extra["auto_pause_5h_threshold"] = 0.95
	paused, decision := shouldAutoPauseOpenAIAccountByQuota(ctx, a)
	require.True(t, paused)
	require.Equal(t, 0.95, decision.threshold, "credit permission cannot bypass a lower manual threshold")
	require.Error(t, svc.CheckOpenAICreditPolicy(ctx, a), "later WS turns retain manual thresholds too")
	delete(a.Extra, "auto_pause_5h_threshold")
	svc.storeCodexCreditObservation(a, &OpenAICredits{}, true, nil)
	require.Error(t, svc.CheckOpenAICreditPolicy(ctx, a))
	svc.storeCodexCreditObservation(a, &OpenAICredits{HasCredits: true}, true, nil)
	require.Error(t, svc.CheckOpenAICreditPolicy(ctx, a), "a later positive balance must not automatically resume overages")
	a.Extra[codexCreditStateKey] = svc.codexCreditState(a)
	a.Extra[CodexAllowCreditUsageKey] = false
	extra, err := normalizeCodexCreditPolicyExtra(a.Platform, a.Type, false, map[string]any{CodexAllowCreditUsageKey: true}, a)
	require.NoError(t, err)
	a.Extra = extra
	require.NoError(t, svc.CheckOpenAICreditPolicy(ctx, a), "explicit off-to-on is the only credit recovery action")
	// A subscription window reset is not a credit top-up: normal quota is usable.
	a.Extra[CodexAllowCreditUsageKey] = false
	svc.storeCodexCreditObservation(a, nil, false, map[string]any{"codex_5h_used_percent": 20.0})
	require.NoError(t, svc.CheckOpenAICreditPolicy(ctx, a))
}

func TestCodexCreditShadowUsesParentPermissionAndOwnQuota(t *testing.T) {
	parent := creditTestAccount()
	shadow := &Account{ID: 102, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent.ID, QuotaDimension: QuotaDimensionSpark, Extra: map[string]any{CodexAllowCreditUsageKey: true}}
	cache := &snapshotHydrationCache{accounts: map[int64]*Account{parent.ID: parent, shadow.ID: shadow}}
	svc := &OpenAIGatewayService{schedulerSnapshot: NewSchedulerSnapshotService(cache, nil, nil, nil, nil)}
	event := []byte(`{"type":"codex.rate_limits","metered_limit_name":"codex_spark","credits":{"has_credits":true,"unlimited":false},"rate_limits":{"primary":{"used_percent":100,"window_minutes":300,"reset_after_seconds":120}}}`)
	svc.observeCodexCreditEvent(context.Background(), shadow, event)
	require.Error(t, svc.CheckOpenAICreditPolicy(context.Background(), shadow), "shadow cannot authorize itself")
	require.NoError(t, svc.CheckOpenAICreditPolicy(context.Background(), parent), "Spark exhaustion is not parent quota exhaustion")
	parent.Extra[CodexAllowCreditUsageKey] = true
	require.NoError(t, svc.CheckOpenAICreditPolicy(context.Background(), shadow))
	require.NotNil(t, svc.codexCreditState(parent).Credits)
	require.Nil(t, svc.codexCreditState(shadow).Credits, "credit observation belongs to the credential owner")
	other := creditTestAccount()
	other.ID = 103
	svc.observeCodexCreditEvent(context.Background(), other, event)
	require.Empty(t, svc.codexCreditState(other).Quota, "unknown feature windows cannot poison normal quota")
	parent.Extra[CodexAllowCreditUsageKey] = false
	require.Error(t, svc.CheckOpenAICreditPolicy(context.Background(), shadow), "an open WS observes disabling before its next turn")
}

type creditWriteRepo struct {
	AccountRepository
	mu      sync.Mutex
	updates []map[string]any
	entered chan struct{}
	release chan struct{}
}

func (r *creditWriteRepo) UpdateExtra(ctx context.Context, _ int64, updates map[string]any) error {
	r.mu.Lock()
	r.updates = append(r.updates, maps.Clone(updates))
	first := len(r.updates) == 1
	r.mu.Unlock()
	if first && r.entered != nil {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func TestCodexCreditPassiveWritesCoalesceExhaustionAndStayBounded(t *testing.T) {
	repo := &creditWriteRepo{entered: make(chan struct{}), release: make(chan struct{})}
	svc := &OpenAIGatewayService{accountRepo: repo}
	a := creditTestAccount()
	headers := http.Header{}
	headers.Set("x-codex-credits-has-credits", "true")
	headers.Set("x-codex-credits-unlimited", "false")
	headers.Set("x-codex-primary-used-percent", "100")
	headers.Set("x-codex-primary-window-minutes", "300")
	headers.Set("x-codex-primary-reset-after-seconds", "120")
	svc.observeCodexCreditHeaders(context.Background(), a, headers, true)
	select {
	case <-repo.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("write did not start")
	}
	headers.Set("x-codex-credits-has-credits", "false")
	svc.observeCodexCreditHeaders(context.Background(), a, headers, true)
	close(repo.release)
	require.Eventually(t, func() bool {
		svc.codexCredits.mu.Lock()
		defer svc.codexCredits.mu.Unlock()
		return !svc.codexCredits.entries[a.ID].writing
	}, 2*time.Second, time.Millisecond)
	repo.mu.Lock()
	require.Len(t, repo.updates, 2, "exhaustion during a write must be persisted after the older positive snapshot")
	last := repo.updates[1]
	require.Equal(t, 100.0, repo.updates[0]["codex_5h_used_percent"], "passive quota must populate the existing UI snapshot too")
	encoded, err := json.Marshal(last[codexCreditStateKey])
	repo.mu.Unlock()
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"has_credits":false`)
	require.Contains(t, string(encoded), `"blocked_at":`)
	require.NotContains(t, last, openaiQuotaResetCreditsKey)
	for i := 0; i < 100; i++ {
		svc.observeCodexCreditHeaders(context.Background(), a, headers, true)
	}
	repo.mu.Lock()
	require.Len(t, repo.updates, 2, "steady state writes are throttled")
	repo.mu.Unlock()
	// No account repository or HTTP client is required for passive admission.
	bounded := &OpenAIGatewayService{}
	for id := int64(1); id <= codexCreditCapacity+20; id++ {
		row := creditTestAccount()
		row.ID = id
		bounded.storeCodexCreditObservation(row, &OpenAICredits{HasCredits: true}, true, nil)
	}
	require.Len(t, bounded.codexCredits.entries, codexCreditCapacity)
}

func TestCodexCreditAdminValidation(t *testing.T) {
	for _, tc := range []struct {
		platform, kind string
		shadow         bool
		value          any
	}{
		{PlatformOpenAI, AccountTypeAPIKey, false, true},
		{PlatformOpenAI, AccountTypeOAuth, true, true},
		{PlatformOpenAI, AccountTypeOAuth, false, "true"},
		{PlatformAnthropic, AccountTypeOAuth, false, true},
	} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			_, err := normalizeCodexCreditPolicyExtra(tc.platform, tc.kind, tc.shadow, map[string]any{CodexAllowCreditUsageKey: tc.value}, nil)
			require.Error(t, err)
		})
	}
	extra, err := normalizeCodexCreditPolicyExtra(PlatformOpenAI, AccountTypeOAuth, false, map[string]any{CodexAllowCreditUsageKey: true, codexCreditStateKey: "forged", codexCreditResumeKey: 999}, nil)
	require.NoError(t, err)
	require.NotContains(t, extra, codexCreditStateKey)
	require.NotContains(t, extra, codexCreditResumeKey)
}

func TestCodexCreditOAuthNativeAbsoluteResetAndSparkQuotaShapes(t *testing.T) {
	resetAt := time.Now().Add(time.Hour).Unix()
	window := fmt.Sprintf(`{"primary":{"used_percent":100,"window_minutes":300,"reset_at":%d}}`, resetAt)
	for _, feature := range []string{
		`"metered_limit_name":"codex_bengalfox","rate_limits":` + window,
		`"additional_rate_limits":{"GPT-5.3-Codex-Spark":{"rate_limit":` + window + `}}`,
		`"additional_rate_limits":{"GPT-5.3-Codex-Spark":` + window + `}`,
		`"additional_rate_limits":[{"metered_feature":"codex_bengalfox","rate_limit":` + window + `}]`,
	} {
		parentID := int64(101)
		shadow := &Account{ID: 102, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parentID, QuotaDimension: QuotaDimensionSpark}
		svc := &OpenAIGatewayService{}
		svc.observeCodexCreditEvent(context.Background(), shadow, []byte(`{"type":"codex.rate_limits",`+feature+`}`))
		state := svc.codexCreditState(shadow)
		require.Equal(t, 100.0, readOpenAIQuotaUsedPercent(state.Quota, "5h"))
		reset, ok := openAICodexWindowResetAt(state.Quota, "5h")
		require.True(t, ok)
		require.InDelta(t, resetAt, reset.Unix(), 1)
		require.Error(t, svc.CheckOpenAICreditPolicy(context.Background(), shadow))
	}
	// A reset_at in the past clears known exhaustion without a new active probe.
	svc := &OpenAIGatewayService{}
	a := creditTestAccount()
	svc.observeCodexCreditEvent(context.Background(), a, []byte(fmt.Sprintf(`{"type":"codex.rate_limits","rate_limits":{"primary":{"used_percent":100,"window_minutes":300,"reset_at":%d}}}`, time.Now().Add(-time.Second).Unix())))
	require.NoError(t, svc.CheckOpenAICreditPolicy(context.Background(), a))
}

func TestCodexCreditOAuthNamespacedHeadersAndErrorFrames(t *testing.T) {
	parent := creditTestAccount()
	shadow := &Account{ID: 102, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent.ID, QuotaDimension: QuotaDimensionSpark}
	svc := &OpenAIGatewayService{}
	headers := make(http.Header)
	for prefix, percent := range map[string]string{"x-codex-": "100", "x-codex-bengalfox-": "20"} {
		headers.Set(prefix+"primary-used-percent", percent)
		headers.Set(prefix+"primary-window-minutes", "300")
		headers.Set(prefix+"primary-reset-after-seconds", "120")
	}
	svc.observeCodexCreditHeaders(context.Background(), parent, headers, true)
	svc.observeCodexCreditHeaders(context.Background(), shadow, headers, false)
	require.Error(t, svc.CheckOpenAICreditPolicy(context.Background(), parent))
	require.NoError(t, svc.CheckOpenAICreditPolicy(context.Background(), shadow), "Spark must not inherit the parent's 100% window")
	require.Equal(t, 20.0, readOpenAIQuotaUsedPercent(svc.codexCreditState(shadow).Quota, "5h"))
	svc.observeCodexCreditEvent(context.Background(), parent, []byte(`{"type":"error","status":429,"headers":{"X-Codex-Credits-Has-Credits":["false"],"x-codex-credits-unlimited":false,"x-codex-credits-balance":"0"}}`))
	state := svc.codexCreditState(parent)
	require.NotNil(t, state.Credits)
	require.False(t, state.Credits.HasCredits)
	require.Positive(t, state.BlockedAt)
}

func TestCodexCreditNonStreamingSSEAndUnavailableParent(t *testing.T) {
	parent := creditTestAccount()
	svc := &OpenAIGatewayService{}
	svc.observeCodexCreditSSEBody(context.Background(), parent, []byte("data: {\"type\":\"codex.rate_limits\",\"credits\":{\"has_credits\":true,\"unlimited\":false}}\n\ndata: [DONE]\n"))
	require.True(t, svc.codexCreditState(parent).Credits.HasCredits)
	shadow := &Account{ID: 102, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent.ID, QuotaDimension: QuotaDimensionSpark}
	missing := &OpenAIGatewayService{schedulerSnapshot: NewSchedulerSnapshotService(&snapshotHydrationCache{accounts: map[int64]*Account{parent.ID: parent}}, nil, nil, nil, nil)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	missing.observeCodexCreditEvent(ctx, shadow, []byte(`{"type":"codex.rate_limits","credits":{"has_credits":true,"unlimited":false}}`))
	require.NotContains(t, missing.codexCredits.entries, parent.ID, "a failed parent lookup must not manufacture and persist an empty owner state")
}

type creditSlowSnapshotCache struct{ SchedulerCache }

func (creditSlowSnapshotCache) GetAccount(ctx context.Context, _ int64) (*Account, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestCodexCreditSnapshotReadsHaveTheirOwnDeadline(t *testing.T) {
	svc := &OpenAIGatewayService{schedulerSnapshot: NewSchedulerSnapshotService(creditSlowSnapshotCache{}, nil, nil, nil, nil)}
	start := time.Now()
	err := svc.CheckOpenAICreditPolicy(context.Background(), creditTestAccount())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), time.Second, "credit admission must not wait for a WS-wide timeout")
}
