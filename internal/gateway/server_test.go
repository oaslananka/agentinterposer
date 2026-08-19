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

func TestHandlerKeepsModelDiscoveryOnDefaultUpstream(t *testing.T) {
	t.Parallel()

	var defaultPath, defaultAuthorization string
	defaultUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defaultPath = r.URL.Path
		defaultAuthorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer defaultUpstream.Close()

	var routedHits atomic.Int32
	routedUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		routedHits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer routedUpstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         defaultUpstream.URL,
		UpstreamBearerToken: "default-token",
		MaxConcurrent:       1,
		ModelRoutes: []ModelRoute{{
			Model:               "provider/routed-model",
			UpstreamURL:         routedUpstream.URL,
			UpstreamBearerToken: "route-token",
		}},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if routedHits.Load() != 0 {
		t.Fatalf("routed upstream hits = %d, want 0", routedHits.Load())
	}
	if defaultPath != "/v1/models" {
		t.Fatalf("default path = %q, want /v1/models", defaultPath)
	}
	if defaultAuthorization != "Bearer default-token" {
		t.Fatalf("default Authorization = %q", defaultAuthorization)
	}
}

func TestHandlerRoutesExplicitModelToDedicatedUpstream(t *testing.T) {
	t.Parallel()

	var defaultHits atomic.Int32
	defaultUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		defaultHits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer defaultUpstream.Close()

	var gotBody, gotAuthorization, gotPath string
	routedUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read routed body: %v", err)
		}
		gotBody = string(body)
		gotAuthorization = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-routed","choices":[]}`))
	}))
	defer routedUpstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         defaultUpstream.URL,
		UpstreamBearerToken: "default-token",
		MaxConcurrent:       1,
		MaxRequestBytes:     1 << 20,
		ModelRoutes: []ModelRoute{{
			Model:               "provider/routed-model",
			UpstreamURL:         routedUpstream.URL + "/v1/",
			UpstreamBearerToken: "route-token",
		}},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	body := `{"model":"provider/routed-model","messages":[{"role":"user","content":"hi"}],"top_k":17}`
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if defaultHits.Load() != 0 {
		t.Fatalf("default upstream hits = %d, want 0", defaultHits.Load())
	}
	if gotBody != body {
		t.Fatalf("routed body = %q, want exact body %q", gotBody, body)
	}
	if gotAuthorization != "Bearer route-token" {
		t.Fatalf("routed Authorization = %q", gotAuthorization)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("routed path = %q, want /v1/chat/completions", gotPath)
	}
}

func TestHandlerUsesDefaultUpstreamForUnroutedModel(t *testing.T) {
	t.Parallel()

	var gotBody, gotAuthorization string
	defaultUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read default body: %v", err)
		}
		gotBody = string(body)
		gotAuthorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-default","choices":[]}`))
	}))
	defer defaultUpstream.Close()

	routedUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer routedUpstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         defaultUpstream.URL,
		UpstreamBearerToken: "default-token",
		MaxConcurrent:       1,
		MaxRequestBytes:     1 << 20,
		ModelRoutes: []ModelRoute{{
			Model:               "provider/routed-model",
			UpstreamURL:         routedUpstream.URL,
			UpstreamBearerToken: "route-token",
		}},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	body := `{"model":"provider/default-model","messages":[{"role":"user","content":"hi"}]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if gotBody != body {
		t.Fatalf("default body = %q, want exact body %q", gotBody, body)
	}
	if gotAuthorization != "Bearer default-token" {
		t.Fatalf("default Authorization = %q", gotAuthorization)
	}
}

func TestHandlerFallbackSelectedModelUsesDedicatedUpstream(t *testing.T) {
	t.Parallel()

	var defaultHits atomic.Int32
	defaultUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		defaultHits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer defaultUpstream.Close()

	var gotModel, gotAuthorization string
	routedUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode routed body: %v", err)
		}
		gotModel, _ = body["model"].(string)
		gotAuthorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-fallback-route","model":"meta/llama-3.2-11b-vision-instruct","choices":[]}`))
	}))
	defer routedUpstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         defaultUpstream.URL,
		UpstreamBearerToken: "default-token",
		MaxConcurrent:       1,
		MaxRequestBytes:     1 << 20,
		FallbackModels:      []string{"meta/llama-3.2-11b-vision-instruct"},
		ModelRoutes: []ModelRoute{{
			Model:               "meta/llama-3.2-11b-vision-instruct",
			UpstreamURL:         routedUpstream.URL,
			UpstreamBearerToken: "route-token",
		}},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	body := `{"model":"nvidia/nemotron-3-super-120b-a12b","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}}]}]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if defaultHits.Load() != 0 {
		t.Fatalf("default upstream hits = %d, want 0", defaultHits.Load())
	}
	if gotModel != "meta/llama-3.2-11b-vision-instruct" {
		t.Fatalf("routed model = %q, want fallback model", gotModel)
	}
	if gotAuthorization != "Bearer route-token" {
		t.Fatalf("routed Authorization = %q", gotAuthorization)
	}
}

func TestNewHandlerRejectsInvalidModelRoutes(t *testing.T) {
	t.Parallel()

	tests := map[string][]ModelRoute{
		"empty model":   {{Model: "", UpstreamURL: "https://route.example.test", UpstreamBearerToken: "route-token"}},
		"invalid URL":   {{Model: "provider/model", UpstreamURL: "file:///tmp/provider", UpstreamBearerToken: "route-token"}},
		"missing token": {{Model: "provider/model", UpstreamURL: "https://route.example.test", UpstreamBearerToken: ""}},
		"duplicate model": {
			{Model: "provider/model", UpstreamURL: "https://one.example.test", UpstreamBearerToken: "one-token"},
			{Model: "provider/model", UpstreamURL: "https://two.example.test", UpstreamBearerToken: "two-token"},
		},
	}
	for name, routes := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := NewHandler(Config{
				UpstreamURL:         "https://default.example.test",
				UpstreamBearerToken: "default-token",
				ModelRoutes:         routes,
			})
			if err == nil {
				t.Fatal("NewHandler() accepted an invalid model route")
			}
		})
	}
}

func TestHandlerRoutesChatVisionToCertifiedFallbackPreservingUnknownFields(t *testing.T) {
	t.Parallel()

	var got map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-fallback","model":"meta/llama-3.2-11b-vision-instruct","choices":[]}`))
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL,
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       1,
		MaxRequestBytes:     1 << 20,
		FallbackModels:      []string{"provider/unknown", "meta/llama-3.2-11b-vision-instruct"},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	body := `{"model":"nvidia/nemotron-3-super-120b-a12b","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}},{"type":"text","text":"Which side?"}]}],"top_k":17}`
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if got["model"] != "meta/llama-3.2-11b-vision-instruct" {
		t.Fatalf("upstream model = %#v, want certified vision fallback", got["model"])
	}
	if got["top_k"] != float64(17) {
		t.Fatalf("upstream top_k = %#v, want preserved provider-specific field", got["top_k"])
	}
}

func TestHandlerKeepsChatTextBodyExactWithFallbackConfigured(t *testing.T) {
	t.Parallel()

	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream body: %v", err)
		}
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-text","choices":[]}`))
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL,
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       1,
		MaxRequestBytes:     1 << 20,
		FallbackModels:      []string{"meta/llama-3.2-11b-vision-instruct"},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	body := `{"model":"nvidia/nemotron-3-super-120b-a12b","messages":[{"role":"user","content":"hi"}],"top_k":17}`
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if gotBody != body {
		t.Fatalf("upstream body = %q, want exact passthrough %q", gotBody, body)
	}
}

func TestHandlerKeepsUnknownChatVisionModelExact(t *testing.T) {
	t.Parallel()

	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream body: %v", err)
		}
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-unknown","choices":[]}`))
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL,
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       1,
		MaxRequestBytes:     1 << 20,
		FallbackModels:      []string{"meta/llama-3.2-11b-vision-instruct"},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	body := `{"model":"provider/new-vision-model","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}}]}]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if gotBody != body {
		t.Fatalf("upstream body = %q, want unknown requested model preserved", gotBody)
	}
}

func TestHandlerKeepsCertifiedChatVisionModelExact(t *testing.T) {
	t.Parallel()

	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream body: %v", err)
		}
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-certified","choices":[]}`))
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL,
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       1,
		MaxRequestBytes:     1 << 20,
		FallbackModels:      []string{"nvidia/nemotron-3-super-120b-a12b"},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	body := `{"model":"meta/llama-3.2-11b-vision-instruct","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}}]}]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if gotBody != body {
		t.Fatalf("upstream body = %q, want certified requested model exact", gotBody)
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

func TestHandlerFlushesStreamingResponses(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[]}\n\n"))
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

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"nvidia/test","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if !response.Flushed {
		t.Fatal("streaming response was not flushed")
	}
}

func TestHandlerRejectsOversizedRequestsBeforeCallingUpstream(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL,
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       1,
		MaxRequestBytes:     8,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("123456789"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("upstream calls = %d, want 0", got)
	}
}

func TestNewHandlerRejectsInvalidUpstreamURL(t *testing.T) {
	t.Parallel()

	_, err := NewHandler(Config{UpstreamURL: "://invalid"})
	if err == nil {
		t.Fatal("NewHandler() error = nil, want invalid upstream URL error")
	}
}

func TestHandlerAcceptsUpstreamBaseURLWithV1Suffix(t *testing.T) {
	t.Parallel()

	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL + "/v1",
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       1,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"test","messages":[]}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("upstream path = %q, want /v1/chat/completions", gotPath)
	}
}

func TestHandlerRoutesResponsesToCertifiedFallbackPreservingUnknownFields(t *testing.T) {
	t.Parallel()

	var got map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_fallback","object":"response","model":"nvidia/nemotron-3-super-120b-a12b","output":[]}`))
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL,
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       1,
		MaxRequestBytes:     1 << 20,
		FallbackModels:      []string{"provider/unknown", "nvidia/nemotron-3-super-120b-a12b"},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	body := `{"model":"meta/llama-3.2-11b-vision-instruct","input":"Reply OK","metadata":{"probe":"keep"},"stream":false}`
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if got["model"] != "nvidia/nemotron-3-super-120b-a12b" {
		t.Fatalf("upstream model = %#v, want certified Responses fallback", got["model"])
	}
	metadata, ok := got["metadata"].(map[string]any)
	if !ok || metadata["probe"] != "keep" {
		t.Fatalf("upstream metadata = %#v, want preserved unknown field", got["metadata"])
	}
}

func TestHandlerRoutesToolBearingResponsesToCertifiedFallback(t *testing.T) {
	t.Parallel()

	var got map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_tools","object":"response","output":[]}`))
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL,
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       1,
		MaxRequestBytes:     1 << 20,
		FallbackModels:      []string{"nvidia/nemotron-3-nano-30b-a3b", "nvidia/nemotron-3-super-120b-a12b"},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	body := `{"model":"meta/llama-3.2-11b-vision-instruct","input":"use a tool","tools":[{"type":"function","name":"probe","description":"probe","parameters":{"type":"object"}}],"metadata":{"keep":"yes"},"stream":false}`
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if got["model"] != "nvidia/nemotron-3-super-120b-a12b" {
		t.Fatalf("upstream model = %#v, want certified Responses+tool fallback", got["model"])
	}
	if got["metadata"].(map[string]any)["keep"] != "yes" {
		t.Fatalf("upstream metadata = %#v, want preserved unknown field", got["metadata"])
	}
	tools, ok := got["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("upstream tools = %#v, want preserved tool definition", got["tools"])
	}
}

func TestHandlerKeepsToolBearingResponsesExactWithoutCertifiedToolFallback(t *testing.T) {
	t.Parallel()

	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream body: %v", err)
		}
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_tools","object":"response","output":[]}`))
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL,
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       1,
		MaxRequestBytes:     1 << 20,
		FallbackModels:      []string{"nvidia/nemotron-3-nano-30b-a3b"},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	body := `{"model":"meta/llama-3.2-11b-vision-instruct","input":"use a tool","tools":[{"type":"function","name":"probe","parameters":{"type":"object"}}],"stream":false}`
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if gotBody != body {
		t.Fatalf("upstream body = %q, want exact passthrough without certified Responses+tool fallback", gotBody)
	}
}

func TestHandlerRoutesStructuredTextResponsesToCertifiedFallback(t *testing.T) {
	t.Parallel()

	var got map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_structured","object":"response","model":"nvidia/nemotron-3-super-120b-a12b","output":[]}`))
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL,
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       1,
		MaxRequestBytes:     1 << 20,
		FallbackModels:      []string{"nvidia/nemotron-3-super-120b-a12b"},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	body := `{"model":"meta/llama-3.2-11b-vision-instruct","input":[{"role":"system","content":[{"type":"input_text","text":"Be concise"}]},{"role":"user","content":[{"type":"input_text","text":"hi"}]}],"metadata":{"probe":"structured"},"stream":false}`
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if got["model"] != "nvidia/nemotron-3-super-120b-a12b" {
		t.Fatalf("upstream model = %#v, want certified Responses fallback", got["model"])
	}
	input, ok := got["input"].([]any)
	if !ok || len(input) != 2 {
		t.Fatalf("upstream input = %#v, want preserved structured text input", got["input"])
	}
	metadata, ok := got["metadata"].(map[string]any)
	if !ok || metadata["probe"] != "structured" {
		t.Fatalf("upstream metadata = %#v, want preserved metadata", got["metadata"])
	}
}

func TestHandlerKeepsImageResponsesInputExact(t *testing.T) {
	t.Parallel()

	assertResponsesFallbackPassthrough(t, `{"model":"meta/llama-3.2-11b-vision-instruct","input":[{"role":"user","content":[{"type":"input_text","text":"describe"},{"type":"input_image","image_url":"data:image/png;base64,iVBORw0KGgo="}]}],"stream":false}`)
}

func TestHandlerKeepsFileResponsesInputExact(t *testing.T) {
	t.Parallel()

	assertResponsesFallbackPassthrough(t, `{"model":"meta/llama-3.2-11b-vision-instruct","input":[{"role":"user","content":[{"type":"input_file","file_data":"ZmlsZQ==","filename":"probe.txt"}]}],"stream":false}`)
}

func assertResponsesFallbackPassthrough(t *testing.T, body string) {
	t.Helper()

	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream body: %v", err)
		}
		gotBody = string(upstreamBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_passthrough","object":"response","output":[]}`))
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL,
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       1,
		MaxRequestBytes:     1 << 20,
		FallbackModels:      []string{"nvidia/nemotron-3-super-120b-a12b"},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if gotBody != body {
		t.Fatalf("upstream body = %q, want exact passthrough %q", gotBody, body)
	}
}

func TestHandlerKeepsUnknownResponsesModelExactWithFallbackConfigured(t *testing.T) {
	t.Parallel()

	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream body: %v", err)
		}
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_unknown","object":"response","output":[]}`))
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL,
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       1,
		MaxRequestBytes:     1 << 20,
		FallbackModels:      []string{"nvidia/nemotron-3-super-120b-a12b"},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	body := `{"model":"provider/new-responses-model","input":"hi","stream":false}`
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if gotBody != body {
		t.Fatalf("upstream body = %q, want unknown requested model exact", gotBody)
	}
}

func TestHandlerKeepsCertifiedResponsesModelExact(t *testing.T) {
	t.Parallel()

	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream body: %v", err)
		}
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_certified","object":"response","output":[]}`))
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL,
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       1,
		MaxRequestBytes:     1 << 20,
		FallbackModels:      []string{"meta/llama-3.2-11b-vision-instruct"},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	body := `{"model":"nvidia/nemotron-3-super-120b-a12b","input":"hi","stream":false}`
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if gotBody != body {
		t.Fatalf("upstream body = %q, want certified requested model exact", gotBody)
	}
}

func TestHandlerForwardsResponsesRequestWithoutTranslation(t *testing.T) {
	t.Parallel()

	var gotPath string
	var gotAuthorization string
	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream body: %v", err)
		}
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resp_test","object":"response","output":[]}`))
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

	const body = `{"model":"nvidia/test","input":"Reply only with OK","stream":false}`
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer client-token")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if gotPath != "/v1/responses" {
		t.Fatalf("upstream path = %q, want /v1/responses", gotPath)
	}
	if gotAuthorization != "Bearer server-secret" {
		t.Fatalf("upstream authorization = %q, want server-owned bearer token", gotAuthorization)
	}
	if strings.Contains(gotAuthorization, "client-token") {
		t.Fatal("client authorization leaked upstream")
	}
	if gotBody != body {
		t.Fatalf("upstream body = %q, want exact passthrough", gotBody)
	}
}

func TestHandlerFlushesStreamingResponsesAPI(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("upstream path = %q, want /v1/responses", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"))
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

	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"nvidia/test","input":"hi","stream":true}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !response.Flushed {
		t.Fatal("Responses API stream was not flushed")
	}
}

func TestHandlerForwardsCodexResponsesFunctionOutputWithoutTranslation(t *testing.T) {
	t.Parallel()

	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream body: %v", err)
		}
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_codex","object":"response","output":[]}`))
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

	const body = `{"client_metadata":{"originator":"codex_cli_rs"},"include":["reasoning.encrypted_content"],"input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"Use tools when needed."}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"Read the proof file."}]},{"type":"function_call_output","call_id":"call_test","output":"proof-value"}],"instructions":"Coding agent instructions","model":"nvidia/nemotron-3-super-120b-a12b","parallel_tool_calls":false,"prompt_cache_key":"codex-test","reasoning":{"summary":"auto"},"store":false,"stream":true,"tool_choice":"auto","tools":[{"type":"function","name":"exec_command","description":"Run a command","parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}}]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer client-token")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if gotBody != body {
		t.Fatalf("Codex Responses body changed in transit\ngot:  %s\nwant: %s", gotBody, body)
	}
}

func TestHandlerPreservesCodexFunctionCallStreamingEvents(t *testing.T) {
	t.Parallel()

	const events = "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_test\",\"name\":\"exec_command\",\"arguments\":\"\"}}\n\n" +
		"event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"call_id\":\"call_test\",\"delta\":\"{\\\"cmd\\\":\\\"printf proof\\\"}\"}\n\n" +
		"event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"call_id\":\"call_test\",\"arguments\":\"{\\\"cmd\\\":\\\"printf proof\\\"}\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"object\":\"response\",\"output\":[]}}\n\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(events))
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

	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"nvidia/test","stream":true,"input":"use a tool"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !response.Flushed {
		t.Fatal("Codex function-call stream was not flushed")
	}
	if got := response.Body.String(); got != events {
		t.Fatalf("Codex function-call stream changed in transit\ngot:  %q\nwant: %q", got, events)
	}
}

func TestFallbackRoutingRequiresExactProtocolFieldNames(t *testing.T) {
	t.Parallel()

	h := &handler{fallbackModels: []string{
		"nvidia/nemotron-3-super-120b-a12b",
		"meta/llama-3.2-11b-vision-instruct",
	}}

	tests := []struct {
		name  string
		body  string
		route func([]byte) []byte
	}{
		{
			name:  "chat top-level model",
			body:  `{"modEl":"nvidia/nemotron-3-super-120b-a12b","messages":[{"role":"user","content":[{"type":"image_url"}]}]}`,
			route: h.routeChatCompletionBody,
		},
		{
			name:  "chat nested fields",
			body:  `{"model":"nvidia/nemotron-3-super-120b-a12b","messAges":[{"role":"user","Content":[{"tYpe":"image_url"}]}]}`,
			route: h.routeChatCompletionBody,
		},
		{
			name:  "responses top-level input",
			body:  `{"model":"meta/llama-3.2-11b-vision-instruct","Input":"hello"}`,
			route: h.routeResponsesBody,
		},
		{
			name:  "responses nested fields",
			body:  `{"model":"meta/llama-3.2-11b-vision-instruct","input":[{"Role":"user","content":[{"Type":"input_text","text":"hello"}]}]}`,
			route: h.routeResponsesBody,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.route([]byte(test.body))
			if string(got) != test.body {
				t.Fatalf("routing changed case-mismatched protocol JSON:\n got: %s\nwant: %s", got, test.body)
			}
		})
	}
}

func TestModelRouteRequiresExactModelFieldName(t *testing.T) {
	t.Parallel()

	h := &handler{modelRoutes: map[string]upstreamRoute{
		"nvidia/test": {upstreamURL: "https://route.example.test", upstreamBearerToken: "route-token"},
	}}

	if route, ok := h.modelRouteForBody([]byte(`{"model":"nvidia/test"}`)); !ok || route.upstreamURL != "https://route.example.test" {
		t.Fatalf("exact model field did not select configured route: route=%#v ok=%v", route, ok)
	}
	for _, body := range []string{
		`{"Model":"nvidia/test"}`,
		`{"MODEL":"nvidia/test"}`,
		`{"modEl":"nvidia/test"}`,
	} {
		if route, ok := h.modelRouteForBody([]byte(body)); ok {
			t.Fatalf("mis-cased model field unexpectedly selected route %#v: %s", route, body)
		}
	}
}
