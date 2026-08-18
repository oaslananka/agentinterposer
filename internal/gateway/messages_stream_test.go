package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type testSSEEvent struct {
	Event string
	Data  map[string]any
}

func TestHandlerStreamsAnthropicTextEvents(t *testing.T) {
	t.Parallel()

	var gotRequest map[string]any
	handler := newMessagesTestHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, chunk := range []string{
			`{"id":"chatcmpl-stream","model":"nvidia/test","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
			`{"id":"chatcmpl-stream","model":"nvidia/test","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
			`{"id":"chatcmpl-stream","model":"nvidia/test","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
			`{"id":"chatcmpl-stream","model":"nvidia/test","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`{"id":"chatcmpl-stream","model":"nvidia/test","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":2,"total_tokens":13}}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))

	response := serveMessages(handler, `{"model":"nvidia/test","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if !response.Flushed {
		t.Fatal("streaming Messages response was not flushed")
	}
	if gotRequest["stream"] != true {
		t.Fatalf("upstream stream = %#v, want true", gotRequest["stream"])
	}
	streamOptions, ok := gotRequest["stream_options"].(map[string]any)
	if !ok || streamOptions["include_usage"] != true {
		t.Fatalf("upstream stream_options = %#v, want include_usage=true", gotRequest["stream_options"])
	}

	events := parseTestSSE(t, response.Body.String())
	wantTypes := []string{"message_start", "content_block_start", "content_block_delta", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	assertSSEEventTypes(t, events, wantTypes)

	start := events[0].Data["message"].(map[string]any)
	if start["id"] != "chatcmpl-stream" || start["model"] != "nvidia/test" || start["role"] != "assistant" {
		t.Fatalf("message_start = %#v", start)
	}
	startUsage := start["usage"].(map[string]any)
	if startUsage["input_tokens"] != float64(0) || startUsage["output_tokens"] != float64(0) {
		t.Fatalf("message_start usage = %#v", startUsage)
	}
	if got := events[2].Data["delta"].(map[string]any)["text"]; got != "Hello" {
		t.Fatalf("first text delta = %#v", got)
	}
	if got := events[3].Data["delta"].(map[string]any)["text"]; got != " world" {
		t.Fatalf("second text delta = %#v", got)
	}
	delta := events[5].Data["delta"].(map[string]any)
	if delta["stop_reason"] != "end_turn" || delta["stop_sequence"] != nil {
		t.Fatalf("message_delta = %#v", delta)
	}
	usage := events[5].Data["usage"].(map[string]any)
	if usage["input_tokens"] != float64(11) || usage["output_tokens"] != float64(2) {
		t.Fatalf("message_delta usage = %#v", usage)
	}
}

func TestHandlerStreamsBufferedAnthropicToolUse(t *testing.T) {
	t.Parallel()

	handler := newMessagesTestHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		if request["tool_choice"] != "required" || request["stream"] != true {
			t.Fatalf("upstream tool stream request = %#v", request)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{
			`{"id":"chatcmpl-tool-stream","model":"nvidia/test","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
			`{"id":"chatcmpl-tool-stream","model":"nvidia/test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_weather","type":"function","function":{"name":"get_weather","arguments":"{\"location\":"}}]},"finish_reason":null}]}`,
			`{"id":"chatcmpl-tool-stream","model":"nvidia/test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Ankara\"}"}}]},"finish_reason":null}]}`,
			`{"id":"chatcmpl-tool-stream","model":"nvidia/test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			`{"id":"chatcmpl-tool-stream","model":"nvidia/test","choices":[],"usage":{"prompt_tokens":20,"completion_tokens":8,"total_tokens":28}}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))

	response := serveMessages(handler, `{"model":"nvidia/test","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"weather"}],"tools":[{"name":"get_weather","input_schema":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}],"tool_choice":{"type":"any","disable_parallel_tool_use":true}}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}

	events := parseTestSSE(t, response.Body.String())
	wantTypes := []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	assertSSEEventTypes(t, events, wantTypes)
	block := events[1].Data["content_block"].(map[string]any)
	if block["type"] != "tool_use" || block["id"] != "call_weather" || block["name"] != "get_weather" {
		t.Fatalf("tool content_block_start = %#v", block)
	}
	if input, ok := block["input"].(map[string]any); !ok || len(input) != 0 {
		t.Fatalf("tool start input = %#v, want empty object", block["input"])
	}
	toolDelta := events[2].Data["delta"].(map[string]any)
	if toolDelta["type"] != "input_json_delta" || toolDelta["partial_json"] != `{"location":"Ankara"}` {
		t.Fatalf("tool delta = %#v", toolDelta)
	}
	messageDelta := events[4].Data["delta"].(map[string]any)
	if messageDelta["stop_reason"] != "tool_use" {
		t.Fatalf("stop_reason = %#v, want tool_use", messageDelta["stop_reason"])
	}
}

func TestHandlerStreamsAnthropicErrorWhenUpstreamSSEBecomesInvalid(t *testing.T) {
	t.Parallel()

	handler := newMessagesTestHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-bad\",\"model\":\"nvidia/test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: this-is-not-json\n\n")
	}))

	response := serveMessages(handler, `{"model":"nvidia/test","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 once stream starts; body=%s", response.Code, response.Body.String())
	}
	events := parseTestSSE(t, response.Body.String())
	if len(events) != 2 || events[0].Event != "message_start" || events[1].Event != "error" {
		t.Fatalf("events = %#v, want message_start then error", events)
	}
	errObject := events[1].Data["error"].(map[string]any)
	if errObject["type"] != "api_error" {
		t.Fatalf("stream error = %#v", errObject)
	}
}

func TestHandlerFlushesAnthropicTextDeltaBeforeUpstreamCompletes(t *testing.T) {
	t.Parallel()

	continueUpstream := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-progressive\",\"model\":\"nvidia/test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"first\"},\"finish_reason\":null}]}\n\n")
		flusher.Flush()
		select {
		case <-continueUpstream:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-progressive\",\"model\":\"nvidia/test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\" second\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-progressive\",\"model\":\"nvidia/test\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{UpstreamURL: upstream.URL, UpstreamBearerToken: "server-secret", MaxConcurrent: 1, MaxRequestBytes: 1 << 20})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	gateway := httptest.NewServer(handler)
	defer gateway.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway.URL+"/v1/messages", strings.NewReader(`{"model":"nvidia/test","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("anthropic-version", supportedAnthropicMessagesVersion)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("streaming request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}

	reader := bufio.NewReader(response.Body)
	seenFirstDelta := false
	for !seenFirstDelta {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read first streamed event: %v", err)
		}
		if strings.Contains(line, `"type":"text_delta"`) && strings.Contains(line, `"text":"first"`) {
			seenFirstDelta = true
		}
	}
	close(continueUpstream)

	rest, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read remaining stream: %v", err)
	}
	if !strings.Contains(string(rest), `"text":" second"`) || !strings.Contains(string(rest), `event: message_stop`) {
		t.Fatalf("remaining stream missing terminal events")
	}
}

func parseTestSSE(t *testing.T, raw string) []testSSEEvent {
	t.Helper()

	scanner := bufio.NewScanner(strings.NewReader(raw))
	var events []testSSEEvent
	var eventName string
	var dataLine string
	flush := func() {
		if eventName == "" && dataLine == "" {
			return
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(dataLine), &data); err != nil {
			t.Fatalf("decode SSE event %q data %q: %v", eventName, dataLine, err)
		}
		events = append(events, testSSEEvent{Event: eventName, Data: data})
		eventName, dataLine = "", ""
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			eventName = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			dataLine += strings.TrimPrefix(line, "data: ")
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan SSE: %v", err)
	}
	flush()
	return events
}

func assertSSEEventTypes(t *testing.T, events []testSSEEvent, want []string) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("event count = %d, want %d; events=%#v", len(events), len(want), events)
	}
	for i := range want {
		if events[i].Event != want[i] {
			t.Fatalf("event[%d] = %q, want %q", i, events[i].Event, want[i])
		}
		if events[i].Data["type"] != want[i] {
			t.Fatalf("event[%d] data.type = %#v, want %q", i, events[i].Data["type"], want[i])
		}
	}
}

func TestHandlerStreamsParallelToolCallsInDeterministicOrder(t *testing.T) {
	t.Parallel()

	handler := newMessagesTestHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{
			`{"id":"chatcmpl-parallel","model":"nvidia/test","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
			`{"id":"chatcmpl-parallel","model":"nvidia/test","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_two","type":"function","function":{"name":"second","arguments":"{\"value\":2}"}},{"index":0,"id":"call_one","type":"function","function":{"name":"first","arguments":"{\"value\":1}"}}]},"finish_reason":null}]}`,
			`{"id":"chatcmpl-parallel","model":"nvidia/test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))

	response := serveMessages(handler, `{"model":"nvidia/test","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"call both"}],"tools":[{"name":"first","input_schema":{"type":"object"}},{"name":"second","input_schema":{"type":"object"}}]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}

	events := parseTestSSE(t, response.Body.String())
	wantTypes := []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	assertSSEEventTypes(t, events, wantTypes)
	first := events[1].Data["content_block"].(map[string]any)
	second := events[4].Data["content_block"].(map[string]any)
	if first["id"] != "call_one" || first["name"] != "first" {
		t.Fatalf("first tool block = %#v", first)
	}
	if second["id"] != "call_two" || second["name"] != "second" {
		t.Fatalf("second tool block = %#v", second)
	}
	if events[1].Data["index"] != float64(0) || events[4].Data["index"] != float64(1) {
		t.Fatalf("tool block indices = %#v/%#v", events[1].Data["index"], events[4].Data["index"])
	}
}
