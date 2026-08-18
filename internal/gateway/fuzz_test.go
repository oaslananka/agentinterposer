package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const maxFuzzProtocolInputBytes = 8 << 10

func boundedFuzzBytes(input []byte) []byte {
	if len(input) <= maxFuzzProtocolInputBytes {
		return input
	}
	return input[:maxFuzzProtocolInputBytes]
}

func boundedFuzzString(input string) string {
	if len(input) <= maxFuzzProtocolInputBytes {
		return input
	}
	return input[:maxFuzzProtocolInputBytes]
}

func FuzzDecodeAnthropicMessagesRequest(f *testing.F) {
	seeds := []string{
		`{"model":"nvidia/test","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"nvidia/test","max_tokens":16,"future":true,"messages":[{"role":"user","content":"hi"}]}`,
		`{"Model":"nvidia/test","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"nvidia/test","MAX_TOKENS":16,"messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"nvidia/test","max_tokens":16,"messages":[{"role":"user","content":[{"Type":"text","text":"hi"}]}]}`,
		`{"model":"nvidia/test","max_tokens":16,"messages":[{"role":"user","content":"hi"}]} {}`,
		`{"model":"nvidia/test","max_tokens":16,"thinking":{"type":"enabled","budget_tokens":16},"messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"nvidia/test","max_tokens":16,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_1","content":"ok"}]}]}`,
		`{"model":"nvidia/test","max_tokens":16,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}]}]}`,
		`{`,
		"\xff\xfe",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		input = boundedFuzzString(input)
		request, err := decodeAnthropicMessagesRequest([]byte(input))
		if err != nil {
			return
		}
		if !json.Valid([]byte(input)) {
			t.Fatal("strict decoder accepted invalid JSON")
		}
		// Decoding success is only the first contract boundary. Translation may
		// reject unsupported semantics, but it must never panic on decoded input.
		_, _ = translateAnthropicRequest(request)
	})
}

func FuzzReadSSEFrame(f *testing.F) {
	for _, seed := range []struct {
		input string
		limit uint16
	}{
		{"data: {\"id\":\"chatcmpl-1\"}\n\n", 256},
		{"event: message\ndata: one\ndata: two\n\n", 256},
		{"event: error\ndata: {\"error\":{\"message\":\"bad\"}}\n\n", 256},
		{": keepalive\nid: 42\nretry: 1000\ndata: ok\n\n", 256},
		{"data: no-newline", 32},
		{strings.Repeat("data: x\n", 32) + "\n", 64},
		{"data: crlf\r\n\r\n", 128},
	} {
		f.Add(seed.input, seed.limit)
	}

	f.Fuzz(func(t *testing.T, input string, rawLimit uint16) {
		input = boundedFuzzString(input)
		limit := int(rawLimit%4096) + 1
		frame, err := readSSEFrame(bufio.NewReader(strings.NewReader(input)), limit)
		if len(frame.data) > limit || len(frame.event) > limit {
			t.Fatalf("parsed frame exceeded raw frame limit: event=%d data=%d limit=%d", len(frame.event), len(frame.data), limit)
		}
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, errUpstreamSSEFrameTooLarge) {
			t.Fatalf("unexpected parser error class: %v", err)
		}
	})
}

func FuzzAnthropicMessagesStream(f *testing.F) {
	seeds := []string{
		"data: {\"id\":\"chatcmpl-text\",\"model\":\"nvidia/test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"id\":\"chatcmpl-text\",\"model\":\"nvidia/test\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n",
		"event: future\ndata: {}\n\n",
		"event: error\ndata: {\"id\":\"chatcmpl-bad\",\"choices\":[]}\n\n",
		"data: {not-json}\n\n",
		"data: {\"id\":\"chatcmpl-tool\",\"model\":\"nvidia/test\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"tool_1\",\"type\":\"function\",\"function\":{\"name\":\"probe\",\"arguments\":\"{\\\"value\\\":\"}}]},\"finish_reason\":null}]}\n\n" +
			"data: {\"id\":\"chatcmpl-tool\",\"model\":\"nvidia/test\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"1}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
			"data: [DONE]\n\n",
	}
	for _, seed := range seeds {
		f.Add([]byte(seed), uint16(4095))
	}

	f.Fuzz(func(t *testing.T, input []byte, rawLimit uint16) {
		input = boundedFuzzBytes(input)
		limit := int(rawLimit%4096) + 1
		response := httptest.NewRecorder()
		streamAnthropicMessagesWithFrameLimit(response, bytes.NewReader(input), "nvidia/test", limit)
		if response.Code < 200 || response.Code > 599 {
			t.Fatalf("invalid downstream HTTP status %d", response.Code)
		}
		// A bounded upstream input must not create unbounded downstream output.
		if response.Body.Len() > len(input)*32+(64<<10) {
			t.Fatalf("downstream output unexpectedly amplified: input=%d output=%d", len(input), response.Body.Len())
		}
	})
}

func FuzzFallbackRoutingPreservesNonModelSemantics(f *testing.F) {
	seeds := []string{
		`{"model":"meta/llama-3.2-11b-vision-instruct","input":"hello","metadata":{"keep":true},"stream":false}`,
		`{"model":"meta/llama-3.2-11b-vision-instruct","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}],"metadata":{"keep":true}}`,
		`{"model":"meta/llama-3.2-11b-vision-instruct","input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}]}`,
		`{"model":"nvidia/nemotron-3-super-120b-a12b","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}],"metadata":{"keep":true}}`,
		`{"model":"provider/unknown","input":"hello"}`,
		`not-json`,
	}
	for _, seed := range seeds {
		f.Add([]byte(seed))
	}

	h := &handler{fallbackModels: []string{
		"nvidia/nemotron-3-super-120b-a12b",
		"meta/llama-3.2-11b-vision-instruct",
	}}

	f.Fuzz(func(t *testing.T, input []byte) {
		input = boundedFuzzBytes(input)
		for name, routed := range map[string][]byte{
			"responses": h.routeResponsesBody(input),
			"chat":      h.routeChatCompletionBody(input),
		} {
			if !json.Valid(input) && !bytes.Equal(routed, input) {
				t.Fatalf("%s routing changed invalid JSON", name)
			}
			if bytes.Equal(routed, input) {
				continue
			}
			assertOnlyModelChanged(t, input, routed, name)
		}
	})
}

func assertOnlyModelChanged(t *testing.T, before, after []byte, route string) {
	t.Helper()
	var beforeObject, afterObject map[string]any
	if err := json.Unmarshal(before, &beforeObject); err != nil || beforeObject == nil {
		t.Fatalf("%s routing changed non-object input", route)
	}
	if err := json.Unmarshal(after, &afterObject); err != nil || afterObject == nil {
		t.Fatalf("%s routing produced invalid object: %v", route, err)
	}
	beforeModel, beforeOK := beforeObject["model"].(string)
	afterModel, afterOK := afterObject["model"].(string)
	if !beforeOK || !afterOK || beforeModel == afterModel || afterModel == "" {
		t.Fatalf("%s routing changed body without a valid model rewrite: before=%#v after=%#v", route, beforeObject["model"], afterObject["model"])
	}
	delete(beforeObject, "model")
	delete(afterObject, "model")
	if !reflect.DeepEqual(beforeObject, afterObject) {
		t.Fatalf("%s routing changed non-model semantics", route)
	}
}
