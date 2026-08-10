package config

import (
	"testing"
	"time"
)

func TestLoadUsesSafeLocalDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := Load(func(key string) string {
		if key == "NVIDIA_API_KEY" {
			return "test-token"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.ListenAddr != "127.0.0.1:11435" {
		t.Fatalf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.UpstreamURL != "https://integrate.api.nvidia.com" {
		t.Fatalf("UpstreamURL = %q", cfg.UpstreamURL)
	}
	if cfg.UpstreamBearerToken != "test-token" {
		t.Fatal("UpstreamBearerToken did not use NVIDIA_API_KEY")
	}
	if cfg.MaxConcurrent != 3 {
		t.Fatalf("MaxConcurrent = %d, want 3", cfg.MaxConcurrent)
	}
	if cfg.MaxRetries != 3 {
		t.Fatalf("MaxRetries = %d, want 3", cfg.MaxRetries)
	}
	if cfg.RetryBaseDelay != 500*time.Millisecond {
		t.Fatalf("RetryBaseDelay = %s, want 500ms", cfg.RetryBaseDelay)
	}
	if cfg.MaxRequestBytes != 32<<20 {
		t.Fatalf("MaxRequestBytes = %d, want %d", cfg.MaxRequestBytes, 32<<20)
	}
}
