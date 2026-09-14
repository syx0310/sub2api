package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type bareErrorAbortStub struct{ aborted bool }

func (s *bareErrorAbortStub) Close() error    { return errors.New("must not wait for close handshake") }
func (s *bareErrorAbortStub) CloseNow() error { s.aborted = true; return nil }

func TestWSBareErrorAbortsWithoutCloseHandshake(t *testing.T) {
	conn := &bareErrorAbortStub{}
	require.NoError(t, abortOpenAIWSBareErrorConn(conn))
	require.True(t, conn.aborted)
}

func TestWSBareErrorDeadlineNeverRenews(t *testing.T) {
	drain := &openAIWSBareErrorDrain{}
	drain.observe([]byte(`{"type":"response.created","response":{"id":"resp_one"}}`))
	drain.observe([]byte(`{"type":"error","error":{"message":"rejected"}}`))
	first := drain.state.Load()
	for _, payload := range []string{
		`{"type":"rate_limits.updated"}`, `{"type":"error","error":{"message":"again"}}`,
		`{"type":"response.output_item.done","item":{"type":"message"}}`,
	} {
		require.True(t, drain.acceptTrailing([]byte(payload)))
		drain.observe([]byte(payload))
		require.Same(t, first, drain.state.Load())
	}
	require.False(t, drain.acceptTrailing([]byte(`{"type":"response.created","response":{"id":"resp_two"}}`)))
	require.False(t, drain.acceptTrailing([]byte(`{"type":"response.completed","response":{"id":"resp_one"}}`)))
	require.False(t, drain.acceptTrailing([]byte(`{"type":"response.failed","response":{"id":"resp_two"}}`)))
	require.True(t, drain.acceptTrailing([]byte(`{"type":"response.failed","response":{"id":"resp_one"}}`)))
	drain.observe([]byte(`{"type":"response.failed","response":{"id":"resp_one"}}`))
	require.True(t, drain.finished)
	require.False(t, drain.acceptTrailing([]byte(`{"type":"rate_limits.updated"}`)))
}

func TestWSBareErrorDeadlineIncludesWritesAndHonorsCancellation(t *testing.T) {
	drain := &openAIWSBareErrorDrain{}
	ctx := context.Background()
	unchanged, cancel := drain.deadlineContext(ctx)
	require.Equal(t, ctx, unchanged)
	cancel()
	drain.observe([]byte(`{"type":"error"}`))
	bounded, cancel := drain.deadlineContext(ctx)
	defer cancel()
	deadline, ok := bounded.Deadline()
	require.True(t, ok)
	require.Equal(t, drain.state.Load().deadline, deadline)
	parent, parentCancel := context.WithCancel(ctx)
	child, childCancel := drain.deadlineContext(parent)
	defer childCancel()
	parentCancel()
	require.ErrorIs(t, child.Err(), context.Canceled)
	drain.state.Store(&openAIWSBareErrorDeadline{deadline: time.Now().Add(-time.Second)})
	require.False(t, drain.acceptTrailing([]byte(`{"type":"error"}`)))
}
