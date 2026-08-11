package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLoadParsesModelRoutesAndResolvesTokenEnv(t *testing.T) {
	t.Parallel()

	cfg, err := Load(func(key string) string {
		switch key {
		case "NVIDIA_API_KEY":
			return "test-token"
		case "AGENTINTERPOSER_MODEL_ROUTES":
			return `[{"model":"provider/routed-model","upstream_url":"https://alt.example.test/v1/","bearer_token_env":"ALT_PROVIDER_API_KEY"}]`
		case "ALT_PROVIDER_API_KEY":
			return "route-token"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.ModelRoutes) != 1 {
		t.Fatalf("ModelRoutes = %#v, want one route", cfg.ModelRoutes)
	}
	route := cfg.ModelRoutes[0]
	if route.Model != "provider/routed-model" {
		t.Fatalf("route.Model = %q", route.Model)
	}
	if route.UpstreamURL != "https://alt.example.test/v1" {
		t.Fatalf("route.UpstreamURL = %q", route.UpstreamURL)
	}
	if route.UpstreamBearerToken != "route-token" {
		t.Fatal("route token was not resolved from bearer_token_env")
	}
}

func TestLoadRejectsLiteralTokenInModelRoute(t *testing.T) {
	t.Parallel()

	_, err := Load(func(key string) string {
		switch key {
		case "NVIDIA_API_KEY":
			return "test-token"
		case "AGENTINTERPOSER_MODEL_ROUTES":
			return `[{"model":"provider/routed-model","upstream_url":"https://alt.example.test/v1","bearer_token_env":"ALT_PROVIDER_API_KEY","bearer_token":"must-not-be-accepted"}]`
		case "ALT_PROVIDER_API_KEY":
			return "route-token"
		default:
			return ""
		}
	})
	if err == nil {
		t.Fatal("Load() accepted a literal bearer token in model route JSON")
	}
	if strings.Contains(err.Error(), "must-not-be-accepted") {
		t.Fatal("model route error exposed a rejected token value")
	}
}

func TestLoadRejectsMissingModelRouteTokenEnvValue(t *testing.T) {
	t.Parallel()

	_, err := Load(func(key string) string {
		switch key {
		case "NVIDIA_API_KEY":
			return "test-token"
		case "AGENTINTERPOSER_MODEL_ROUTES":
			return `[{"model":"provider/routed-model","upstream_url":"https://alt.example.test/v1","bearer_token_env":"ALT_PROVIDER_API_KEY"}]`
		default:
			return ""
		}
	})
	if err == nil {
		t.Fatal("Load() accepted an unresolved model route token env")
	}
	if !strings.Contains(err.Error(), "ALT_PROVIDER_API_KEY") {
		t.Fatalf("error = %q, want missing env name", err)
	}
}

func TestLoadRejectsInvalidModelRouteTokenEnvName(t *testing.T) {
	t.Parallel()

	_, err := Load(func(key string) string {
		switch key {
		case "NVIDIA_API_KEY":
			return "test-token"
		case "AGENTINTERPOSER_MODEL_ROUTES":
			return `[{"model":"provider/routed-model","upstream_url":"https://alt.example.test/v1","bearer_token_env":"BAD-NAME"}]`
		default:
			return ""
		}
	})
	if err == nil {
		t.Fatal("Load() accepted an invalid model route token env name")
	}
	if !strings.Contains(err.Error(), "bearer_token_env") {
		t.Fatalf("error = %q, want bearer_token_env validation", err)
	}
}

func TestLoadRejectsDuplicateModelRoute(t *testing.T) {
	t.Parallel()

	_, err := Load(func(key string) string {
		switch key {
		case "NVIDIA_API_KEY":
			return "test-token"
		case "AGENTINTERPOSER_MODEL_ROUTES":
			return `[
				{"model":"provider/routed-model","upstream_url":"https://one.example.test/v1","bearer_token_env":"ALT_ONE_API_KEY"},
				{"model":"provider/routed-model","upstream_url":"https://two.example.test/v1","bearer_token_env":"ALT_TWO_API_KEY"}
			]`
		case "ALT_ONE_API_KEY", "ALT_TWO_API_KEY":
			return "route-token"
		default:
			return ""
		}
	})
	if err == nil {
		t.Fatal("Load() accepted duplicate model routes")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("error = %q, want duplicate model route error", err)
	}
}

func TestLoadRejectsInvalidModelRouteURL(t *testing.T) {
	t.Parallel()

	_, err := Load(func(key string) string {
		switch key {
		case "NVIDIA_API_KEY":
			return "test-token"
		case "AGENTINTERPOSER_MODEL_ROUTES":
			return `[{"model":"provider/routed-model","upstream_url":"file:///tmp/provider","bearer_token_env":"ALT_PROVIDER_API_KEY"}]`
		case "ALT_PROVIDER_API_KEY":
			return "route-token"
		default:
			return ""
		}
	})
	if err == nil {
		t.Fatal("Load() accepted a non-HTTP model route URL")
	}
}

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
