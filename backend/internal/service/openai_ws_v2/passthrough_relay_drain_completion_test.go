package openai_ws_v2

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestRelay_ClientCloseDuringTerminalSettlement(t *testing.T) {
	for _, completeBeforeWait := range []bool{false, true} {
		name := "completion_during_drain"
		if completeBeforeWait {
			name = "completion_before_drain_wait"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			client := newPassthroughTestFrameConn(nil, false)
			upstream := newPassthroughTestFrameConn([]passthroughTestFrame{{
				msgType: coderws.MessageText,
				payload: []byte(`{"type":"response.completed","response":{"id":"resp_settled","usage":{"input_tokens":11,"output_tokens":4}}}`),
			}}, false)
			callbackEntered := make(chan struct{})
			allowCallback := make(chan struct{})
			terminalProcessed := make(chan struct{})
			drainStarted := make(chan struct{})
			allowDrainWait := make(chan struct{})
			releaseCallback := sync.OnceFunc(func() { close(allowCallback) })
			releaseDrainWait := sync.OnceFunc(func() { close(allowDrainWait) })
			var result RelayResult
			var exit *RelayExit
			var turns []RelayTurnResult
			done := make(chan struct{})
			t.Cleanup(func() {
				releaseCallback()
				releaseDrainWait()
				cancel()
				_ = upstream.Close()
				_ = client.Close()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Error("relay did not exit during cleanup")
				}
			})
			go func() {
				defer close(done)
				result, exit = Relay(ctx, client, upstream,
					[]byte(`{"type":"response.create","model":"gpt-6-astra","input":[]}`),
					RelayOptions{
						FirstMessageSent:     true,
						UpstreamDrainTimeout: time.Minute,
						OnTurnComplete: func(turn RelayTurnResult) {
							close(callbackEntered)
							<-allowCallback
							turns = append(turns, turn)
						},
						AfterClientWrite: func(coderws.MessageType, []byte, error) {
							close(terminalProcessed)
						},
						OnTrace: func(event RelayTraceEvent) {
							if event.Stage == "drain_start" {
								close(drainStarted)
								if completeBeforeWait {
									<-allowDrainWait
								}
							}
						},
					})
			}()
			select {
			case <-callbackEntered:
			case <-time.After(3 * time.Second):
				t.Fatal("terminal did not reach settlement")
			}
			require.Len(t, client.Writes(), 1, "the client has already received the terminal")
			require.NoError(t, client.Close())
			select {
			case <-drainStarted:
			case <-time.After(3 * time.Second):
				t.Fatal("client close did not enter drain while settlement was pending")
			}
			select {
			case <-done:
				t.Fatal("relay returned before the settlement callback finished")
			default:
			}
			releaseCallback()
			select {
			case <-terminalProcessed:
			case <-time.After(3 * time.Second):
				t.Fatal("terminal processing did not finish")
			}
			releaseDrainWait()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("completed settlement did not wake drain; upstream remains silent")
			}
			require.Nil(t, exit)
			require.Len(t, turns, 1)
			require.Equal(t, "resp_settled", result.RequestID)
			require.Equal(t, Usage{InputTokens: 11, OutputTokens: 4}, result.Usage)
			require.Equal(t, result.Usage, turns[0].Usage)
			require.Equal(t, int64(1), result.ClientToUpstreamFrames, "no replay")
			require.Equal(t, int64(1), result.UpstreamToClientFrames)
		})
	}
}

func TestWaitRelayDrainExit_CompletionDoesNotSkipPendingTurn(t *testing.T) {
	exits := make(chan relayExitSignal, 1)
	completions := make(chan relayExitSignal, 1)
	completed := atomic.Int64{}
	completed.Store(1)
	signal := relayExitSignal{stage: "drain_terminal", graceful: true, wroteDownstream: true, turnCompleted: true}
	completions <- signal // A previous turn filled the bounded notification slot.
	checkedPending := make(chan struct{})
	markChecked := sync.OnceFunc(func() { close(checkedPending) })
	type drainResult struct {
		signal    relayExitSignal
		hasExit   bool
		completed bool
	}
	done := make(chan drainResult, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		sig, hasExit, settled := waitRelayDrainExit(exits, time.Minute, false, nil, completions, func() bool {
			settled := completed.Load() == 2
			if !settled {
				markChecked()
			}
			return settled
		})
		done <- drainResult{sig, hasExit, settled}
	}()
	t.Cleanup(func() {
		exits <- relayExitSignal{stage: "read_upstream"}
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("drain waiter did not exit during cleanup")
		}
	})
	select {
	case <-checkedPending:
	case <-time.After(time.Second):
		t.Fatal("drain did not inspect the buffered notification")
	}
	select {
	case <-done:
		t.Fatal("an earlier completion stopped drain with a request still pending")
	default:
	}
	completed.Store(2)
	completions <- signal
	select {
	case result := <-done:
		require.True(t, result.hasExit)
		require.True(t, result.completed)
		require.Equal(t, signal, result.signal)
	case <-time.After(time.Second):
		t.Fatal("final completion did not wake drain")
	}
}

func TestEmitTurnComplete_WithoutObserverStillSettlesProtocol(t *testing.T) {
	observed := observedUpstreamEvent{
		terminal:        true,
		terminalMatched: true,
		eventType:       "response.completed",
		responseID:      "resp_no_observer",
		requestSequence: 1,
	}
	require.True(t, emitTurnComplete(nil, nil, observed, 0))
	observed.terminalMatched = false
	require.False(t, emitTurnComplete(nil, nil, observed, 0), "unmatched terminals must not settle a request")
}
