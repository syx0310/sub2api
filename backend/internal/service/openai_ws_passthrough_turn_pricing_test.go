package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// startPassthroughHookRecordingServer 与 startPassthroughLifecycleServer 相同，
// 但把一组会记录调用的 hooks 传给 ingress，用于观察透传路径的 turn 回调。
func startPassthroughHookRecordingServer(
	t *testing.T,
	controlCtx context.Context,
	svc *OpenAIGatewayService,
	account *Account,
	hooks *OpenAIWSIngressHooks,
) (*httptest.Server, <-chan error) {
	t.Helper()
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			serverErr <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()

		msgType, firstMessage, err := ReadOpenAIWSClientMessage(
			controlCtx,
			conn,
			3*time.Second,
			coderws.StatusPolicyViolation,
			"missing first response.create message",
		)
		if err != nil {
			serverErr <- err
			return
		}
		if msgType != coderws.MessageText {
			serverErr <- errors.New("first message was not text")
			return
		}

		recorder := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(recorder)
		req := r.Clone(controlCtx)
		req.Header = req.Header.Clone()
		ginCtx.Request = req
		serverErr <- svc.ProxyResponsesWebSocketFromClient(controlCtx, ginCtx, conn, account, "sk-test", firstMessage, hooks)
	}))
	return server, serverErr
}

// TestPassthroughIngressCallsBothTurnHooks 钉死 ws_v2 透传 ingress 与 handler
// 侧 turn 定价的耦合：每轮先执行 BeforeTurn 的利润检查，同时通过 TurnStarted
// 冻结该轮开始时刻，并在 AfterTurn 中提交同一轮用量。
func TestPassthroughIngressCallsBothTurnHooks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	controlCtx, cancelControl := context.WithCancelCause(context.Background())
	defer cancelControl(context.Canceled)

	upstream := newStagedPassthroughConn()
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_pricing","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`)

	var hooksMu sync.Mutex
	beforeTurnCalls := 0
	expectedTurnStartedAt := time.Date(2026, time.August, 17, 9, 59, 59, 0, time.UTC)
	type hookEvent struct {
		name      string
		turn      int
		startedAt time.Time
	}
	var hookEvents []hookEvent
	hooks := &OpenAIWSIngressHooks{
		InitialTurnStartedAt: expectedTurnStartedAt,
		TurnStarted: func(turn int, startedAt time.Time) {
			hooksMu.Lock()
			hookEvents = append(hookEvents, hookEvent{name: "TurnStarted", turn: turn, startedAt: startedAt})
			hooksMu.Unlock()
		},
		BeforeTurn: func(int) error {
			hooksMu.Lock()
			beforeTurnCalls++
			hooksMu.Unlock()
			return nil
		},
		AfterTurn: func(turn int, _ *OpenAIForwardResult, _ error) {
			hooksMu.Lock()
			hookEvents = append(hookEvents, hookEvent{name: "AfterTurn", turn: turn})
			hooksMu.Unlock()
		},
	}

	server, serverErr := startPassthroughHookRecordingServer(
		t,
		controlCtx,
		newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream),
		passthroughLifecycleAccount(),
		hooks,
	)
	defer server.Close()
	clientConn := dialPassthroughLifecycleClient(t, server)
	defer func() { _ = clientConn.CloseNow() }()

	event, err := readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
	require.NoError(t, err)
	require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())

	// 等待连接自然结束（inter-turn idle 超时），确保 AfterTurn 已提交。
	_, _ = readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
	select {
	case <-serverErr:
	case <-time.After(3 * time.Second):
		t.Fatal("passthrough ingress did not exit")
	}

	hooksMu.Lock()
	gotBefore := beforeTurnCalls
	gotEvents := append([]hookEvent(nil), hookEvents...)
	hooksMu.Unlock()

	require.Equal(t, 1, gotBefore, "透传 ingress 应在首轮开始前执行定价与利润检查")
	require.GreaterOrEqual(t, len(gotEvents), 2, "透传 ingress 应报告 TurnStarted 和 AfterTurn")
	require.Equal(t, "TurnStarted", gotEvents[0].name)
	require.Equal(t, expectedTurnStartedAt, gotEvents[0].startedAt, "TurnStarted 必须携带入口冻结的首轮开始时刻")
	require.Equal(t, "AfterTurn", gotEvents[1].name)
	require.Equal(t, gotEvents[0].turn, gotEvents[1].turn, "TurnStarted 后应提交同一 turn 的 AfterTurn")
}

func TestPassthroughIngressFollowUpCallsBeforeTurnAfterBeforeRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	controlCtx, cancelControl := context.WithCancelCause(context.Background())
	defer cancelControl(context.Canceled)

	upstream := newStagedPassthroughConn()
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_pricing_1","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`)

	var hooksMu sync.Mutex
	var callbacks []string
	afterTurnCalls := 0
	completedTurns := make(chan int, 2)
	hooks := &OpenAIWSIngressHooks{
		BeforeRequest: func(int, []byte, string) error {
			hooksMu.Lock()
			callbacks = append(callbacks, "before_request")
			hooksMu.Unlock()
			return nil
		},
		BeforeTurn: func(int) error {
			hooksMu.Lock()
			callbacks = append(callbacks, "before_turn")
			hooksMu.Unlock()
			return nil
		},
		AfterTurn: func(turn int, _ *OpenAIForwardResult, _ error) {
			hooksMu.Lock()
			afterTurnCalls++
			hooksMu.Unlock()
			completedTurns <- turn
		},
	}

	server, _ := startPassthroughHookRecordingServer(
		t,
		controlCtx,
		newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream),
		passthroughLifecycleAccount(),
		hooks,
	)
	defer server.Close()
	clientConn := dialPassthroughLifecycleClient(t, server)
	defer func() { _ = clientConn.CloseNow() }()
	require.Equal(t, "response.create", gjson.GetBytes(requirePassthroughUpstreamWrite(t, upstream, time.Second), "type").String())

	event, err := readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
	require.NoError(t, err)
	require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1"}`))
	cancelWrite()
	require.NoError(t, err)
	require.Equal(t, "response.create", gjson.GetBytes(requirePassthroughUpstreamWrite(t, upstream, time.Second), "type").String())
	upstream.Send(`{"type":"response.created","response":{"id":"resp_pricing_2","model":"gpt-5.1"}}`)
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_pricing_2","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`)
	event, err = readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
	require.NoError(t, err)
	require.Equal(t, "response.created", gjson.GetBytes(event, "type").String())
	event, err = readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
	require.NoError(t, err)
	require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())

	// Delivery precedes accounting; wait for both callbacks before asserting.
	for _, expectedTurn := range []int{1, 2} {
		select {
		case turn := <-completedTurns:
			require.Equal(t, expectedTurn, turn)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for turn accounting")
		}
	}
	hooksMu.Lock()
	gotCallbacks := append([]string(nil), callbacks...)
	gotAfter := afterTurnCalls
	hooksMu.Unlock()

	require.Equal(t, []string{"before_turn", "before_request", "before_turn"}, gotCallbacks)
	require.Equal(t, 2, gotAfter)
}

func TestPassthroughIngressBeforeTurnRejectionDoesNotForwardFollowUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	controlCtx, cancelControl := context.WithCancelCause(context.Background())
	defer cancelControl(context.Canceled)

	upstream := newStagedPassthroughConn()
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_reject_1","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`)
	rejection := errors.New("turn rejected")
	var hooksMu sync.Mutex
	afterTurnCalls := 0
	var finalErr error
	beforeTurnTurn := 0
	hooks := &OpenAIWSIngressHooks{
		BeforeTurn: func(turn int) error {
			hooksMu.Lock()
			beforeTurnTurn = turn
			hooksMu.Unlock()
			if turn == 1 {
				return nil
			}
			return rejection
		},
		AfterTurn: func(_ int, _ *OpenAIForwardResult, turnErr error) {
			hooksMu.Lock()
			afterTurnCalls++
			if turnErr != nil {
				finalErr = turnErr
			}
			hooksMu.Unlock()
		},
	}

	server, serverErr := startPassthroughHookRecordingServer(t, controlCtx, newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream), passthroughLifecycleAccount(), hooks)
	defer server.Close()
	clientConn := dialPassthroughLifecycleClient(t, server)
	defer func() { _ = clientConn.CloseNow() }()
	requirePassthroughUpstreamWrite(t, upstream, time.Second)
	_, err := readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
	require.NoError(t, err)

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1"}`))
	cancelWrite()
	require.NoError(t, err)
	select {
	case payload := <-upstream.writes:
		t.Fatalf("rejected response.create was forwarded upstream: %s", payload)
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case err = <-serverErr:
		require.ErrorIs(t, err, rejection)
	case <-time.After(3 * time.Second):
		t.Fatal("passthrough ingress did not exit after BeforeTurn rejection")
	}

	hooksMu.Lock()
	gotBeforeTurn, gotAfter, gotFinalErr := beforeTurnTurn, afterTurnCalls, finalErr
	hooksMu.Unlock()
	require.Equal(t, 2, gotBeforeTurn)
	require.Equal(t, 1, gotAfter, "only the admitted turn is finalized; policy rejection never starts a second upstream turn")
	require.NoError(t, gotFinalErr)
}

func TestPassthroughIngressFreezesSubsequentTurnBeforeRequestPolicy(t *testing.T) {
	testPassthroughIngressFreezesSubsequentTurnBeforeRequestPolicy(t, coderws.MessageText)
}

func TestPassthroughIngressFreezesBinarySubsequentTurnBeforeRequestPolicy(t *testing.T) {
	testPassthroughIngressFreezesSubsequentTurnBeforeRequestPolicy(t, coderws.MessageBinary)
}

func testPassthroughIngressFreezesSubsequentTurnBeforeRequestPolicy(t *testing.T, secondMessageType coderws.MessageType) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	controlCtx, cancelControl := context.WithCancelCause(context.Background())
	defer cancelControl(context.Canceled)

	upstream := newStagedPassthroughConn()
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_first","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`)

	type turnStart struct {
		turn      int
		startedAt time.Time
	}
	turnStarts := make(chan turnStart, 2)
	beforeRequestEntered := make(chan time.Time, 1)
	releaseBeforeRequest := make(chan struct{})
	hooks := &OpenAIWSIngressHooks{
		InitialTurnStartedAt: time.Now(),
		TurnStarted: func(turn int, startedAt time.Time) {
			turnStarts <- turnStart{turn: turn, startedAt: startedAt}
		},
		BeforeRequest: func(turn int, _ []byte, _ string) error {
			if turn == 2 {
				beforeRequestEntered <- time.Now()
				<-releaseBeforeRequest
			}
			return nil
		},
	}

	server, serverErr := startPassthroughHookRecordingServer(
		t,
		controlCtx,
		newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream),
		passthroughLifecycleAccount(),
		hooks,
	)
	defer server.Close()
	clientConn := dialPassthroughLifecycleClient(t, server)
	defer func() { _ = clientConn.CloseNow() }()

	require.Equal(t, "response.create", gjson.GetBytes(requirePassthroughUpstreamWrite(t, upstream, 3*time.Second), "type").String())
	firstCompleted, err := readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
	require.NoError(t, err)
	require.Equal(t, "resp_first", gjson.GetBytes(firstCompleted, "response.id").String())
	select {
	case first := <-turnStarts:
		require.Equal(t, 1, first.turn)
	case <-time.After(time.Second):
		t.Fatal("first turn start was not reported")
	}

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, secondMessageType, []byte(`{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_first"}`))
	cancelWrite()
	require.NoError(t, err)

	var policyEnteredAt time.Time
	select {
	case policyEnteredAt = <-beforeRequestEntered:
	case <-time.After(time.Second):
		t.Fatal("second turn did not enter BeforeRequest")
	}
	close(releaseBeforeRequest)
	require.Equal(t, "response.create", gjson.GetBytes(requirePassthroughUpstreamWrite(t, upstream, 3*time.Second), "type").String())
	upstream.Send(`{"type":"response.created","response":{"id":"resp_second","model":"gpt-5.1"}}`)
	secondCreated, err := readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
	require.NoError(t, err)
	require.Equal(t, "resp_second", gjson.GetBytes(secondCreated, "response.id").String())
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_second","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`)
	secondCompleted, err := readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
	require.NoError(t, err)
	require.Equal(t, "resp_second", gjson.GetBytes(secondCompleted, "response.id").String())

	select {
	case second := <-turnStarts:
		require.Equal(t, 2, second.turn)
		require.False(t, second.startedAt.After(policyEnteredAt), "第二轮开始时刻必须在 BeforeRequest 策略执行前冻结")
	case <-time.After(time.Second):
		t.Fatal("second turn start was not reported")
	}

	_ = clientConn.CloseNow()
	cancelControl(context.Canceled)
	select {
	case <-serverErr:
	case <-time.After(3 * time.Second):
		t.Fatal("passthrough ingress did not exit")
	}
}
