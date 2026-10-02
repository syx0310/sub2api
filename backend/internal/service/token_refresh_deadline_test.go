package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type unsignaledRefreshDeadline struct {
	context.Context
	deadline time.Time
}

func (c unsignaledRefreshDeadline) Deadline() (time.Time, bool) { return c.deadline, true }

func TestRefreshAttemptDeadlineDoesNotWaitForTimerCallback(t *testing.T) {
	now := time.Now()
	parent := context.Background()
	attempt := unsignaledRefreshDeadline{Context: parent, deadline: now}
	require.NoError(t, attempt.Err())
	require.True(t, refreshAttemptExceededDeadline(parent, attempt, now))
	require.True(t, refreshAttemptExceededDeadline(parent, attempt, now.Add(time.Second)))
	require.False(t, refreshAttemptExceededDeadline(parent, attempt, now.Add(-time.Second)))
	require.False(t, refreshAttemptExceededDeadline(parent, parent, now))
	canceled, cancel := context.WithCancel(parent)
	cancel()
	require.False(t, refreshAttemptExceededDeadline(canceled, attempt, now))
}
