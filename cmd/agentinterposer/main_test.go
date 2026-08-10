package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/oaslananka/agentinterposer/internal/config"
)

func TestNewHandlerWiresApplicationConfig(t *testing.T) {
	t.Parallel()

	handler, err := newHandler(config.Config{
		UpstreamBearerToken: "test-token",
		MaxConcurrent:       2,
		MaxRetries:          1,
		MaxRequestBytes:     1 << 20,
	})
	if err != nil {
		t.Fatalf("newHandler() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}
