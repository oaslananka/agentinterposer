package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHandlerForwardsModelsRequestWithServerOwnedAuth(t *testing.T) {
	t.Parallel()

	var gotMethod, gotPath, gotAuth string
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var err error
		gotBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"nvidia/test","object":"model"}]}`))
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL + "/v1",
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       1,
		MaxRequestBytes:     1 << 20,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer client-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("upstream method = %q, want GET", gotMethod)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("upstream path = %q, want /v1/models", gotPath)
	}
	if gotAuth != "Bearer server-secret" {
		t.Fatalf("upstream Authorization = %q, want server-owned credential", gotAuth)
	}
	if len(gotBody) != 0 {
		t.Fatalf("upstream body = %q, want empty", gotBody)
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	const wantBody = `{"object":"list","data":[{"id":"nvidia/test","object":"model"}]}`
	if response.Body.String() != wantBody {
		t.Fatalf("body = %q, want %q", response.Body.String(), wantBody)
	}
}

func TestHandlerRetriesModelsRequestOnTransientUpstreamFailure(t *testing.T) {
	t.Parallel()

	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"temporarily unavailable"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		UpstreamURL:         upstream.URL,
		UpstreamBearerToken: "server-secret",
		MaxConcurrent:       1,
		MaxRetries:          1,
		RetryBaseDelay:      time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if calls != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls)
	}
	if response.Body.String() != `{"object":"list","data":[]}` {
		t.Fatalf("body = %q", response.Body.String())
	}
}
