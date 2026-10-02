//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type creditSchedulerSettings struct{ service.SettingRepository }

func (creditSchedulerSettings) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{"openai_advanced_scheduler_enabled": "true"}, nil
}

func (creditSchedulerSettings) GetValue(context.Context, string) (string, error) { return "", nil }

type creditSchedulerCache struct {
	service.SchedulerCache
	fullReads int
}

func (c *creditSchedulerCache) GetAccount(ctx context.Context, id int64) (*service.Account, error) {
	c.fullReads++
	return c.SchedulerCache.GetAccount(ctx, id)
}

// Exercise serialization, actual Redis candidate reads and the production
// scheduler together. A passing full-Account credit check cannot cover this bug.
func TestSchedulerCacheCodexCreditCandidateAdmission(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	cache := newSchedulerCacheUnit(t)
	counted := &creditSchedulerCache{SchedulerCache: cache}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	snapshot := service.NewSchedulerSnapshotService(counted, nil, nil, nil, cfg)
	limiter := service.NewRateLimitService(nil, nil, cfg, nil, nil)
	limiter.SetSettingService(service.NewSettingService(creditSchedulerSettings{}, cfg))
	gw := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, cfg, snapshot, nil, nil, limiter, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	bucket := service.SchedulerBucket{Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
	token, err := cache.CaptureBucketWriteToken(ctx, bucket)
	require.NoError(t, err)
	account := service.Account{
		ID: 91, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Status: service.StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-only", "plan_type": "pro"},
	}
	require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{account}))
	for _, tc := range []struct {
		name                      string
		allow, displayOnly, stale bool
		blocked, resume           int64
		wantReason                string
	}{
		{name: "enabled with passive balance", allow: true},
		{name: "enabled with manual display balance", allow: true, displayOnly: true},
		{name: "disabled", wantReason: "codex_credit_usage_disabled"},
		{name: "stale balance", allow: true, stale: true, wantReason: "codex_credit_usage_unknown_or_empty"},
		{name: "exhaustion latch", allow: true, blocked: now.UnixMilli(), wantReason: "codex_credit_usage_manual_resume_required"},
		{name: "manually reauthorized", allow: true, blocked: now.UnixMilli(), resume: now.UnixMilli()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at := now
			if tc.stale {
				at = now.Add(-10 * time.Minute)
			}
			credits := map[string]any{"has_credits": true, "unlimited": false, "balance": "12.50"}
			account.Extra = map[string]any{
				service.CodexAllowCreditUsageKey: tc.allow,
				"codex_credit_usage_resume_at":   tc.resume,
				"codex_5h_used_percent":          100.0, "codex_5h_reset_at": now.Add(time.Hour).Format(time.RFC3339),
				"codex_usage_updated_at": now.Format(time.RFC3339), "unrelated_large_payload": "drop me",
			}
			if tc.displayOnly {
				account.Extra["codex_credits_snapshot"] = map[string]any{"credits": credits, "fetched_at": at.Unix()}
			} else {
				account.Extra["codex_credit_policy_state"] = map[string]any{"credits": credits, "credits_at": at.UnixMilli(), "blocked_at": tc.blocked}
			}
			// Account updates refresh the projection without rebuilding membership.
			require.NoError(t, cache.SetAccount(ctx, &account))
			candidates, hit, err := cache.GetSnapshot(ctx, bucket)
			require.NoError(t, err)
			require.True(t, hit)
			require.Len(t, candidates, 1)
			require.NotContains(t, candidates[0].Credentials, "access_token")
			require.NotContains(t, candidates[0].Extra, "unrelated_large_payload")
			counted.fullReads = 0
			selection, decision, err := gw.SelectAccountWithScheduler(ctx, nil, "", "", "gpt-5.1", nil, service.OpenAIUpstreamTransportAny, false)
			if tc.wantReason != "" {
				require.ErrorIs(t, err, service.ErrNoAvailableAccounts)
				require.ErrorContains(t, err, tc.wantReason)
				require.Nil(t, selection)
				require.Zero(t, counted.fullReads, "candidate rejection happens before hydration")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.Equal(t, account.ID, selection.Account.ID)
			require.Equal(t, 1, decision.CandidateCount, "must exercise the advanced candidate filter")
			require.Positive(t, counted.fullReads)
			selection.ReleaseFunc()
		})
	}
}
