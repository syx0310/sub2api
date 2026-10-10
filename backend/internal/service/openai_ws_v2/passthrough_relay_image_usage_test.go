package openai_ws_v2

import (
	"context"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

// WS passthrough must account image tokens the way the HTTP Responses path
// does: image input from input/prompt_tokens_details, and the hosted
// tool_usage.image_gen breakdown filling counters the usage object omits.
func TestRelay_ImageUsageMatchesHTTPResponsesAccounting(t *testing.T) {
	t.Parallel()

	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := newPassthroughTestFrameConn(nil, false)
	frames := []passthroughTestFrame{
		{msgType: coderws.MessageText, payload: []byte(`{"type":"response.completed","response":{"id":"resp_tool_image","usage":{"input_tokens":100,"output_tokens":60},"tool_usage":{"image_gen":{"input_tokens_details":{"image_tokens":80},"output_tokens_details":{"image_tokens":50}}}}}`)},
		{msgType: coderws.MessageText, payload: []byte(`{"type":"response.completed","response":{"id":"resp_explicit_image","usage":{"input_tokens":30,"output_tokens":15,"input_tokens_details":{"image_tokens":20},"output_tokens_details":{"image_tokens":10}},"tool_usage":{"image_gen":{"input_tokens_details":{"image_tokens":800},"output_tokens_details":{"image_tokens":500}}}}}`)},
		{msgType: coderws.MessageText, payload: []byte(`{"type":"response.completed","response":{"id":"resp_prompt_details","usage":{"prompt_tokens":12,"completion_tokens":2,"prompt_tokens_details":{"image_tokens":7}}}}`)},
		{msgType: coderws.MessageText, payload: []byte(`{"type":"response.completed","response":{"id":"resp_text","usage":{"input_tokens":3,"output_tokens":4}}}`)},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var turns []RelayTurnResult
	type outcome struct {
		result RelayResult
		exit   *RelayExit
	}
	completed := make(chan RelayTurnResult, len(frames))
	done := make(chan outcome, 1)
	create := []byte(`{"type":"response.create","model":"gpt-5.4","tools":[{"type":"image_generation"}],"input":[]}`)
	go func() {
		result, relayExit := Relay(ctx, clientConn, upstreamConn, create,
			RelayOptions{OnTurnComplete: func(turn RelayTurnResult) { completed <- turn }})
		done <- outcome{result, relayExit}
	}()
	// Each billable terminal must belong to an accepted create. Unsolicited
	// terminals are intentionally ignored by the fork's duplicate-turn guard.
	for i, frame := range frames {
		if i > 0 {
			clientConn.readCh <- passthroughTestFrame{msgType: coderws.MessageText, payload: create}
		}
		require.Eventually(t, func() bool { return len(upstreamConn.Writes()) == i+1 }, time.Second, time.Millisecond)
		upstreamConn.readCh <- frame
		select {
		case turn := <-completed:
			turns = append(turns, turn)
		case <-ctx.Done():
			t.Fatal("image usage turn did not complete")
		}
	}
	require.NoError(t, upstreamConn.Close())
	var finished outcome
	select {
	case finished = <-done:
	case <-ctx.Done():
		t.Fatal("relay did not finish")
	}
	require.Nil(t, finished.exit)
	result := finished.result
	require.Len(t, turns, 4)

	require.Equal(t, 80, turns[0].Usage.ImageInputTokens, "tool_usage.image_gen fills a missing image input counter")
	require.Equal(t, 50, turns[0].Usage.ImageOutputTokens, "tool_usage.image_gen fills a missing image output counter")
	require.Equal(t, 20, turns[1].Usage.ImageInputTokens, "response usage takes precedence over the hosted tool breakdown")
	require.Equal(t, 10, turns[1].Usage.ImageOutputTokens, "response usage takes precedence over the hosted tool breakdown")
	require.Equal(t, 7, turns[2].Usage.ImageInputTokens)
	require.Zero(t, turns[3].Usage.ImageInputTokens, "a text turn must not reuse earlier image usage")
	require.Zero(t, turns[3].Usage.ImageOutputTokens, "a text turn must not reuse earlier image usage")

	require.Equal(t, 107, result.Usage.ImageInputTokens)
	require.Equal(t, 60, result.Usage.ImageOutputTokens)
	require.Equal(t, 145, result.Usage.InputTokens)
	require.Equal(t, 81, result.Usage.OutputTokens)
}
