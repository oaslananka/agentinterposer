package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oaslananka/agentinterposer/internal/config"
)

func TestNewHandlerWiresModelRoutes(t *testing.T) {
	t.Parallel()

	var gotAuthorization string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-route","choices":[]}`))
	}))
	defer upstream.Close()

	handler, err := newHandler(config.Config{
		UpstreamURL:         "https://default.example.test",
		UpstreamBearerToken: "default-token",
		MaxConcurrent:       1,
		MaxRequestBytes:     1 << 20,
		ModelRoutes: []config.ModelRoute{{
			Model:               "provider/routed-model",
			UpstreamURL:         upstream.URL,
			UpstreamBearerToken: "route-token",
		}},
	})
	if err != nil {
		t.Fatalf("newHandler() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"provider/routed-model","messages":[{"role":"user","content":"hi"}]}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if gotAuthorization != "Bearer route-token" {
		t.Fatalf("Authorization = %q, want routed token", gotAuthorization)
	}
}

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

func TestRunCapabilitiesCommandPrintsCertifiedProfileJSON(t *testing.T) {
	t.Parallel()

	var stdout, stderr strings.Builder
	handled, exitCode := runCapabilitiesCommand(
		[]string{"capabilities", "nvidia/nemotron-3-super-120b-a12b"},
		&stdout,
		&stderr,
	)
	if !handled || exitCode != 0 {
		t.Fatalf("runCapabilitiesCommand() = handled:%v exit:%d stderr:%q", handled, exitCode, stderr.String())
	}
	var payload struct {
		Model          string   `json:"model"`
		Capabilities   []string `json:"capabilities"`
		Certifications []struct {
			Client   string `json:"client"`
			Version  string `json:"version"`
			Scenario string `json:"scenario"`
		} `json:"certifications"`
	}
	if err := json.Unmarshal([]byte(stdout.String()), &payload); err != nil {
		t.Fatalf("decode capabilities JSON: %v; output=%q", err, stdout.String())
	}
	if payload.Model != "nvidia/nemotron-3-super-120b-a12b" {
		t.Fatalf("model = %q", payload.Model)
	}
	if len(payload.Capabilities) != 3 {
		t.Fatalf("capabilities = %#v", payload.Capabilities)
	}
	if len(payload.Certifications) != 6 {
		t.Fatalf("certifications = %#v", payload.Certifications)
	}
}

func TestRunCapabilitiesCommandRejectsUnknownModel(t *testing.T) {
	t.Parallel()

	var stdout, stderr strings.Builder
	handled, exitCode := runCapabilitiesCommand([]string{"capabilities", "provider/unknown"}, &stdout, &stderr)
	if !handled || exitCode != 2 {
		t.Fatalf("runCapabilitiesCommand() = handled:%v exit:%d", handled, exitCode)
	}
	if !strings.Contains(stderr.String(), "no certified capability profile") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunMetaCommandPrintsHelpWithoutProviderCredential(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"--help"}, {"help"}} {
		var stdout, stderr strings.Builder
		handled, exitCode := runMetaCommand(args, &stdout, &stderr)
		if !handled || exitCode != 0 {
			t.Fatalf("runMetaCommand(%v) = handled:%v exit:%d stderr:%q", args, handled, exitCode, stderr.String())
		}
		if !strings.Contains(stdout.String(), "usage: agentinterposer") || !strings.Contains(stdout.String(), "capabilities") || !strings.Contains(stdout.String(), "config") {
			t.Fatalf("help output missing usage/subcommands:\n%s", stdout.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("stderr = %q", stderr.String())
		}
	}
}

func TestRunMetaCommandPrintsVersionWithoutProviderCredential(t *testing.T) {
	oldVersion, oldCommit := version, commit
	version, commit = "v1.2.3", "abc1234"
	t.Cleanup(func() {
		version, commit = oldVersion, oldCommit
	})

	for _, args := range [][]string{{"--version"}, {"version"}} {
		var stdout, stderr strings.Builder
		handled, exitCode := runMetaCommand(args, &stdout, &stderr)
		if !handled || exitCode != 0 {
			t.Fatalf("runMetaCommand(%v) = handled:%v exit:%d stderr:%q", args, handled, exitCode, stderr.String())
		}
		if got := stdout.String(); got != "agentinterposer v1.2.3 (abc1234)\n" {
			t.Fatalf("version output = %q", got)
		}
		if stderr.Len() != 0 {
			t.Fatalf("stderr = %q", stderr.String())
		}
	}
}

func TestRunMetaCommandIgnoresServerArguments(t *testing.T) {
	t.Parallel()

	var stdout, stderr strings.Builder
	handled, exitCode := runMetaCommand(nil, &stdout, &stderr)
	if handled || exitCode != 0 {
		t.Fatalf("runMetaCommand(nil) = handled:%v exit:%d, want not handled", handled, exitCode)
	}
}
