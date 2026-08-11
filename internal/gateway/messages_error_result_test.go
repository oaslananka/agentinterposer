package gateway

import (
	"encoding/json"
	"testing"
)

func TestTranslateAnthropicErrorToolResultPreservesErrorSemantics(t *testing.T) {
	t.Parallel()

	const body = `{
		"model":"nvidia/test",
		"max_tokens":64,
		"tools":[{"name":"Bash","input_schema":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}],
		"messages":[
			{"role":"user","content":"run it"},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_fail","name":"Bash","input":{"command":"false"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_fail","content":"Exit code 1","is_error":true}]}
		]
	}`
	var request anthropicMessagesRequest
	if err := json.Unmarshal([]byte(body), &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}

	translated, err := translateAnthropicRequest(request)
	if err != nil {
		t.Fatalf("translateAnthropicRequest() error = %v", err)
	}
	if len(translated.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(translated.Messages))
	}
	result := translated.Messages[2]
	if result.Role != "tool" || result.ToolCallID != "call_fail" || result.Name != "Bash" {
		t.Fatalf("tool result routing = %#v", result)
	}
	const want = `{"agentinterposer_tool_result":{"version":1,"is_error":true,"content":"Exit code 1"}}`
	if result.Content != want {
		t.Fatalf("tool result content = %#v, want %s", result.Content, want)
	}
}
