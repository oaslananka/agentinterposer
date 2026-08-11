package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestRunConfigCommandPrintsCodexConfigWithoutServerSecret(t *testing.T) {
	t.Parallel()

	var stdout, stderr strings.Builder
	handled, exitCode := runConfigCommand(
		[]string{"config", "codex", "nvidia/nemotron-3-super-120b-a12b"},
		&stdout,
		&stderr,
	)
	if !handled || exitCode != 0 {
		t.Fatalf("runConfigCommand() = handled:%v exit:%d stderr:%q", handled, exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), `base_url = "http://127.0.0.1:11435/v1"`) {
		t.Fatalf("stdout missing local Codex gateway config:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunConfigCommandPrintsClaudeCodeEnvWithCustomGateway(t *testing.T) {
	t.Parallel()

	var stdout, stderr strings.Builder
	handled, exitCode := runConfigCommand(
		[]string{"config", "claude-code", "nvidia/nemotron-3-super-120b-a12b", "https://gateway.example.test/agent"},
		&stdout,
		&stderr,
	)
	if !handled || exitCode != 0 {
		t.Fatalf("runConfigCommand() = handled:%v exit:%d stderr:%q", handled, exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), `export ANTHROPIC_BASE_URL='https://gateway.example.test/agent'`) {
		t.Fatalf("stdout missing custom Claude Code gateway config:\n%s", stdout.String())
	}
}

func TestRunConfigCommandPrintsOpenCodeConfig(t *testing.T) {
	t.Parallel()

	var stdout, stderr strings.Builder
	handled, exitCode := runConfigCommand(
		[]string{"config", "opencode", "nvidia/nemotron-3-super-120b-a12b"},
		&stdout,
		&stderr,
	)
	if !handled || exitCode != 0 {
		t.Fatalf("runConfigCommand() = handled:%v exit:%d stderr:%q", handled, exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"npm": "@ai-sdk/openai-compatible"`) {
		t.Fatalf("stdout missing OpenCode provider config:\n%s", stdout.String())
	}
}

func TestRunConfigCommandRejectsUnknownClient(t *testing.T) {
	t.Parallel()

	var stdout, stderr strings.Builder
	handled, exitCode := runConfigCommand([]string{"config", "unknown", "model"}, &stdout, &stderr)
	if !handled || exitCode != 2 {
		t.Fatalf("runConfigCommand() = handled:%v exit:%d", handled, exitCode)
	}
	if !strings.Contains(stderr.String(), "codex|claude-code|opencode") {
		t.Fatalf("stderr = %q, want supported client usage", stderr.String())
	}
}

func TestRunConfigCommandIgnoresNonConfigArguments(t *testing.T) {
	t.Parallel()

	var stdout, stderr strings.Builder
	handled, exitCode := runConfigCommand(nil, &stdout, &stderr)
	if handled || exitCode != 0 {
		t.Fatalf("runConfigCommand() = handled:%v exit:%d, want not handled", handled, exitCode)
	}
}
