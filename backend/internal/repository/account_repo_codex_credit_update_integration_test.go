//go:build integration

package repository

import (
	"context"
	"maps"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Pause at the real admin read/save boundary, without replacing persistence.
// The observer commits through a separate repository before the edit locks and
// saves its stale Account. This is deterministic, with no timing sleeps.
type creditInterleavedAdminRepo struct {
	*accountRepository
	beforeSave func(context.Context, *service.Account) error
}

func (r *creditInterleavedAdminRepo) UpdateWithAccountBillingSettings(ctx context.Context, account *service.Account, probe, sync *bool, rate *float64, credit *bool) error {
	if before := r.beforeSave; before != nil {
		r.beforeSave = nil
		if err := before(ctx, account); err != nil {
			return err
		}
	}
	return r.accountRepository.UpdateWithAccountBillingSettings(ctx, account, probe, sync, rate, credit)
}

func TestAdminAccountEditPreservesConcurrentCodexCreditExhaustion(t *testing.T) {
	for _, fullForm := range []bool{false, true} {
		name := "name only"
		if fullForm {
			name = "stale full form with unchanged permission"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			client := testEntClient(t)
			cache := &schedulerCacheRecorder{}
			repo := newAccountRepositoryWithSQL(client, integrationDB, cache)
			observer := newAccountRepositoryWithSQL(client, integrationDB, nil)
			account := mustCreateAccount(t, client, &service.Account{
				Name: "credit-edit-race", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
				Credentials: map[string]any{"access_token": "test-only"},
				Extra: map[string]any{
					service.CodexAllowCreditUsageKey: true,
					"codex_5h_used_percent":          100.0, "codex_5h_reset_at": time.Now().Add(time.Hour).Format(time.RFC3339),
				},
			})
			t.Cleanup(func() {
				_, err := integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
				require.NoError(t, err)
				_, err = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", account.ID)
				require.NoError(t, err)
			})
			quota := service.NewOpenAIQuotaService(observer, nil, nil, nil, nil)
			wrapped := &creditInterleavedAdminRepo{accountRepository: repo, beforeSave: func(ctx context.Context, stale *service.Account) error {
				require.NotContains(t, stale.Extra, "codex_credit_policy_state")
				// A zero followed by positive evidence must still require manual
				// reauthorization, even in a new gateway instance after this edit.
				if err := quota.CacheCreditsSnapshot(ctx, account.ID, &service.OpenAIQuotaUsage{FetchedAt: time.Now().Unix(), Credits: &service.OpenAICredits{}}); err != nil {
					return err
				}
				return quota.CacheCreditsSnapshot(ctx, account.ID, &service.OpenAIQuotaUsage{FetchedAt: time.Now().Unix(), Credits: &service.OpenAICredits{HasCredits: true}})
			}}
			admin := service.NewAdminService(nil, nil, nil, wrapped, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			input := &service.UpdateAccountInput{Name: "edited name"}
			if fullForm {
				input.Extra = maps.Clone(account.Extra)
				input.Extra["codex_credits_snapshot"] = map[string]any{"credits": nil, "fetched_at": 1}
			}
			updated, err := admin.UpdateAccount(ctx, account.ID, input)
			require.NoError(t, err)
			require.Equal(t, "edited name", updated.Name)
			state := updated.Extra["codex_credit_policy_state"].(map[string]any)
			require.Positive(t, state["blocked_at"])
			require.Equal(t, true, state["credits"].(map[string]any)["has_credits"])
			require.NotContains(t, updated.Extra, "codex_credit_usage_resume_at", "unchanged permission is not reauthorization")
			require.Equal(t, updated.Extra["codex_credit_policy_state"], cache.accounts[account.ID].Extra["codex_credit_policy_state"])
			fresh, err := observer.GetByID(ctx, account.ID)
			require.NoError(t, err)
			gateway := &service.OpenAIGatewayService{}
			require.Equal(t, "codex_credit_usage_manual_resume_required", infraerrors.Reason(gateway.CheckOpenAICreditPolicy(ctx, fresh)))

			// Explicit off->on still works and is based on current locked state.
			extra := maps.Clone(updated.Extra)
			extra[service.CodexAllowCreditUsageKey] = false
			updated, err = admin.UpdateAccount(ctx, account.ID, &service.UpdateAccountInput{Extra: extra})
			require.NoError(t, err)
			extra = maps.Clone(updated.Extra)
			extra[service.CodexAllowCreditUsageKey] = true
			updated, err = admin.UpdateAccount(ctx, account.ID, &service.UpdateAccountInput{Extra: extra})
			require.NoError(t, err)
			require.Equal(t, state, updated.Extra["codex_credit_policy_state"], "reauthorization does not erase the latch")
			require.NoError(t, (&service.OpenAIGatewayService{}).CheckOpenAICreditPolicy(ctx, updated))
		})
	}
}

func TestAccountUpdatePreservesLockedCodexCreditFields(t *testing.T) {
	ctx := t.Context()
	tx := testEntTx(t)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	account := mustCreateAccount(t, tx.Client(), &service.Account{
		Name: "credit-generic-edit", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Extra: map[string]any{service.CodexAllowCreditUsageKey: true},
	})
	stale, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	current := map[string]any{
		service.CodexAllowCreditUsageKey: false,
		"codex_credit_policy_state":      map[string]any{"blocked_at": 2000, "credits_at": 2000, "credits": nil, "future_field": "keep"},
		"codex_credit_usage_resume_at":   1000,
		"codex_credits_snapshot":         map[string]any{"fetched_at": 2, "credits": nil},
	}
	require.NoError(t, repo.UpdateExtra(ctx, account.ID, current))
	stale.Name = "new name"
	stale.Extra["codex_credit_usage_resume_at"] = 9000 // Generic update cannot grant permission.
	require.NoError(t, repo.Update(ctx, stale))
	got, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, "new name", got.Name)
	require.Equal(t, false, got.Extra[service.CodexAllowCreditUsageKey])
	require.EqualValues(t, 1000, got.Extra["codex_credit_usage_resume_at"])
	require.EqualValues(t, 2000, got.Extra["codex_credit_policy_state"].(map[string]any)["blocked_at"])
	require.Equal(t, "keep", got.Extra["codex_credit_policy_state"].(map[string]any)["future_field"])
}
