package handler

import (
	"context"
	"errors"
	"sync"
)

// A connection has at most one executing turn. Only that turn owns a handler
// reservation reference; billing tasks retain their own references separately.
// No frame, response history, or map of completed turns is retained here.
type openAIWSInflightTurn struct {
	mu   sync.Mutex
	turn int
	ctx  context.Context
	done func()
}

func (s *openAIWSInflightTurn) begin(turn int, reserve func() (context.Context, func(), error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done != nil {
		if s.turn == turn {
			return nil // Same-account transport retry: no second reservation.
		}
		return errors.New("previous websocket turn reservation is still active")
	}
	ctx, done, err := reserve()
	if err != nil {
		return err
	}
	s.turn, s.ctx, s.done = turn, ctx, done
	return nil
}

// take transfers ownership to AfterTurn so it can submit billing before its
// deferred handler completion releases the reference, including error billing.
func (s *openAIWSInflightTurn) take(turn int, fallback context.Context) (context.Context, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done == nil || s.turn != turn {
		return fallback, inflightNoop
	}
	ctx, done := s.ctx, s.done
	s.ctx, s.done, s.turn = nil, nil, 0
	return ctx, done
}

// close handles pre-write errors, disconnects and account failover paths that
// do not emit AfterTurn. Async billing references remain valid after this call.
func (s *openAIWSInflightTurn) close() {
	s.mu.Lock()
	done := s.done
	s.ctx, s.done, s.turn = nil, nil, 0
	s.mu.Unlock()
	if done != nil {
		done()
	}
}
