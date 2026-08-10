package config

import (
	"errors"
	"os"
	"strconv"
	"time"
)

const (
	defaultListenAddr            = "127.0.0.1:11435"
	defaultUpstreamURL           = "https://integrate.api.nvidia.com"
	defaultMaxConcurrent         = 3
	defaultMaxRetries            = 3
	defaultRetryBaseDelay        = 500 * time.Millisecond
	defaultMaxRequestBytes int64 = 32 << 20
)

type Config struct {
	ListenAddr          string
	UpstreamURL         string
	UpstreamBearerToken string
	MaxConcurrent       int
	MaxRetries          int
	RetryBaseDelay      time.Duration
	MaxRequestBytes     int64
}

func LoadFromEnv() (Config, error) {
	return Load(os.Getenv)
}

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		ListenAddr:          valueOrDefault(getenv("AGENTINTERPOSER_ADDR"), defaultListenAddr),
		UpstreamURL:         valueOrDefault(getenv("AGENTINTERPOSER_UPSTREAM_URL"), defaultUpstreamURL),
		UpstreamBearerToken: valueOrDefault(getenv("AGENTINTERPOSER_UPSTREAM_BEARER_TOKEN"), getenv("NVIDIA_API_KEY")),
		MaxConcurrent:       defaultMaxConcurrent,
		MaxRetries:          defaultMaxRetries,
		RetryBaseDelay:      defaultRetryBaseDelay,
		MaxRequestBytes:     defaultMaxRequestBytes,
	}

	if cfg.UpstreamBearerToken == "" {
		return Config{}, errors.New("missing upstream bearer token: set NVIDIA_API_KEY or AGENTINTERPOSER_UPSTREAM_BEARER_TOKEN")
	}

	var err error
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

	return cfg, nil
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
