package service

import (
	"context"
	"errors"
	"sync"
	"time"
)

// openAIWSClientDrain keeps an admitted upstream read alive after its HTTP
// consumer disconnects. Canceling a coder/websocket Read closes the socket, so
// detaching only after that Read fails cannot recover terminal usage.
type openAIWSClientDrain struct {
	readCtx context.Context
	cancel  context.CancelCauseFunc
	budget  time.Duration

	mu           sync.Mutex
	timer        *time.Timer
	closed       bool
	stopWatching func() bool
}

func newOpenAIWSClientDrain(clientCtx context.Context, budget time.Duration) *openAIWSClientDrain {
	if clientCtx == nil {
		clientCtx = context.Background()
	}
	readCtx, cancel := context.WithCancelCause(context.WithoutCancel(clientCtx))
	drain := &openAIWSClientDrain{readCtx: readCtx, cancel: cancel, budget: budget}
	drain.stopWatching = context.AfterFunc(clientCtx, func() {
		if errors.Is(clientCtx.Err(), context.Canceled) {
			drain.begin()
		} else {
			// An explicit operation deadline is not a user cancellation and must
			// not be extended by the accounting drain.
			cancel(clientCtx.Err())
		}
	})
	return drain
}

func (d *openAIWSClientDrain) begin() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.timer != nil {
		return
	}
	d.timer = time.AfterFunc(d.budget, func() { d.cancel(context.Canceled) })
}

func (d *openAIWSClientDrain) close() {
	d.stopWatching()
	d.mu.Lock()
	d.closed = true
	if d.timer != nil {
		d.timer.Stop()
	}
	d.mu.Unlock()
	d.cancel(context.Canceled)
}
