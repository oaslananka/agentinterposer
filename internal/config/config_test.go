package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLoadParsesOrderedFallbackModels(t *testing.T) {
	t.Parallel()

	cfg, err := Load(func(key string) string {
		switch key {
		case "NVIDIA_API_KEY":
			return "test-token"
		case "AGENTINTERPOSER_FALLBACK_MODELS":
			return " meta/llama-3.2-11b-vision-instruct , nvidia/nemotron-3-super-120b-a12b , meta/llama-3.2-11b-vision-instruct "
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := []string{"meta/llama-3.2-11b-vision-instruct", "nvidia/nemotron-3-super-120b-a12b"}
	if !slices.Equal(cfg.FallbackModels, want) {
		t.Fatalf("FallbackModels = %#v, want %#v", cfg.FallbackModels, want)
	}
}

func TestLoadRejectsEmptyFallbackModelEntry(t *testing.T) {
	t.Parallel()

	_, err := Load(func(key string) string {
		switch key {
		case "NVIDIA_API_KEY":
			return "test-token"
		case "AGENTINTERPOSER_FALLBACK_MODELS":
			return "model-one,,model-two"
		default:
			return ""
		}
	})
	if err == nil {
		t.Fatal("Load() accepted an empty fallback model entry")
	}
	if !strings.Contains(err.Error(), "AGENTINTERPOSER_FALLBACK_MODELS") {
		t.Fatalf("error = %q, want fallback-model validation", err)
	}
}

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
