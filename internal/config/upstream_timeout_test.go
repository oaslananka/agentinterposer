package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadUsesBoundedUpstreamTimeoutDefaults(t *testing.T) {
	cfg, err := Load(func(key string) string {
		if key == "NVIDIA_API_KEY" {
			return "test-token"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.UpstreamResponseHeaderTimeout != 2*time.Minute {
		t.Fatalf("UpstreamResponseHeaderTimeout = %s, want 2m", cfg.UpstreamResponseHeaderTimeout)
	}
	if cfg.UpstreamBodyIdleTimeout != 2*time.Minute {
		t.Fatalf("UpstreamBodyIdleTimeout = %s, want 2m", cfg.UpstreamBodyIdleTimeout)
	}
}

func TestLoadParsesUpstreamTimeoutOverrides(t *testing.T) {
	cfg, err := Load(func(key string) string {
		switch key {
		case "NVIDIA_API_KEY":
			return "test-token"
		case "AGENTINTERPOSER_UPSTREAM_RESPONSE_HEADER_TIMEOUT":
			return "45s"
		case "AGENTINTERPOSER_UPSTREAM_BODY_IDLE_TIMEOUT":
			return "3m"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.UpstreamResponseHeaderTimeout != 45*time.Second {
		t.Fatalf("UpstreamResponseHeaderTimeout = %s, want 45s", cfg.UpstreamResponseHeaderTimeout)
	}
	if cfg.UpstreamBodyIdleTimeout != 3*time.Minute {
		t.Fatalf("UpstreamBodyIdleTimeout = %s, want 3m", cfg.UpstreamBodyIdleTimeout)
	}
}

func TestLoadRejectsInvalidUpstreamTimeoutOverrides(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"header invalid", "AGENTINTERPOSER_UPSTREAM_RESPONSE_HEADER_TIMEOUT", "soon"},
		{"header zero", "AGENTINTERPOSER_UPSTREAM_RESPONSE_HEADER_TIMEOUT", "0s"},
		{"body negative", "AGENTINTERPOSER_UPSTREAM_BODY_IDLE_TIMEOUT", "-1s"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Load(func(key string) string {
				if key == "NVIDIA_API_KEY" {
					return "test-token"
				}
				if key == test.key {
					return test.value
				}
				return ""
			})
			if err == nil {
				t.Fatal("Load() accepted invalid upstream timeout")
			}
			if !strings.Contains(err.Error(), test.key) {
				t.Fatalf("error = %q, want %s", err, test.key)
			}
		})
	}
}
