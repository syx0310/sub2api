package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// Codex plaintext_agent_message_content joins plaintext parts with newlines.
// Chat providers can receive those messages, but cannot decrypt encrypted parts.
func TestResponsesToChatCompletionsRequest_AgentMessageBecomesUserMessage(t *testing.T) {
	req := &ResponsesRequest{
		Model: "glm-5.3",
		Input: json.RawMessage(`[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>cwd</environment_context>"}]},
			{"type":"agent_message","id":"amsg_1","author":"/root","recipient":"/root/alpha_task","content":[
				{"type":"input_text","text":"Message Type: NEW_TASK\nTask name: /root/alpha_task\nSender: /root\nPayload:"},
				{"type":"input_text","text":"Reply with the single word: ALPHA"}
			]},
			{"type":"reasoning","summary":[{"type":"summary_text","text":"just answer"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ALPHA"}]},
			{"type":"agent_message","id":"amsg_2","author":"/root/alpha_task","recipient":"/root","content":[
				{"type":"input_text","text":"Message Type: FINAL_ANSWER\nTask name: /root\nSender: /root/alpha_task\nPayload:\nALPHA"}
			]}
		]`),
	}

	out, err := ResponsesToChatCompletionsRequest(req)
	require.NoError(t, err)
	require.Len(t, out.Messages, 4)

	require.Equal(t, "user", out.Messages[0].Role)
	require.Equal(t, "user", out.Messages[1].Role, "父线程发来的任务要成为 user 消息")
	require.JSONEq(t, `"Message Type: NEW_TASK\nTask name: /root/alpha_task\nSender: /root\nPayload:\nReply with the single word: ALPHA"`, string(out.Messages[1].Content), "明文片段按客户端规则以换行连接")
	require.Equal(t, "assistant", out.Messages[2].Role)
	require.JSONEq(t, `"ALPHA"`, string(out.Messages[2].Content))
	require.Equal(t, "user", out.Messages[3].Role, "子智能体回给父线程的消息同样是 user 消息")
	require.JSONEq(t, `"Message Type: FINAL_ANSWER\nTask name: /root\nSender: /root/alpha_task\nPayload:\nALPHA"`, string(out.Messages[3].Content))
}

func TestResponsesToChatCompletionsRequest_EncryptedAgentMessageRequiresNativeResponses(t *testing.T) {
	req := &ResponsesRequest{
		Model: "glm-5.3",
		Input: json.RawMessage(`[{"type":"agent_message","content":[
			{"type":"input_text","text":"Payload:"},
			{"type":"encrypted_content","encrypted_content":"opaque-private-payload"}
		]}]`),
	}
	out, err := ResponsesToChatCompletionsRequest(req)
	require.Nil(t, out)
	require.ErrorContains(t, err, "requires a native Responses upstream")
	require.NotContains(t, err.Error(), "opaque-private-payload")
}

func TestResponsesToChatCompletionsRequest_AgentMessageWithoutTextIsSkipped(t *testing.T) {
	req := &ResponsesRequest{
		Model: "glm-5.3",
		Input: json.RawMessage(`[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"type":"agent_message","author":"/root","recipient":"/root/a","content":[]},
			{"type":"agent_message","author":"/root","recipient":"/root/a"},
			{"type":"agent_message","author":"/root","recipient":"/root/a","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}
		]`),
	}

	out, err := ResponsesToChatCompletionsRequest(req)
	require.NoError(t, err)
	require.Len(t, out.Messages, 1, "没有文本正文的 agent_message 不产生消息")
	require.Equal(t, "user", out.Messages[0].Role)
}
