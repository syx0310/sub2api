package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRuntimeBlockRecoveryRequiresFreshPersistedSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name         string
		freshBlock   bool
		updatedDelta time.Duration
		missingStamp bool
		disabled     bool
		wantBlocked  bool
	}{
		{name: "fresh local protection", freshBlock: true, updatedDelta: time.Second, wantBlocked: true},
		{name: "failed write or missing snapshot timestamp", missingStamp: true, wantBlocked: true},
		{name: "lagging cache", updatedDelta: -time.Second, wantBlocked: true},
		{name: "unchanged persisted snapshot", wantBlocked: true},
		{name: "confirmed newer recovery", updatedDelta: time.Second},
		{name: "newer but disabled account", updatedDelta: time.Second, disabled: true, wantBlocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &OpenAIGatewayService{}
			account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: !tc.disabled}
			svc.BlockAccountScheduling(account, time.Now().Add(time.Minute), "transport_error")
			created := time.Now()
			if !tc.freshBlock {
				created = created.Add(-2 * openAIAccountStateUpdateTimeout)
			}
			svc.openaiAccountRuntimeBlockCreatedAt.Store(account.ID, created)
			if !tc.missingStamp {
				account.UpdatedAt = created.Add(tc.updatedDelta)
			}
			require.Equal(t, tc.wantBlocked, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.6-sol"))
			require.Equal(t, tc.wantBlocked, svc.isOpenAIAccountRuntimeBlocked(account))
		})
	}
}

func TestRuntimeBlockRecoveryPreservesNewGenerationWithSameDeadline(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 43, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
	until := time.Now().Add(time.Minute)
	svc.BlockAccountScheduling(account, until, "old block")
	svc.openaiAccountRuntimeBlockCreatedAt.Store(account.ID, time.Now().Add(-2*openAIAccountStateUpdateTimeout))
	snapshot := svc.peekOpenAIAccountRuntimeBlock(account)
	svc.BlockAccountScheduling(account, until, "new failure")
	svc.clearOpenAIAccountRuntimeBlockIfUnchanged(account.ID, snapshot)
	account.UpdatedAt = time.Now()
	require.True(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.6-sol"))
}

func TestGrokRuntimeRollbackRestoresRecoveryTimestamp(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 44, Platform: PlatformGrok, Type: AccountTypeOAuth}
	svc.BlockAccountScheduling(account, time.Now().Add(time.Minute), "original")
	createdAt := time.Now().Add(-time.Minute)
	svc.openaiAccountRuntimeBlockCreatedAt.Store(account.ID, createdAt)
	rollback := svc.blockGrokCredentialRuntime(account, time.Now().Add(2*time.Minute), "credential mutation")
	rollback()
	got, ok := svc.openaiAccountRuntimeBlockCreatedAt.Load(account.ID)
	require.True(t, ok)
	require.Equal(t, createdAt, got)
	svc.ClearAccountSchedulingBlock(account.ID)
	_, ok = svc.openaiAccountRuntimeBlockCreatedAt.Load(account.ID)
	require.False(t, ok)
}
