package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQueryUsageCodexCredits(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"decimal balance", `{"credits":{"has_credits":true,"unlimited":false,"balance":"12345678901234567890.0123"}}`, `{"has_credits":true,"unlimited":false,"balance":"12345678901234567890.0123"}`},
		{"zero", `{"credits":{"has_credits":false,"unlimited":false,"balance":"0"}}`, `{"has_credits":false,"unlimited":false,"balance":"0"}`},
		{"unlimited", `{"credits":{"has_credits":false,"unlimited":true,"balance":null}}`, `{"has_credits":false,"unlimited":true,"balance":null}`},
		{"hidden balance", `{"credits":{"has_credits":true,"unlimited":false}}`, `{"has_credits":true,"unlimited":false,"balance":null}`},
		{"absent", `{}`, `null`},
		{"null", `{"credits":null}`, `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{ID: 100, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
				Credentials: map[string]any{"chatgpt_account_id": "test-workspace"}}
			repo := &stubQuotaAccountRepo{accounts: map[int64]*Account{100: account}}
			tokens := &stubQuotaTokenCache{tokens: map[string]string{OpenAITokenCacheKey(account): "test-token"}}
			var paths []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
				require.Equal(t, "test-workspace", r.Header.Get("ChatGPT-Account-ID"))
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/backend-api/wham/usage" {
					_, _ = w.Write([]byte(tc.body))
					return
				}
				// Reset-card details may be unavailable without losing points.
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
			}))
			defer srv.Close()
			svc := NewOpenAIQuotaService(repo, nil, NewOpenAITokenProvider(repo, tokens, nil), newQuotaRedirectingFactory(srv), nil)
			usage, err := svc.QueryUsage(context.Background(), 100)
			require.NoError(t, err)
			require.Positive(t, usage.FetchedAt)
			encoded, err := json.Marshal(usage.Credits)
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(encoded))
			require.Equal(t, []string{"/backend-api/wham/usage", "/backend-api/wham/rate-limit-reset-credits"}, paths)
			require.Zero(t, repo.extraUpdateCalls, "read-only query must not write account state")
		})
	}
}

func TestCacheCodexCreditsSnapshot(t *testing.T) {
	ctx := context.Background()
	parent := &Account{ID: 100, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	shadow := &Account{ID: 200, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent.ID}
	repo := &stubQuotaAccountRepo{accounts: map[int64]*Account{100: parent, 200: shadow}}
	svc := &OpenAIQuotaService{accountRepo: repo}
	balance := "1200.50"
	usage := &OpenAIQuotaUsage{FetchedAt: 123, Credits: &OpenAICredits{HasCredits: true, Balance: &balance}}

	// No reset-card details are required to persist the balance on the queried row.
	require.NoError(t, svc.CacheCreditsSnapshot(ctx, 200, usage))
	encoded, err := json.Marshal(repo.extraUpdates[200][openaiQuotaCreditsKey])
	require.NoError(t, err)
	require.JSONEq(t, `{"credits":{"has_credits":true,"unlimited":false,"balance":"1200.50"},"fetched_at":123}`, string(encoded))
	require.Contains(t, repo.extraUpdates[100], codexCreditStateKey, "credit policy belongs to the credential owner")
	require.NotContains(t, repo.extraUpdates[100], openaiQuotaCreditsKey, "the per-row display stays on the queried row")
	require.NotContains(t, repo.extraUpdates[100], CodexAllowCreditUsageKey, "refresh does not grant permission")
	require.NotContains(t, repo.extraUpdates[200], openaiQuotaResetCreditsKey)

	// Missing credits from a successful read invalidate the previous balance.
	require.NoError(t, svc.CacheCreditsSnapshot(ctx, 200, &OpenAIQuotaUsage{FetchedAt: 456}))
	encoded, err = json.Marshal(repo.extraUpdates[200][openaiQuotaCreditsKey])
	require.NoError(t, err)
	require.JSONEq(t, `{"credits":null,"fetched_at":456}`, string(encoded))

	require.Error(t, svc.CacheCreditsSnapshot(ctx, 200, nil))
	require.Equal(t, 4, repo.extraUpdateCalls)
	repo.extraUpdateErr = errors.New("database unavailable")
	require.ErrorContains(t, svc.CacheCreditsSnapshot(ctx, 200, usage), "database unavailable")
}

func TestCacheCodexCreditsSnapshotLatchesExhaustionUntilManualReauthorization(t *testing.T) {
	for _, shadowRefresh := range []bool{false, true} {
		t.Run(fmt.Sprintf("shadow=%t", shadowRefresh), func(t *testing.T) {
			now := time.Now()
			parent := &Account{ID: 100, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
				CodexAllowCreditUsageKey: true, "codex_5h_used_percent": 100.0,
				"codex_5h_reset_at": now.Add(time.Hour).Format(time.RFC3339),
			}}
			shadow := &Account{ID: 200, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent.ID, Extra: map[string]any{}}
			repo := &stubQuotaAccountRepo{accounts: map[int64]*Account{100: parent, 200: shadow}}
			svc := &OpenAIQuotaService{accountRepo: repo}
			id := parent.ID
			if shadowRefresh {
				id = shadow.ID
			}
			refresh := func(credits *OpenAICredits) {
				t.Helper()
				require.NoError(t, svc.CacheCreditsSnapshot(t.Context(), id, &OpenAIQuotaUsage{FetchedAt: now.Unix(), Credits: credits}))
				// This recording stub does not mutate stored accounts automatically.
				for updatedID, updates := range repo.extraUpdates {
					maps.Copy(repo.accounts[updatedID].Extra, updates)
				}
			}
			refresh(&OpenAICredits{HasCredits: true})
			gw := &OpenAIGatewayService{}
			require.NoError(t, gw.CheckOpenAICreditPolicy(t.Context(), parent))
			refresh(&OpenAICredits{})
			blocked := codexCreditStateFromExtra(parent.Extra).BlockedAt
			require.Positive(t, blocked)
			require.Error(t, gw.CheckOpenAICreditPolicy(t.Context(), parent))
			refresh(&OpenAICredits{HasCredits: true})
			require.Equal(t, blocked, codexCreditStateFromExtra(parent.Extra).BlockedAt)
			require.Error(t, gw.CheckOpenAICreditPolicy(t.Context(), parent), "manual positive balance cannot resume credit spending")
			gw.storeCodexCreditObservation(parent, &OpenAICredits{HasCredits: true}, true, nil)
			require.Error(t, gw.CheckOpenAICreditPolicy(t.Context(), parent), "passive positive balance cannot resume it either")
			parent.Extra[CodexAllowCreditUsageKey] = false
			extra, err := normalizeCodexCreditPolicyExtra(parent.Platform, parent.Type, false, maps.Clone(parent.Extra), parent)
			require.NoError(t, err)
			extra[CodexAllowCreditUsageKey] = true
			extra, err = normalizeCodexCreditPolicyExtra(parent.Platform, parent.Type, false, extra, parent)
			require.NoError(t, err)
			parent.Extra = extra
			require.NoError(t, gw.CheckOpenAICreditPolicy(t.Context(), parent))
			require.NotContains(t, shadow.Extra, codexCreditStateKey)
		})
	}
}

func TestCacheCodexCreditsSnapshotUnknownDoesNotLatchExhaustion(t *testing.T) {
	account := &Account{ID: 100, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
		codexCreditStateKey: codexCreditState{Credits: &OpenAICredits{HasCredits: true}, CreditsAt: time.Now().Add(-time.Minute).UnixMilli()},
	}}
	repo := &stubQuotaAccountRepo{accounts: map[int64]*Account{100: account}}
	svc := &OpenAIQuotaService{accountRepo: repo}
	require.NoError(t, svc.CacheCreditsSnapshot(t.Context(), 100, &OpenAIQuotaUsage{FetchedAt: time.Now().Unix()}))
	state := codexCreditStateFromExtra(repo.extraUpdates[100])
	require.Nil(t, state.Credits)
	require.Zero(t, state.BlockedAt)
}
