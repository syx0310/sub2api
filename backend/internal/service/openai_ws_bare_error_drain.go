package service

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
	"github.com/tidwall/gjson"
)

// A rejected create is already finished from the client's perspective. Allow
// only a short, absolute window for an authoritative response.failed/usage.
// This is independent of inference read timeouts and never renews on traffic.
const openAIWSBareErrorDrainTimeout = 500 * time.Millisecond

var errOpenAIWSBareErrorSettled = errors.New("upstream websocket request failed")

func openAIWSBareErrorCloseError() error {
	return NewOpenAIWSClientCloseError(coderws.StatusInternalError, "upstream request failed; reconnect before the next request", errOpenAIWSBareErrorSettled)
}

type openAIWSBareErrorDeadline struct {
	deadline   time.Time
	responseID string
}

// One per ingress execution, never reset after an error: the socket is no
// longer reusable. observe/acceptTrailing have one upstream-reader owner;
// deadlineContext/active may also be used by the downstream writer.
type openAIWSBareErrorDrain struct {
	state            atomic.Pointer[openAIWSBareErrorDeadline]
	activeResponseID string
	finished         bool
}

func (d *openAIWSBareErrorDrain) active() bool { return d != nil && d.state.Load() != nil }

func (d *openAIWSBareErrorDrain) deadlineContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if d != nil {
		if state := d.state.Load(); state != nil {
			return context.WithDeadline(ctx, state.deadline)
		}
	}
	return ctx, func() {}
}

func (d *openAIWSBareErrorDrain) observe(payload []byte) {
	event := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
	if d.active() {
		if event == "response.failed" {
			d.finished = true
		}
		return
	}
	switch event {
	case "response.created":
		d.activeResponseID = strings.Clone(gjson.GetBytes(payload, "response.id").String())
	case "error":
		id := gjson.GetBytes(payload, "response_id").String()
		if id == "" {
			id = gjson.GetBytes(payload, "response.id").String()
		}
		if id == "" {
			id = d.activeResponseID
		}
		d.state.Store(&openAIWSBareErrorDeadline{deadline: time.Now().Add(openAIWSBareErrorDrainTimeout), responseID: strings.Clone(id)})
	default:
		if isOpenAIWSTerminalEvent(event) {
			d.activeResponseID = ""
		}
	}
}

func (d *openAIWSBareErrorDrain) acceptTrailing(payload []byte) bool {
	state := d.state.Load()
	if state == nil {
		return true
	}
	if d.finished || !time.Now().Before(state.deadline) {
		return false
	}
	event := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
	// Never attribute another generation or a contradictory success to the
	// failed turn, and never let it establish a continuation/cache baseline.
	if event == "response.created" || (isOpenAIWSTerminalEvent(event) && event != "response.failed") {
		return false
	}
	id := gjson.GetBytes(payload, "response.id").String()
	if id == "" {
		id = gjson.GetBytes(payload, "response_id").String()
	}
	return id == "" || state.responseID == "" || id == state.responseID
}

// The existing relay owns error/failed usage coalescing. This adapter adds the
// missing bounded read and forces teardown even if both peers stay connected.
type openAIWSBareErrorFrameConn struct {
	openaiwsv2.FrameConn
	drain *openAIWSBareErrorDrain
	abort func() error
}

func abortOpenAIWSBareErrorConn(conn interface{ Close() error }) error {
	if immediate, ok := conn.(interface{ CloseNow() error }); ok {
		return immediate.CloseNow()
	}
	return conn.Close()
}

func (c *openAIWSBareErrorFrameConn) Close() error {
	if c.drain.active() && c.abort != nil {
		return c.abort()
	}
	return c.FrameConn.Close()
}

func (c *openAIWSBareErrorFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	if c.drain.finished {
		return coderws.MessageText, nil, openAIWSBareErrorCloseError()
	}
	readCtx, cancel := c.drain.deadlineContext(ctx)
	defer cancel()
	kind, payload, err := c.FrameConn.ReadFrame(readCtx)
	if c.drain.active() && (err != nil || (kind == coderws.MessageText && !c.drain.acceptTrailing(payload))) {
		return kind, nil, openAIWSBareErrorCloseError()
	}
	return kind, payload, err
}
