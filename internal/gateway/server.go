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

	"github.com/oaslananka/agentinterposer/internal/compatibility"
)

const (
	defaultMaxRequestBytes int64 = 32 << 20
	contentTypeHeader            = "Content-Type"
)

type ModelRoute struct {
	Model               string
	UpstreamURL         string
	UpstreamBearerToken string
}

type Config struct {
	UpstreamURL         string
	UpstreamBearerToken string
	MaxConcurrent       int
	MaxRetries          int
	RetryBaseDelay      time.Duration
	MaxRequestBytes     int64
	FallbackModels      []string
	ModelRoutes         []ModelRoute
}

type upstreamRoute struct {
	upstreamURL         string
	upstreamBearerToken string
}

type handler struct {
	upstreamURL         string
	upstreamBearerToken string
	maxRequestBytes     int64
	semaphore           chan struct{}
	client              *http.Client
	maxRetries          int
	retryBaseDelay      time.Duration
	fallbackModels      []string
	modelRoutes         map[string]upstreamRoute
}

func NewHandler(cfg Config) (http.Handler, error) {
	upstreamURL := strings.TrimRight(cfg.UpstreamURL, "/")
	if upstreamURL != "" {
		parsed, err := url.Parse(upstreamURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return nil, fmt.Errorf("invalid upstream URL %q", cfg.UpstreamURL)
		}
	}
	modelRoutes, err := normalizeModelRoutes(cfg.ModelRoutes)
	if err != nil {
		return nil, err
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
		fallbackModels:      append([]string(nil), cfg.FallbackModels...),
		modelRoutes:         modelRoutes,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.handleHealth)
	mux.HandleFunc("GET /v1/models", h.handleModels)
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) { h.handleProxy(w, r, "/chat/completions") })
	mux.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, r *http.Request) { h.handleProxy(w, r, "/responses") })
	mux.HandleFunc("POST /v1/messages", h.handleMessages)
	return mux, nil
}

func normalizeModelRouteURL(raw string) (string, error) {
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("upstream URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	return value, nil
}

func normalizeModelRoutes(routes []ModelRoute) (map[string]upstreamRoute, error) {
	if len(routes) == 0 {
		return nil, nil
	}
	result := make(map[string]upstreamRoute, len(routes))
	for _, route := range routes {
		model := strings.TrimSpace(route.Model)
		if model == "" {
			return nil, errors.New("model route model must be non-empty")
		}
		if _, duplicate := result[model]; duplicate {
			return nil, fmt.Errorf("duplicate model route for %q", model)
		}
		upstreamURL, err := normalizeModelRouteURL(route.UpstreamURL)
		if err != nil {
			return nil, fmt.Errorf("invalid upstream URL for model route %q", model)
		}
		if strings.TrimSpace(route.UpstreamBearerToken) == "" {
			return nil, fmt.Errorf("missing bearer token for model route %q", model)
		}
		result[model] = upstreamRoute{upstreamURL: upstreamURL, upstreamBearerToken: route.UpstreamBearerToken}
	}
	return result, nil
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

	switch upstreamPath {
	case "/chat/completions":
		body = h.routeChatCompletionBody(body)
	case "/responses":
		body = h.routeResponsesBody(body)
	}

	upstreamResponse, err := h.doUpstream(r, body, upstreamPath)
	writeUpstreamResponse(w, upstreamResponse, err)
}

type responsesRoutingRequest struct {
	Model string          `json:"model"`
	Input json.RawMessage `json:"input"`
	Tools json.RawMessage `json:"tools"`
}

func (h *handler) routeResponsesBody(body []byte) []byte {
	if len(h.fallbackModels) == 0 {
		return body
	}

	var request responsesRoutingRequest
	if err := json.Unmarshal(body, &request); err != nil || request.Model == "" {
		return body
	}
	if !responsesInputIsTextOnly(request.Input) {
		return body
	}
	if rawJSONHasValues(request.Tools) {
		return body
	}
	return h.routeModelBody(body, compatibility.CapabilityResponses)
}

type responsesRoutingInputItem struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type responsesRoutingContentPart struct {
	Type string `json:"type"`
}

func responsesInputIsTextOnly(raw json.RawMessage) bool {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return true
	}

	var items []responsesRoutingInputItem
	if err := json.Unmarshal(raw, &items); err != nil || len(items) == 0 {
		return false
	}
	for _, item := range items {
		if !responsesInputItemIsTextOnly(item) {
			return false
		}
	}
	return true
}

func responsesInputItemIsTextOnly(item responsesRoutingInputItem) bool {
	if item.Type != "" && item.Type != "message" {
		return false
	}
	if strings.TrimSpace(item.Role) == "" {
		return false
	}

	var text string
	if json.Unmarshal(item.Content, &text) == nil {
		return true
	}
	var parts []responsesRoutingContentPart
	if err := json.Unmarshal(item.Content, &parts); err != nil || len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if part.Type != "input_text" {
			return false
		}
	}
	return true
}

func rawJSONHasValues(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte("[]")) {
		return false
	}
	return true
}

type chatRoutingRequest struct {
	Model    string               `json:"model"`
	Messages []chatRoutingMessage `json:"messages"`
}

type chatRoutingMessage struct {
	Content json.RawMessage `json:"content"`
}

type chatRoutingContentPart struct {
	Type string `json:"type"`
}

func (h *handler) routeChatCompletionBody(body []byte) []byte {
	if len(h.fallbackModels) == 0 {
		return body
	}

	var request chatRoutingRequest
	if err := json.Unmarshal(body, &request); err != nil || request.Model == "" || !chatRoutingHasVisionInput(request.Messages) {
		return body
	}
	return h.routeModelBody(body, compatibility.CapabilityChatCompletions, compatibility.CapabilityVisionInput)
}

func (h *handler) routeModelBody(body []byte, required ...compatibility.Capability) []byte {
	if len(h.fallbackModels) == 0 {
		return body
	}

	var envelope struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Model == "" {
		return body
	}
	selected := h.selectCertifiedModel(envelope.Model, required...)
	if selected == envelope.Model {
		return body
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		return body
	}
	model, err := json.Marshal(selected)
	if err != nil {
		return body
	}
	object["model"] = model
	routed, err := json.Marshal(object)
	if err != nil {
		return body
	}
	return routed
}

func chatRoutingHasVisionInput(messages []chatRoutingMessage) bool {
	for _, message := range messages {
		var parts []chatRoutingContentPart
		if err := json.Unmarshal(message.Content, &parts); err != nil {
			continue
		}
		for _, part := range parts {
			if part.Type == "image_url" {
				return true
			}
		}
	}
	return false
}

func (h *handler) selectCertifiedModel(requested string, required ...compatibility.Capability) string {
	if len(h.fallbackModels) == 0 {
		return requested
	}
	profile, known := compatibility.Lookup(requested)
	if !known {
		return requested
	}
	for _, capability := range required {
		if !profile.Supports(capability) {
			fallback, ok := compatibility.SelectModel(h.fallbackModels, required...)
			if !ok {
				return requested
			}
			return fallback.Model
		}
	}
	return requested
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

	upstreamURL := h.upstreamURL
	upstreamBearerToken := h.upstreamBearerToken
	if route, ok := h.modelRouteForBody(body); ok {
		upstreamURL = route.upstreamURL
		upstreamBearerToken = route.upstreamBearerToken
	}

	request, err := http.NewRequestWithContext(r.Context(), method, upstreamEndpoint(upstreamURL, upstreamPath), requestBody)
	if err != nil {
		return nil, err
	}
	if method == http.MethodPost {
		request.Header.Set(contentTypeHeader, "application/json")
	}
	if upstreamBearerToken != "" {
		request.Header.Set("Authorization", "Bearer "+upstreamBearerToken)
	}
	return request, nil
}

func (h *handler) modelRouteForBody(body []byte) (upstreamRoute, bool) {
	if len(h.modelRoutes) == 0 || len(body) == 0 {
		return upstreamRoute{}, false
	}
	var envelope struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return upstreamRoute{}, false
	}
	route, ok := h.modelRoutes[envelope.Model]
	return route, ok
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
