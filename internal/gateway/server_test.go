package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHandlerHealth(t *testing.T) {
	t.Parallel()

	handler, err := NewHandler(Config{})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}

	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status body = %q, want %q", body["status"], "ok")
	}
}

func TestHandlerForwardsChatCompletionWithServerOwnedAuthorization(t *testing.T) {
	t.Parallel()

	var gotAuthorization string
	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream body: %v", err)
		}
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","choices":[]}`))
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL,
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       2,
		MaxRetries:          0,
		MaxRequestBytes:     1 << 20,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"nvidia/test","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer client-token")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if gotAuthorization != "Bearer server-secret" {
		t.Fatalf("upstream authorization = %q, want server-owned bearer token", gotAuthorization)
	}
	if strings.Contains(gotAuthorization, "client-token") {
		t.Fatal("client authorization leaked upstream")
	}
	if gotBody != `{"model":"nvidia/test","messages":[{"role":"user","content":"hi"}]}` {
		t.Fatalf("upstream body = %q", gotBody)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q, want application/json", got)
	}
}

func TestHandlerRetriesTransientCapacityErrors(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if attempt < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"ResourceExhausted: Worker local total request limit reached (32/32)"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-ok","choices":[]}`))
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL,
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       1,
		MaxRetries:          2,
		RetryBaseDelay:      time.Millisecond,
		MaxRequestBytes:     1 << 20,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"nvidia/test","messages":[{"role":"user","content":"hi"}]}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("upstream attempts = %d, want 3", got)
	}
}

func TestHandlerLimitsConcurrentUpstreamRequests(t *testing.T) {
	t.Parallel()

	var active atomic.Int32
	var maxSeen atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		for {
			previous := maxSeen.Load()
			if current <= previous || maxSeen.CompareAndSwap(previous, current) {
				break
			}
		}
		defer active.Add(-1)
		time.Sleep(25 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL,
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       2,
		MaxRequestBytes:     1 << 20,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	const requests = 8
	var wg sync.WaitGroup
	wg.Add(requests)
	for range requests {
		go func() {
			defer wg.Done()
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"nvidia/test","messages":[{"role":"user","content":"hi"}]}`))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", response.Code, http.StatusOK)
			}
		}()
	}
	wg.Wait()

	if got := maxSeen.Load(); got > 2 {
		t.Fatalf("maximum concurrent upstream requests = %d, want <= 2", got)
	}
}
