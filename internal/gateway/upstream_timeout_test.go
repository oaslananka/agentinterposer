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

const (
	timeoutMessagesRequest       = `{"model":"nvidia/test","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`
	timeoutMessagesStreamRequest = `{"model":"nvidia/test","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
)

func newTimeoutTestHandler(t *testing.T, upstreamHandler http.Handler, headerTimeout, bodyIdleTimeout time.Duration, maxRetries int) http.Handler {
	t.Helper()
	upstream := httptest.NewServer(upstreamHandler)
	t.Cleanup(upstream.Close)

	cfg := Config{
		UpstreamURL:                   upstream.URL,
		UpstreamBearerToken:           "server-secret",
		MaxConcurrent:                 1,
		MaxRetries:                    maxRetries,
		MaxRequestBytes:               1 << 20,
		UpstreamResponseHeaderTimeout: headerTimeout,
		UpstreamBodyIdleTimeout:       bodyIdleTimeout,
	}
	if maxRetries > 0 {
		cfg.RetryBaseDelay = time.Millisecond
	}
	handler, err := NewHandler(cfg)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler
}

func stallingUpstream(contentType, initialBody string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		if initialBody != "" {
			_, _ = w.Write([]byte(initialBody))
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}
}

func TestHandlerTimesOutWaitingForUpstreamResponseHeaders(t *testing.T) {
	handler := newTimeoutTestHandler(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}), 25*time.Millisecond, time.Second, 0)

	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504; body=%s", response.Code, response.Body.String())
	}
}

func TestMessagesTimesOutWhenNonStreamingUpstreamBodyStopsMakingProgress(t *testing.T) {
	handler := newTimeoutTestHandler(t,
		stallingUpstream("application/json", `{"id":"chatcmpl-stalled"`),
		time.Second, 25*time.Millisecond, 0,
	)

	response := serveMessages(handler, timeoutMessagesRequest)
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
	handler := newTimeoutTestHandler(t,
		stallingUpstream("text/event-stream", ""),
		time.Second, 25*time.Millisecond, 0,
	)

	response := serveMessages(handler, timeoutMessagesStreamRequest)
	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504; body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"type":"timeout_error"`) {
		t.Fatalf("body = %s, want timeout_error", response.Body.String())
	}
}

func TestMessagesStreamUsesTimeoutErrorAfterMessageStartWhenUpstreamBodyBecomesIdle(t *testing.T) {
	firstChunk := "data: {\"id\":\"chatcmpl-idle\",\"model\":\"nvidia/test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"first\"},\"finish_reason\":null}]}\n\n"
	handler := newTimeoutTestHandler(t,
		stallingUpstream("text/event-stream", firstChunk),
		time.Second, 25*time.Millisecond, 0,
	)

	response := serveMessages(handler, timeoutMessagesStreamRequest)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "event: error") || !strings.Contains(response.Body.String(), `"type":"timeout_error"`) {
		t.Fatalf("body = %s, want SSE timeout_error", response.Body.String())
	}
}

func TestMessagesStreamKeepsActiveLongStreamAliveAcrossIdleTimeoutIntervals(t *testing.T) {
	activeStream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-active\",\"model\":\"nvidia/test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"one\"},\"finish_reason\":null}]}\n\n")
		flusher.Flush()
		time.Sleep(15 * time.Millisecond)
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-active\",\"model\":\"nvidia/test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"two\"},\"finish_reason\":null}]}\n\n")
		flusher.Flush()
		time.Sleep(15 * time.Millisecond)
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-active\",\"model\":\"nvidia/test\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		flusher.Flush()
	})
	handler := newTimeoutTestHandler(t, activeStream, time.Second, 25*time.Millisecond, 0)

	response := serveMessages(handler, timeoutMessagesStreamRequest)
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
	retryUpstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(strings.Repeat("x", 128<<10)))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(firstCancelled)
			return
		}
		_, _ = w.Write([]byte(`{"id":"chatcmpl-ok","choices":[]}`))
	})
	handler := newTimeoutTestHandler(t, retryUpstream, time.Second, time.Second, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"nvidia/test","messages":[]}`)).WithContext(ctx)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || attempts.Load() != 2 {
		t.Fatalf("status = %d attempts=%d, want 200/2; body=%s", response.Code, attempts.Load(), response.Body.String())
	}
	select {
	case <-firstCancelled:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("first retryable response was not closed after bounded drain")
	}
}
