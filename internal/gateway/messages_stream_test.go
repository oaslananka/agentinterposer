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

type failingSSEWriter struct {
	header        http.Header
	failAt        int
	writeAttempts int
}

func (w *failingSSEWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *failingSSEWriter) WriteHeader(int) {}

func (w *failingSSEWriter) Write(p []byte) (int, error) {
	w.writeAttempts++
	if w.writeAttempts == w.failAt {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

func (w *failingSSEWriter) Flush() {}

func TestReadSSEFrameRejectsOversizedUnterminatedLine(t *testing.T) {
	t.Parallel()

	const limit = 64
	input := "data: " + strings.Repeat("x", limit) // Prefix pushes the raw frame over the limit; no terminating newline.
	frame, err := readSSEFrame(bufio.NewReader(strings.NewReader(input)), limit)
	if err == nil || !strings.Contains(err.Error(), "64 bytes") {
		t.Fatalf("readSSEFrame() data len=%d error=%v, want 64-byte resource-limit error", len(frame.data), err)
	}
}

func TestReadSSEFrameRejectsOversizedAccumulatedFrame(t *testing.T) {
	t.Parallel()

	const limit = 64
	input := "data: " + strings.Repeat("a", 28) + "\n" +
		"data: " + strings.Repeat("b", 28) + "\n\n"
	frame, err := readSSEFrame(bufio.NewReader(strings.NewReader(input)), limit)
	if err == nil || !strings.Contains(err.Error(), "64 bytes") {
		t.Fatalf("readSSEFrame() data len=%d error=%v, want 64-byte resource-limit error", len(frame.data), err)
	}
}

func TestReadSSEFrameResetsLimitAcrossDataLessFrames(t *testing.T) {
	t.Parallel()

	const limit = 64
	input := "id: " + strings.Repeat("a", 45) + "\n\n" +
		"data: {\"id\":\"chatcmpl-reset\"}\n\n"
	frame, err := readSSEFrame(bufio.NewReader(strings.NewReader(input)), limit)
	if err != nil {
		t.Fatalf("readSSEFrame() error = %v", err)
	}
	if frame.data != `{"id":"chatcmpl-reset"}` {
		t.Fatalf("frame = %#v", frame)
	}
}

func TestReadSSEFrameIgnoresIDAndRetryMetadata(t *testing.T) {
	t.Parallel()

	input := "id: upstream-42\nretry: 1000\ndata: {\"id\":\"chatcmpl-meta\"}\n\n"
	frame, err := readSSEFrame(bufio.NewReader(strings.NewReader(input)), 128)
	if err != nil {
		t.Fatalf("readSSEFrame() error = %v", err)
	}
	if frame.event != "" || frame.data != `{"id":"chatcmpl-meta"}` {
		t.Fatalf("frame = %#v", frame)
	}
}

func TestMessagesStreamReportsOversizedFrameBeforeStart(t *testing.T) {
	t.Parallel()

	response := httptest.NewRecorder()
	body := "data: " + strings.Repeat("x", 128) + "\n\n"
	streamAnthropicMessagesWithFrameLimit(response, strings.NewReader(body), "nvidia/test", 64)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "64 bytes") {
		t.Fatalf("body = %s, want resource-limit error", response.Body.String())
	}
}

func TestMessagesStreamReportsOversizedFrameAfterStart(t *testing.T) {
	t.Parallel()

	response := httptest.NewRecorder()
	first := `data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}` + "\n\n"
	oversized := "data: " + strings.Repeat("x", 256) + "\n\n"
	streamAnthropicMessagesWithFrameLimit(response, strings.NewReader(first+oversized), "m", 160)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 once stream starts; body=%s", response.Code, response.Body.String())
	}
	events := parseTestSSE(t, response.Body.String())
	if len(events) == 0 || events[len(events)-1].Event != "error" {
		t.Fatalf("events = %#v, want terminal error event", events)
	}
	errObject := events[len(events)-1].Data["error"].(map[string]any)
	if message, _ := errObject["message"].(string); !strings.Contains(message, "160 bytes") {
		t.Fatalf("stream error = %#v, want resource-limit error", errObject)
	}
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

func TestHandlerPreservesUpstreamSSEErrorBeforeMessageStart(t *testing.T) {
	t.Parallel()

	handler := newMessagesTestHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n")
	}))

	response := serveMessages(handler, `{"model":"nvidia/test","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 streaming error; body=%s", response.Code, response.Body.String())
	}
	events := parseTestSSE(t, response.Body.String())
	if len(events) != 1 || events[0].Event != "error" {
		t.Fatalf("events = %#v, want one error event", events)
	}
	errObject := events[0].Data["error"].(map[string]any)
	if errObject["type"] != "api_error" || errObject["message"] != "Overloaded" {
		t.Fatalf("stream error = %#v", errObject)
	}
}

func TestHandlerValidatesUpstreamSSEEventMetadata(t *testing.T) {
	t.Parallel()

	normalChunk := `{"id":"chatcmpl-event","model":"nvidia/test","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":"stop"}]}`
	errorChunk := `{"error":{"type":"overloaded_error","message":"Overloaded"}}`
	tests := []struct {
		name       string
		stream     string
		wantStatus int
		wantEvents []string
		wantBody   string
	}{
		{
			name:       "data_only_default_message",
			stream:     "data: " + normalChunk + "\n\ndata: [DONE]\n\n",
			wantStatus: http.StatusOK,
			wantEvents: []string{"message_start", "message_delta", "message_stop"},
		},
		{
			name:       "explicit_message_event",
			stream:     "event: message\ndata: " + normalChunk + "\n\nevent: message\ndata: [DONE]\n\n",
			wantStatus: http.StatusOK,
			wantEvents: []string{"message_start", "message_delta", "message_stop"},
		},
		{
			name:       "explicit_error_event",
			stream:     "event: error\ndata: " + errorChunk + "\n\n",
			wantStatus: http.StatusOK,
			wantEvents: []string{"error"},
		},
		{
			name:       "error_event_with_normal_chunk",
			stream:     "event: error\ndata: " + normalChunk + "\n\n",
			wantStatus: http.StatusBadGateway,
			wantBody:   "event metadata",
		},
		{
			name:       "unknown_event_with_data",
			stream:     "event: future_event\ndata: " + normalChunk + "\n\n",
			wantStatus: http.StatusBadGateway,
			wantBody:   "event metadata",
		},
		{
			name: "comments_and_data_less_event_are_ignored",
			stream: ": keep-alive\n\n" +
				"event: future_event\n\n" +
				"data: " + normalChunk + "\n\ndata: [DONE]\n\n",
			wantStatus: http.StatusOK,
			wantEvents: []string{"message_start", "message_delta", "message_stop"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := newMessagesTestHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, test.stream)
			}))
			response := serveMessages(handler, `{"model":"nvidia/test","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hello"}]}`)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if test.wantBody != "" {
				if !strings.Contains(response.Body.String(), test.wantBody) {
					t.Fatalf("body = %s, want %q", response.Body.String(), test.wantBody)
				}
				return
			}
			events := parseTestSSE(t, response.Body.String())
			assertSSEEventTypes(t, events, test.wantEvents)
		})
	}
}

func TestHandlerStreamsErrorWhenUpstreamEventMetadataChangesAfterStart(t *testing.T) {
	t.Parallel()

	handler := newMessagesTestHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w,
			"data: {\"id\":\"chatcmpl-event-late\",\"model\":\"nvidia/test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"},\"finish_reason\":null}]}\n\n"+
				"event: future_event\ndata: {\"id\":\"chatcmpl-event-late\",\"model\":\"nvidia/test\",\"choices\":[]}\n\n")
	}))

	response := serveMessages(handler, `{"model":"nvidia/test","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 once stream starts; body=%s", response.Code, response.Body.String())
	}
	events := parseTestSSE(t, response.Body.String())
	if len(events) < 2 || events[len(events)-1].Event != "error" {
		t.Fatalf("events = %#v, want terminal error event", events)
	}
	errObject := events[len(events)-1].Data["error"].(map[string]any)
	if message, _ := errObject["message"].(string); !strings.Contains(message, "event metadata") {
		t.Fatalf("stream error = %#v, want event metadata failure", errObject)
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

func TestMessagesStreamStopsWritingAfterDownstreamWriteFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		failAt     int
		body       string
		wantWrites int
	}{
		{
			name:       "message_start",
			failAt:     1,
			body:       "data: {\"id\":\"chatcmpl-write-fail\",\"model\":\"nvidia/test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n",
			wantWrites: 1,
		},
		{
			name:   "message_delta",
			failAt: 2,
			body: "data: {\"id\":\"chatcmpl-write-fail\",\"model\":\"nvidia/test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n" +
				"data: {\"id\":\"chatcmpl-write-fail\",\"model\":\"nvidia/test\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: [DONE]\n\n",
			wantWrites: 2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			writer := &failingSSEWriter{failAt: test.failAt}
			streamAnthropicMessages(writer, strings.NewReader(test.body), "nvidia/test")
			if writer.writeAttempts != test.wantWrites {
				t.Fatalf("downstream write attempts = %d, want %d", writer.writeAttempts, test.wantWrites)
			}
		})
	}
}

func TestHandlerCancelsUpstreamMessagesStreamWhenClientCancels(t *testing.T) {
	t.Parallel()

	upstreamCancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-cancel\",\"model\":\"nvidia/test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"first\"},\"finish_reason\":null}]}\n\n")
		flusher.Flush()
		<-r.Context().Done()
		close(upstreamCancelled)
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{UpstreamURL: upstream.URL, UpstreamBearerToken: "server-secret", MaxConcurrent: 1, MaxRequestBytes: 1 << 20})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	gateway := httptest.NewServer(handler)
	defer gateway.Close()

	ctx, cancel := context.WithCancel(context.Background())
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

	reader := bufio.NewReader(response.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read first streamed delta: %v", err)
		}
		if strings.Contains(line, `"type":"text_delta"`) {
			break
		}
	}
	cancel()
	_ = response.Body.Close()

	select {
	case <-upstreamCancelled:
	case <-time.After(time.Second):
		t.Fatal("upstream request context was not cancelled after downstream client cancellation")
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
	if requestID := response.Header.Get("request-id"); requestID == "" {
		t.Fatal("streaming response missing request-id header")
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
