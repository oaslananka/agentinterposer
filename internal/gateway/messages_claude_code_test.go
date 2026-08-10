package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHandlerRejectsInvalidAnthropicMetadataBeforeUpstream(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	handler := newMessagesTestHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-meta","model":"nvidia/test","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))

	userID := strings.Repeat("a", 513)
	body := `{"model":"nvidia/test","max_tokens":32,"metadata":{"user_id":"` + userID + `"},"messages":[{"role":"user","content":"hi"}]}`
	response := serveMessages(handler, body)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatalf("upstream calls = %d, want 0", calls.Load())
	}
	if !strings.Contains(response.Body.String(), "metadata.user_id") {
		t.Fatalf("body = %s, want metadata.user_id validation error", response.Body.String())
	}
}

func TestHandlerRejectsInvalidAnthropicCacheControlBeforeUpstream(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	handler := newMessagesTestHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-cache","model":"nvidia/test","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))

	body := `{"model":"nvidia/test","max_tokens":32,"system":[{"type":"text","text":"system","cache_control":{"type":"persistent"}}],"messages":[{"role":"user","content":"hi"}]}`
	response := serveMessages(handler, body)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatalf("upstream calls = %d, want 0", calls.Load())
	}
	if !strings.Contains(response.Body.String(), "cache_control") {
		t.Fatalf("body = %s, want cache_control validation error", response.Body.String())
	}
}

func TestHandlerRejectsInvalidAnthropicCacheControlTTLBeforeUpstream(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	handler := newMessagesTestHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))

	body := `{"model":"nvidia/test","max_tokens":32,"system":[{"type":"text","text":"system","cache_control":{"type":"ephemeral","ttl":"2h"}}],"messages":[{"role":"user","content":"hi"}]}`
	response := serveMessages(handler, body)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatalf("upstream calls = %d, want 0", calls.Load())
	}
	if !strings.Contains(response.Body.String(), "ttl") {
		t.Fatalf("body = %s, want cache_control ttl validation error", response.Body.String())
	}
}

func TestHandlerAcceptsClaudeCodeHintsWithoutForwardingCrossProvider(t *testing.T) {
	t.Parallel()

	var gotPath string
	var gotRequest map[string]any
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-claude","model":"nvidia/test","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":1}}`))
	})
	handler := newMessagesTestHandler(t, upstream)

	body := `{
		"model":"nvidia/test",
		"max_tokens":32,
		"metadata":{"user_id":"opaque-claude-code-user"},
		"system":[
			{"type":"text","text":"system one"},
			{"type":"text","text":"system two","cache_control":{"type":"ephemeral","ttl":"1h"}}
		],
		"messages":[{"role":"user","content":[
			{"type":"text","text":"hello"},
			{"type":"text","text":" world","cache_control":{"type":"ephemeral","ttl":"5m"}}
		]}],
		"tools":[{"name":"Bash","description":"Run a shell command","input_schema":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]},"cache_control":{"type":"ephemeral"}}]
	}`
	request := httptest.NewRequest(http.MethodPost, "/v1/messages?beta=true", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer client-placeholder")
	request.Header.Set("anthropic-version", "2023-06-01")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("upstream path = %q, want /v1/chat/completions", gotPath)
	}
	encoded, err := json.Marshal(gotRequest)
	if err != nil {
		t.Fatalf("marshal translated request: %v", err)
	}
	translated := string(encoded)
	for _, forbidden := range []string{"opaque-claude-code-user", "metadata", "cache_control"} {
		if strings.Contains(translated, forbidden) {
			t.Fatalf("translated request leaked Anthropic-only hint %q: %s", forbidden, translated)
		}
	}
	if !strings.Contains(translated, `"name":"Bash"`) {
		t.Fatalf("translated request lost Bash tool: %s", translated)
	}
}
