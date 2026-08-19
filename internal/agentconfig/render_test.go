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
		`export CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS='1'`,
		`export CLAUDE_CODE_DISABLE_THINKING='1'`,
		`export CLAUDE_CODE_EFFORT_LEVEL='auto'`,
		`export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC='1'`,
		`export CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK='1'`,
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

func TestRenderContinueConfigUsesOpenAICompatibleGateway(t *testing.T) {
	t.Parallel()

	got, err := RenderContinueConfig(certifiedModel, "http://127.0.0.1:11435")
	if err != nil {
		t.Fatalf("RenderContinueConfig() error = %v", err)
	}
	for _, want := range []string{
		`name: "AgentInterposer"`,
		`schema: v1`,
		`provider: openai`,
		`model: "nvidia/nemotron-3-super-120b-a12b"`,
		`apiBase: "http://127.0.0.1:11435/v1"`,
		`apiKey: "agentinterposer-local-placeholder"`,
		`useResponsesApi: false`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Continue config missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"NVIDIA_API_KEY", "DOPPLER_TOKEN", "sk-"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("Continue config contains forbidden secret-like value %q", forbidden)
		}
	}
}

func TestRenderContinueConfigEscapesYAMLControlCharacters(t *testing.T) {
	t.Parallel()

	model := "provider/model\"\nroles:\n  - embed"
	got, err := RenderContinueConfig(model, "http://127.0.0.1:11435")
	if err != nil {
		t.Fatalf("RenderContinueConfig() error = %v", err)
	}
	if strings.Contains(got, "\nroles:\n") {
		t.Fatalf("Continue config allowed model text to inject YAML structure: %q", got)
	}
	if !strings.Contains(got, `model: "provider/model\"\nroles:\n  - embed"`) {
		t.Fatalf("Continue config did not safely quote model text: %q", got)
	}
}

func TestRenderAgentConfigRejectsNonHTTPGatewayURL(t *testing.T) {
	t.Parallel()

	for name, render := range map[string]func(string, string) (string, error){
		"codex":       RenderCodexConfig,
		"claude-code": RenderClaudeCodeEnv,
		"opencode":    RenderOpenCodeConfig,
		"continue":    RenderContinueConfig,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := render(certifiedModel, "file:///tmp/socket"); err == nil {
				t.Fatal("renderer accepted a non-HTTP gateway URL")
			}
		})
	}
}

func TestRenderCodexConfigEscapesTOMLControlCharacters(t *testing.T) {
	t.Parallel()

	got, err := RenderCodexConfig("provider/model\x7fvariant", "http://127.0.0.1:11435")
	if err != nil {
		t.Fatalf("RenderCodexConfig() error = %v", err)
	}
	if strings.ContainsRune(got, '\x7f') {
		t.Fatalf("Codex config contains an unescaped TOML control character: %q", got)
	}
	if !strings.Contains(got, `model = "provider/model\u007Fvariant"`) {
		t.Fatalf("Codex config does not contain TOML-safe escaped model: %q", got)
	}
}
