package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultListenAddr                          = "127.0.0.1:11435"
	defaultUpstreamURL                         = "https://integrate.api.nvidia.com"
	defaultMaxConcurrent                       = 3
	defaultMaxRetries                          = 3
	defaultRetryBaseDelay                      = 500 * time.Millisecond
	defaultMaxRequestBytes               int64 = 32 << 20
	defaultUpstreamResponseHeaderTimeout       = 2 * time.Minute
	defaultUpstreamBodyIdleTimeout             = 2 * time.Minute
)

type ModelRoute struct {
	Model               string
	UpstreamURL         string
	UpstreamBearerToken string
}

type Config struct {
	ListenAddr                    string
	UpstreamURL                   string
	UpstreamBearerToken           string
	MaxConcurrent                 int
	MaxRetries                    int
	RetryBaseDelay                time.Duration
	MaxRequestBytes               int64
	UpstreamResponseHeaderTimeout time.Duration
	UpstreamBodyIdleTimeout       time.Duration
	FallbackModels                []string
	ModelRoutes                   []ModelRoute
}

func LoadFromEnv() (Config, error) {
	return Load(os.Getenv)
}

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		ListenAddr:                    valueOrDefault(getenv("AGENTINTERPOSER_ADDR"), defaultListenAddr),
		UpstreamURL:                   valueOrDefault(getenv("AGENTINTERPOSER_UPSTREAM_URL"), defaultUpstreamURL),
		UpstreamBearerToken:           valueOrDefault(getenv("AGENTINTERPOSER_UPSTREAM_BEARER_TOKEN"), getenv("NVIDIA_API_KEY")),
		MaxConcurrent:                 defaultMaxConcurrent,
		MaxRetries:                    defaultMaxRetries,
		RetryBaseDelay:                defaultRetryBaseDelay,
		MaxRequestBytes:               defaultMaxRequestBytes,
		UpstreamResponseHeaderTimeout: defaultUpstreamResponseHeaderTimeout,
		UpstreamBodyIdleTimeout:       defaultUpstreamBodyIdleTimeout,
	}

	if cfg.UpstreamBearerToken == "" {
		return Config{}, errors.New("missing upstream bearer token: set NVIDIA_API_KEY or AGENTINTERPOSER_UPSTREAM_BEARER_TOKEN")
	}

	allowRemote, err := parseAllowRemote(getenv("AGENTINTERPOSER_ALLOW_REMOTE"))
	if err != nil {
		return Config{}, err
	}
	if err := validateListenAddr(cfg.ListenAddr, allowRemote); err != nil {
		return Config{}, err
	}

	if cfg.FallbackModels, err = fallbackModels(getenv("AGENTINTERPOSER_FALLBACK_MODELS")); err != nil {
		return Config{}, err
	}
	if cfg.ModelRoutes, err = parseModelRoutes(getenv("AGENTINTERPOSER_MODEL_ROUTES"), getenv); err != nil {
		return Config{}, err
	}
	if cfg.MaxConcurrent, err = positiveInt(getenv("AGENTINTERPOSER_MAX_CONCURRENT"), defaultMaxConcurrent); err != nil {
		return Config{}, err
	}
	if cfg.MaxRetries, err = nonNegativeInt(getenv("AGENTINTERPOSER_MAX_RETRIES"), defaultMaxRetries); err != nil {
		return Config{}, err
	}
	if cfg.MaxRequestBytes, err = positiveInt64(getenv("AGENTINTERPOSER_MAX_REQUEST_BYTES"), defaultMaxRequestBytes); err != nil {
		return Config{}, err
	}
	if raw := getenv("AGENTINTERPOSER_RETRY_BASE_DELAY"); raw != "" {
		cfg.RetryBaseDelay, err = time.ParseDuration(raw)
		if err != nil || cfg.RetryBaseDelay <= 0 {
			return Config{}, errors.New("AGENTINTERPOSER_RETRY_BASE_DELAY must be a positive duration")
		}
	}
	if cfg.UpstreamResponseHeaderTimeout, err = positiveDuration(
		getenv("AGENTINTERPOSER_UPSTREAM_RESPONSE_HEADER_TIMEOUT"),
		defaultUpstreamResponseHeaderTimeout,
		"AGENTINTERPOSER_UPSTREAM_RESPONSE_HEADER_TIMEOUT",
	); err != nil {
		return Config{}, err
	}
	if cfg.UpstreamBodyIdleTimeout, err = positiveDuration(
		getenv("AGENTINTERPOSER_UPSTREAM_BODY_IDLE_TIMEOUT"),
		defaultUpstreamBodyIdleTimeout,
		"AGENTINTERPOSER_UPSTREAM_BODY_IDLE_TIMEOUT",
	); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func parseAllowRemote(raw string) (bool, error) {
	if strings.TrimSpace(raw) == "" {
		return false, nil
	}
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, errors.New("AGENTINTERPOSER_ALLOW_REMOTE must be a boolean")
	}
	return value, nil
}

func validateListenAddr(addr string, allowRemote bool) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("AGENTINTERPOSER_ADDR must be a host:port listen address: %w", err)
	}
	if allowRemote {
		return nil
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return errors.New("AGENTINTERPOSER_ADDR must use a loopback host unless AGENTINTERPOSER_ALLOW_REMOTE=true")
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func positiveInt(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, errors.New("AGENTINTERPOSER_MAX_CONCURRENT must be a positive integer")
	}
	return value, nil
}

func nonNegativeInt(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, errors.New("AGENTINTERPOSER_MAX_RETRIES must be a non-negative integer")
	}
	return value, nil
}

func positiveDuration(raw string, fallback time.Duration, name string) (time.Duration, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return value, nil
}

func positiveInt64(raw string, fallback int64) (int64, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, errors.New("AGENTINTERPOSER_MAX_REQUEST_BYTES must be a positive integer")
	}
	return value, nil
}

func fallbackModels(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	parts := strings.Split(raw, ",")
	models := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		model := strings.TrimSpace(part)
		if model == "" {
			return nil, errors.New("AGENTINTERPOSER_FALLBACK_MODELS must be a comma-separated list of non-empty model IDs")
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}
	return models, nil
}

type modelRouteConfig struct {
	Model          string `json:"model"`
	UpstreamURL    string `json:"upstream_url"`
	BearerTokenEnv string `json:"bearer_token_env"`
}

func parseModelRoutes(raw string, getenv func(string) string) ([]ModelRoute, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var configured []modelRouteConfig
	if err := decoder.Decode(&configured); err != nil {
		return nil, fmt.Errorf("AGENTINTERPOSER_MODEL_ROUTES must be a JSON array of model routes: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("AGENTINTERPOSER_MODEL_ROUTES must contain exactly one JSON array")
	}

	routes := make([]ModelRoute, 0, len(configured))
	seen := make(map[string]struct{}, len(configured))
	for _, item := range configured {
		model := strings.TrimSpace(item.Model)
		if model == "" {
			return nil, errors.New("AGENTINTERPOSER_MODEL_ROUTES model must be non-empty")
		}
		if _, duplicate := seen[model]; duplicate {
			return nil, fmt.Errorf("AGENTINTERPOSER_MODEL_ROUTES contains duplicate model %q", model)
		}
		seen[model] = struct{}{}

		upstreamURL, err := normalizedHTTPURL(item.UpstreamURL)
		if err != nil {
			return nil, fmt.Errorf("AGENTINTERPOSER_MODEL_ROUTES model %q has invalid upstream_url", model)
		}
		envName := strings.TrimSpace(item.BearerTokenEnv)
		if !validEnvironmentName(envName) {
			return nil, fmt.Errorf("AGENTINTERPOSER_MODEL_ROUTES model %q has invalid bearer_token_env", model)
		}
		token := getenv(envName)
		if strings.TrimSpace(token) == "" {
			return nil, fmt.Errorf("AGENTINTERPOSER_MODEL_ROUTES bearer token env %s is not set", envName)
		}
		routes = append(routes, ModelRoute{
			Model:               model,
			UpstreamURL:         upstreamURL,
			UpstreamBearerToken: token,
		})
	}
	return routes, nil
}

func normalizedHTTPURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	return strings.TrimRight(value, "/"), nil
}

func validEnvironmentName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if i == 0 && !environmentNameStart(r) {
			return false
		}
		if i > 0 && !environmentNamePart(r) {
			return false
		}
	}
	return true
}

func environmentNameStart(r rune) bool {
	return r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z'
}

func environmentNamePart(r rune) bool {
	return environmentNameStart(r) || r >= '0' && r <= '9'
}
