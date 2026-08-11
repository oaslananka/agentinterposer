package agentconfig

import (
	"strings"
	"testing"
)

const certifiedModel = "nvidia/nemotron-3-super-120b-a12b"

func TestRenderCodexConfigUsesLocalResponsesGateway(t *testing.T) {
	t.Parallel()

	got, err := RenderCodexConfig(certifiedModel, "http://127.0.0.1:11435")
	if err != nil {
		t.Fatalf("RenderCodexConfig() error = %v", err)
	}
	for _, want := range []string{
		`model = "nvidia/nemotron-3-super-120b-a12b"`,
		`model_provider = "agentinterposer"`,
		`[model_providers.agentinterposer]`,
		`base_url = "http://127.0.0.1:11435/v1"`,
		`env_key = "AGENTINTERPOSER_CLIENT_KEY"`,
		`# export AGENTINTERPOSER_CLIENT_KEY='agentinterposer-local-placeholder'`,
		`wire_api = "responses"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Codex config missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"NVIDIA_API_KEY", "DOPPLER_TOKEN", "sk-"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("Codex config contains forbidden secret-like value %q", forbidden)
		}
	}
}

func TestRenderClaudeCodeEnvUsesMessagesGateway(t *testing.T) {
	t.Parallel()

	got, err := RenderClaudeCodeEnv(certifiedModel, "http://127.0.0.1:11435")
	if err != nil {
		t.Fatalf("RenderClaudeCodeEnv() error = %v", err)
	}
	for _, want := range []string{
		`export ANTHROPIC_BASE_URL='http://127.0.0.1:11435'`,
		`export ANTHROPIC_AUTH_TOKEN='agentinterposer-local-placeholder'`,
		`export ANTHROPIC_MODEL='nvidia/nemotron-3-super-120b-a12b'`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Claude Code environment missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"NVIDIA_API_KEY", "DOPPLER_TOKEN", "sk-"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("Claude Code environment contains forbidden secret-like value %q", forbidden)
		}
	}
}

func TestRenderOpenCodeConfigUsesOpenAICompatibleProvider(t *testing.T) {
	t.Parallel()

	got, err := RenderOpenCodeConfig(certifiedModel, "http://127.0.0.1:11435")
	if err != nil {
		t.Fatalf("RenderOpenCodeConfig() error = %v", err)
	}
	for _, want := range []string{
		`"$schema": "https://opencode.ai/config.json"`,
		`"npm": "@ai-sdk/openai-compatible"`,
		`"baseURL": "http://127.0.0.1:11435/v1"`,
		`"apiKey": "{env:AGENTINTERPOSER_CLIENT_KEY}"`,
		`"nvidia/nemotron-3-super-120b-a12b"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("OpenCode config missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"NVIDIA_API_KEY", "DOPPLER_TOKEN", "sk-"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("OpenCode config contains forbidden secret-like value %q", forbidden)
		}
	}
}

func TestRenderAgentConfigRejectsNonHTTPGatewayURL(t *testing.T) {
	t.Parallel()

	for name, render := range map[string]func(string, string) (string, error){
		"codex":       RenderCodexConfig,
		"claude-code": RenderClaudeCodeEnv,
		"opencode":    RenderOpenCodeConfig,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := render(certifiedModel, "file:///tmp/socket"); err == nil {
				t.Fatal("renderer accepted a non-HTTP gateway URL")
			}
		})
	}
}
