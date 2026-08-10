package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHandlerTranslatesAnthropicTextMessageToChatCompletions(t *testing.T) {
	t.Parallel()

	var gotPath string
	var gotAuthorization string
	var gotRequest map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-text","model":"nvidia/test","choices":[{"index":0,"message":{"role":"assistant","content":"Hello from NVIDIA"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":4,"total_tokens":15}}`))
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL,
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       1,
		MaxRequestBytes:     1 << 20,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	const body = `{"model":"nvidia/test","max_tokens":128,"system":"You are concise.","messages":[{"role":"user","content":"Say hello"}],"temperature":0.2,"top_p":0.9}`
	response := serveMessages(handler, body)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("upstream path = %q, want /v1/chat/completions", gotPath)
	}
	if gotAuthorization != "Bearer server-secret" {
		t.Fatalf("upstream authorization = %q, want server-owned bearer", gotAuthorization)
	}
	if strings.Contains(gotAuthorization, "client-token") {
		t.Fatal("client authorization leaked upstream")
	}

	messages, ok := gotRequest["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("upstream messages = %#v, want system + user", gotRequest["messages"])
	}
	assertMessageRoleContent(t, messages[0], "system", "You are concise.")
	assertMessageRoleContent(t, messages[1], "user", "Say hello")
	if gotRequest["model"] != "nvidia/test" || gotRequest["max_tokens"] != float64(128) {
		t.Fatalf("upstream model/max_tokens = %#v/%#v", gotRequest["model"], gotRequest["max_tokens"])
	}
	if gotRequest["temperature"] != 0.2 || gotRequest["top_p"] != 0.9 {
		t.Fatalf("upstream sampling params = temperature:%#v top_p:%#v", gotRequest["temperature"], gotRequest["top_p"])
	}
	if gotRequest["stream"] != false {
		t.Fatalf("upstream stream = %#v, want false", gotRequest["stream"])
	}

	var anthropic struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Role       string `json:"role"`
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &anthropic); err != nil {
		t.Fatalf("decode Anthropic response: %v", err)
	}
	if anthropic.ID != "chatcmpl-text" || anthropic.Type != "message" || anthropic.Role != "assistant" || anthropic.Model != "nvidia/test" {
		t.Fatalf("Anthropic envelope = %#v", anthropic)
	}
	if anthropic.StopReason != "end_turn" {
		t.Fatalf("stop_reason = %q, want end_turn", anthropic.StopReason)
	}
	if len(anthropic.Content) != 1 || anthropic.Content[0].Type != "text" || anthropic.Content[0].Text != "Hello from NVIDIA" {
		t.Fatalf("content = %#v", anthropic.Content)
	}
	if anthropic.Usage.InputTokens != 11 || anthropic.Usage.OutputTokens != 4 {
		t.Fatalf("usage = %#v", anthropic.Usage)
	}
}

func TestHandlerTranslatesAnthropicToolUseAndToolResultRoundTrip(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	var firstRequest map[string]any
	var secondRequest map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch call {
		case 1:
			firstRequest = body
			_, _ = w.Write([]byte(`{"id":"chatcmpl-tool","model":"nvidia/test","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_weather","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Ankara\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":8,"total_tokens":28}}`))
		case 2:
			secondRequest = body
			_, _ = w.Write([]byte(`{"id":"chatcmpl-final","model":"nvidia/test","choices":[{"index":0,"message":{"role":"assistant","content":"It is sunny."},"finish_reason":"stop"}],"usage":{"prompt_tokens":30,"completion_tokens":5,"total_tokens":35}}`))
		default:
			t.Fatalf("unexpected upstream call %d", call)
		}
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL,
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       1,
		MaxRequestBytes:     1 << 20,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	const firstBody = `{"model":"nvidia/test","max_tokens":128,"messages":[{"role":"user","content":"What is the weather in Ankara?"}],"tools":[{"name":"get_weather","description":"Get weather","input_schema":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}],"tool_choice":{"type":"any","disable_parallel_tool_use":true}}`
	firstResponse := serveMessages(handler, firstBody)
	if firstResponse.Code != http.StatusOK {
		t.Fatalf("first status = %d, body=%s", firstResponse.Code, firstResponse.Body.String())
	}

	tools, ok := firstRequest["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("upstream tools = %#v", firstRequest["tools"])
	}
	tool := tools[0].(map[string]any)
	function := tool["function"].(map[string]any)
	if tool["type"] != "function" || function["name"] != "get_weather" || function["description"] != "Get weather" {
		t.Fatalf("translated tool = %#v", tool)
	}
	if firstRequest["tool_choice"] != "required" {
		t.Fatalf("tool_choice = %#v, want required", firstRequest["tool_choice"])
	}
	if firstRequest["parallel_tool_calls"] != false {
		t.Fatalf("parallel_tool_calls = %#v, want false", firstRequest["parallel_tool_calls"])
	}

	var toolMessage struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type  string         `json:"type"`
			ID    string         `json:"id"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(firstResponse.Body.Bytes(), &toolMessage); err != nil {
		t.Fatalf("decode tool response: %v", err)
	}
	if toolMessage.StopReason != "tool_use" || len(toolMessage.Content) != 1 {
		t.Fatalf("tool response = %#v", toolMessage)
	}
	if block := toolMessage.Content[0]; block.Type != "tool_use" || block.ID != "call_weather" || block.Name != "get_weather" || block.Input["location"] != "Ankara" {
		t.Fatalf("tool_use block = %#v", block)
	}

	const secondBody = `{"model":"nvidia/test","max_tokens":128,"tools":[{"name":"get_weather","description":"Get weather","input_schema":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}],"messages":[{"role":"user","content":"What is the weather in Ankara?"},{"role":"assistant","content":[{"type":"tool_use","id":"call_weather","name":"get_weather","input":{"location":"Ankara"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_weather","content":"sunny"}]}]}`
	secondResponse := serveMessages(handler, secondBody)
	if secondResponse.Code != http.StatusOK {
		t.Fatalf("second status = %d, body=%s", secondResponse.Code, secondResponse.Body.String())
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls.Load())
	}

	messages, ok := secondRequest["messages"].([]any)
	if !ok || len(messages) != 3 {
		t.Fatalf("second upstream messages = %#v", secondRequest["messages"])
	}
	assistant := messages[1].(map[string]any)
	assistantCalls := assistant["tool_calls"].([]any)
	assistantCall := assistantCalls[0].(map[string]any)
	assistantFunction := assistantCall["function"].(map[string]any)
	if assistant["role"] != "assistant" || assistantCall["id"] != "call_weather" || assistantFunction["name"] != "get_weather" || assistantFunction["arguments"] != `{"location":"Ankara"}` {
		t.Fatalf("assistant tool_calls message = %#v", assistant)
	}
	result := messages[2].(map[string]any)
	if result["role"] != "tool" || result["tool_call_id"] != "call_weather" || result["content"] != "sunny" {
		t.Fatalf("tool result message = %#v", result)
	}

	var final struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(secondResponse.Body.Bytes(), &final); err != nil {
		t.Fatalf("decode final response: %v", err)
	}
	if final.StopReason != "end_turn" || len(final.Content) != 1 || final.Content[0].Text != "It is sunny." {
		t.Fatalf("final response = %#v", final)
	}
}

func TestHandlerRejectsStreamingAnthropicMessagesBeforeUpstream(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	handler := newMessagesTestHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))

	response := serveMessages(handler, `{"model":"nvidia/test","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatalf("upstream calls = %d, want 0", calls.Load())
	}
	var body struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Type != "error" || body.Error.Type != "invalid_request_error" || !strings.Contains(body.Error.Message, "stream") {
		t.Fatalf("error response = %#v", body)
	}
}

func assertMessageRoleContent(t *testing.T, value any, role, content string) {
	t.Helper()
	message, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("message = %#v, want object", value)
	}
	if message["role"] != role || message["content"] != content {
		t.Fatalf("message = %#v, want role=%q content=%q", message, role, content)
	}
}

func TestHandlerRejectsUnsupportedAnthropicContentBlockBeforeUpstream(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	handler := newMessagesTestHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))

	response := serveMessages(handler, `{"model":"nvidia/test","max_tokens":32,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}]}]}`)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatalf("upstream calls = %d, want 0", calls.Load())
	}
	if !strings.Contains(response.Body.String(), "unsupported user content block") {
		t.Fatalf("body = %s, want unsupported block error", response.Body.String())
	}
}

func TestHandlerTranslatesAnthropicNamedToolChoice(t *testing.T) {
	t.Parallel()

	var gotRequest map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-tool","model":"nvidia/test","choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_one","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":4,"completion_tokens":3}}`))
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL,
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       1,
		MaxRequestBytes:     1 << 20,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	response := serveMessages(handler, `{"model":"nvidia/test","max_tokens":32,"messages":[{"role":"user","content":"lookup"}],"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{}}}],"tool_choice":{"type":"tool","name":"lookup","disable_parallel_tool_use":true}}`)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	choice, ok := gotRequest["tool_choice"].(map[string]any)
	if !ok || choice["type"] != "function" {
		t.Fatalf("tool_choice = %#v", gotRequest["tool_choice"])
	}
	function, ok := choice["function"].(map[string]any)
	if !ok || function["name"] != "lookup" {
		t.Fatalf("tool_choice function = %#v", choice["function"])
	}
	if gotRequest["parallel_tool_calls"] != false {
		t.Fatalf("parallel_tool_calls = %#v, want false", gotRequest["parallel_tool_calls"])
	}
}

func TestHandlerTranslatesUpstreamRateLimitToAnthropicError(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"provider busy"}}`))
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL,
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       1,
		MaxRetries:          0,
		MaxRequestBytes:     1 << 20,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	response := serveMessages(handler, `{"model":"nvidia/test","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`)

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusTooManyRequests, response.Body.String())
	}
	if got := response.Header().Get("Retry-After"); got != "7" {
		t.Fatalf("Retry-After = %q, want 7", got)
	}
	var body struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Type != "error" || body.Error.Type != "rate_limit_error" || body.Error.Message != "provider busy" {
		t.Fatalf("error body = %#v", body)
	}
}

func newMessagesTestHandler(t *testing.T, upstreamHandler http.Handler) http.Handler {
	t.Helper()

	upstream := httptest.NewServer(upstreamHandler)
	t.Cleanup(upstream.Close)
	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL,
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       1,
		MaxRequestBytes:     1 << 20,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler
}

func TestHandlerRejectsAnthropicStopSequencesBeforeUpstream(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	handler := newMessagesTestHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))

	response := serveMessages(handler, `{"model":"nvidia/test","max_tokens":32,"stop_sequences":["STOP"],"messages":[{"role":"user","content":"hi"}]}`)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatalf("upstream calls = %d, want 0", calls.Load())
	}
	if !strings.Contains(response.Body.String(), "stop_sequences") {
		t.Fatalf("body = %s, want stop_sequences error", response.Body.String())
	}
}

func serveMessages(handler http.Handler, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer client-token")
	request.Header.Set("anthropic-version", "2023-06-01")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
