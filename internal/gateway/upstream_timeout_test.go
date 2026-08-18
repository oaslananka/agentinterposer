package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHandlerTimesOutWaitingForUpstreamResponseHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:                   upstream.URL,
		UpstreamBearerToken:           "server-secret",
		MaxConcurrent:                 1,
		MaxRetries:                    0,
		MaxRequestBytes:               1 << 20,
		UpstreamResponseHeaderTimeout: 25 * time.Millisecond,
		UpstreamBodyIdleTimeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504; body=%s", response.Code, response.Body.String())
	}
}

func TestMessagesTimesOutWhenNonStreamingUpstreamBodyStopsMakingProgress(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-stalled"`))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:                   upstream.URL,
		UpstreamBearerToken:           "server-secret",
		MaxConcurrent:                 1,
		MaxRetries:                    0,
		MaxRequestBytes:               1 << 20,
		UpstreamResponseHeaderTimeout: time.Second,
		UpstreamBodyIdleTimeout:       25 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	response := serveMessages(handler, `{"model":"nvidia/test","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`)
	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504; body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if envelope.Error.Type != "timeout_error" {
		t.Fatalf("error.type = %q, want timeout_error", envelope.Error.Type)
	}
}

func TestMessagesStreamTimesOutBeforeMessageStartWhenUpstreamBodyIsIdle(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:                   upstream.URL,
		UpstreamBearerToken:           "server-secret",
		MaxConcurrent:                 1,
		MaxRetries:                    0,
		MaxRequestBytes:               1 << 20,
		UpstreamResponseHeaderTimeout: time.Second,
		UpstreamBodyIdleTimeout:       25 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	response := serveMessages(handler, `{"model":"nvidia/test","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504; body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"type":"timeout_error"`) {
		t.Fatalf("body = %s, want timeout_error", response.Body.String())
	}
}

func TestMessagesStreamUsesTimeoutErrorAfterMessageStartWhenUpstreamBodyBecomesIdle(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-idle\",\"model\":\"nvidia/test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"first\"},\"finish_reason\":null}]}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:                   upstream.URL,
		UpstreamBearerToken:           "server-secret",
		MaxConcurrent:                 1,
		MaxRetries:                    0,
		MaxRequestBytes:               1 << 20,
		UpstreamResponseHeaderTimeout: time.Second,
		UpstreamBodyIdleTimeout:       25 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	response := serveMessages(handler, `{"model":"nvidia/test","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "event: error") || !strings.Contains(response.Body.String(), `"type":"timeout_error"`) {
		t.Fatalf("body = %s, want SSE timeout_error", response.Body.String())
	}
}

func TestMessagesStreamKeepsActiveLongStreamAliveAcrossIdleTimeoutIntervals(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-active\",\"model\":\"nvidia/test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"one\"},\"finish_reason\":null}]}\n\n")
		flusher.Flush()
		time.Sleep(15 * time.Millisecond)
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-active\",\"model\":\"nvidia/test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"two\"},\"finish_reason\":null}]}\n\n")
		flusher.Flush()
		time.Sleep(15 * time.Millisecond)
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-active\",\"model\":\"nvidia/test\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:                   upstream.URL,
		UpstreamBearerToken:           "server-secret",
		MaxConcurrent:                 1,
		MaxRetries:                    0,
		MaxRequestBytes:               1 << 20,
		UpstreamResponseHeaderTimeout: time.Second,
		UpstreamBodyIdleTimeout:       25 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	response := serveMessages(handler, `{"model":"nvidia/test","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "event: error") {
		t.Fatalf("active stream unexpectedly timed out: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"stop_reason":"end_turn"`) {
		t.Fatalf("body missing successful terminal event: %s", response.Body.String())
	}
}

func TestRetryableUpstreamResponseDrainIsByteBounded(t *testing.T) {
	var attempts atomic.Int32
	firstCancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if attempt == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(strings.Repeat("x", 128<<10)))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-r.Context().Done()
			close(firstCancelled)
			return
		}
		_, _ = w.Write([]byte(`{"id":"chatcmpl-ok","choices":[]}`))
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:                   upstream.URL,
		UpstreamBearerToken:           "server-secret",
		MaxConcurrent:                 1,
		MaxRetries:                    1,
		RetryBaseDelay:                time.Millisecond,
		MaxRequestBytes:               1 << 20,
		UpstreamResponseHeaderTimeout: time.Second,
		UpstreamBodyIdleTimeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"nvidia/test","messages":[]}`)).WithContext(ctx)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; attempts=%d body=%s", response.Code, attempts.Load(), response.Body.String())
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
	select {
	case <-firstCancelled:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("first retryable response was not closed after bounded drain")
	}
}
