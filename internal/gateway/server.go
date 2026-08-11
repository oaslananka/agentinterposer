package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultMaxRequestBytes int64 = 32 << 20
	contentTypeHeader            = "Content-Type"
)

type Config struct {
	UpstreamURL         string
	UpstreamBearerToken string
	MaxConcurrent       int
	MaxRetries          int
	RetryBaseDelay      time.Duration
	MaxRequestBytes     int64
}

type handler struct {
	upstreamURL         string
	upstreamBearerToken string
	maxRequestBytes     int64
	semaphore           chan struct{}
	client              *http.Client
	maxRetries          int
	retryBaseDelay      time.Duration
}

func NewHandler(cfg Config) (http.Handler, error) {
	upstreamURL := strings.TrimRight(cfg.UpstreamURL, "/")
	if upstreamURL != "" {
		parsed, err := url.Parse(upstreamURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return nil, fmt.Errorf("invalid upstream URL %q", cfg.UpstreamURL)
		}
	}

	maxConcurrent := cfg.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}
	maxRequestBytes := cfg.MaxRequestBytes
	if maxRequestBytes <= 0 {
		maxRequestBytes = defaultMaxRequestBytes
	}
	retryBaseDelay := cfg.RetryBaseDelay
	if retryBaseDelay <= 0 {
		retryBaseDelay = 250 * time.Millisecond
	}

	h := &handler{
		upstreamURL:         upstreamURL,
		upstreamBearerToken: cfg.UpstreamBearerToken,
		maxRequestBytes:     maxRequestBytes,
		semaphore:           make(chan struct{}, maxConcurrent),
		client:              &http.Client{},
		maxRetries:          max(cfg.MaxRetries, 0),
		retryBaseDelay:      retryBaseDelay,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.handleHealth)
	mux.HandleFunc("GET /v1/models", h.handleModels)
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) { h.handleProxy(w, r, "/chat/completions") })
	mux.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, r *http.Request) { h.handleProxy(w, r, "/responses") })
	mux.HandleFunc("POST /v1/messages", h.handleMessages)
	return mux, nil
}

func (h *handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *handler) handleModels(w http.ResponseWriter, r *http.Request) {
	if h.upstreamURL == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "upstream is not configured"})
		return
	}

	select {
	case h.semaphore <- struct{}{}:
		defer func() { <-h.semaphore }()
	case <-r.Context().Done():
		writeJSON(w, http.StatusRequestTimeout, map[string]string{"error": "request cancelled while waiting for upstream capacity"})
		return
	}

	upstreamResponse, err := h.doUpstreamMethod(r, http.MethodGet, nil, "/models")
	writeUpstreamResponse(w, upstreamResponse, err)
}

func (h *handler) handleProxy(w http.ResponseWriter, r *http.Request, upstreamPath string) {
	if h.upstreamURL == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "upstream is not configured"})
		return
	}

	select {
	case h.semaphore <- struct{}{}:
		defer func() { <-h.semaphore }()
	case <-r.Context().Done():
		writeJSON(w, http.StatusRequestTimeout, map[string]string{"error": "request cancelled while waiting for upstream capacity"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.maxRequestBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unable to read request body"})
		return
	}

	upstreamResponse, err := h.doUpstream(r, body, upstreamPath)
	writeUpstreamResponse(w, upstreamResponse, err)
}

func writeUpstreamResponse(w http.ResponseWriter, response *http.Response, err error) {
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "upstream request failed"})
		return
	}
	defer response.Body.Close()

	copyHeader(w.Header(), response.Header, contentTypeHeader)
	copyHeader(w.Header(), response.Header, "Cache-Control")
	copyHeader(w.Header(), response.Header, "Retry-After")
	w.WriteHeader(response.StatusCode)
	_ = copyResponseBody(w, response.Body, response.Header.Get(contentTypeHeader))
}

func copyResponseBody(w http.ResponseWriter, body io.Reader, contentType string) error {
	if !strings.HasPrefix(strings.ToLower(contentType), "text/event-stream") {
		_, err := io.Copy(w, body)
		return err
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		_, err := io.Copy(w, body)
		return err
	}

	buffer := make([]byte, 32*1024)
	for {
		n, readErr := body.Read(buffer)
		if n > 0 {
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return writeErr
			}
			flusher.Flush()
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}

func (h *handler) doUpstream(r *http.Request, body []byte, upstreamPath string) (*http.Response, error) {
	return h.doUpstreamMethod(r, http.MethodPost, body, upstreamPath)
}

func (h *handler) doUpstreamMethod(r *http.Request, method string, body []byte, upstreamPath string) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		upstreamRequest, err := h.newUpstreamRequest(r, method, body, upstreamPath)
		if err != nil {
			return nil, err
		}

		response, err := h.client.Do(upstreamRequest)
		if err == nil && (!retryableStatus(response.StatusCode) || attempt >= h.maxRetries) {
			return response, nil
		}
		if err != nil && attempt >= h.maxRetries {
			return nil, err
		}
		if response != nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}

		if err := waitForRetry(r, h.retryBaseDelay*time.Duration(1<<attempt)); err != nil {
			return nil, err
		}
	}
}

func (h *handler) newUpstreamRequest(r *http.Request, method string, body []byte, upstreamPath string) (*http.Request, error) {
	var requestBody io.Reader
	if method == http.MethodPost || len(body) > 0 {
		requestBody = bytes.NewReader(body)
	}

	request, err := http.NewRequestWithContext(r.Context(), method, upstreamEndpoint(h.upstreamURL, upstreamPath), requestBody)
	if err != nil {
		return nil, err
	}
	if method == http.MethodPost {
		request.Header.Set(contentTypeHeader, "application/json")
	}
	if h.upstreamBearerToken != "" {
		request.Header.Set("Authorization", "Bearer "+h.upstreamBearerToken)
	}
	return request, nil
}

func upstreamEndpoint(baseURL, path string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + path
	}
	return baseURL + "/v1" + path
}

func waitForRetry(r *http.Request, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-r.Context().Done():
		return r.Context().Err()
	}
}

func retryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func copyHeader(dst, src http.Header, key string) {
	if value := src.Get(key); value != "" {
		dst.Set(key, value)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set(contentTypeHeader, "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
