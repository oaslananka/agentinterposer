package agentconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const localClientPlaceholder = "agentinterposer-local-placeholder"

func RenderCodexConfig(model, gatewayURL string) (string, error) {
	model, baseURL, err := validateInputs(model, gatewayURL)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(`model = %s
model_provider = "agentinterposer"

[model_providers.agentinterposer]
name = "AgentInterposer"
base_url = %s
env_key = "AGENTINTERPOSER_CLIENT_KEY"
# export AGENTINTERPOSER_CLIENT_KEY='agentinterposer-local-placeholder'
wire_api = "responses"
request_max_retries = 0
stream_max_retries = 0
`, quoteTOMLBasicString(model), quoteTOMLBasicString(baseURL+"/v1")), nil
}

func RenderOpenCodeConfig(model, gatewayURL string) (string, error) {
	model, baseURL, err := validateInputs(model, gatewayURL)
	if err != nil {
		return "", err
	}

	document := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"provider": map[string]any{
			"agentinterposer": map[string]any{
				"npm":  "@ai-sdk/openai-compatible",
				"name": "AgentInterposer",
				"options": map[string]any{
					"baseURL": baseURL + "/v1",
					"apiKey":  "{env:AGENTINTERPOSER_CLIENT_KEY}",
				},
				"models": map[string]any{
					model: map[string]any{"name": model},
				},
			},
		},
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode OpenCode config: %w", err)
	}
	return string(encoded) + "\n", nil
}

func RenderContinueConfig(model, gatewayURL string) (string, error) {
	model, baseURL, err := validateInputs(model, gatewayURL)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(`name: %s
version: "0.0.1"
schema: v1

models:
  - name: %s
    provider: openai
    model: %s
    apiBase: %s
    apiKey: %s
    useResponsesApi: false
`, quoteYAMLString("AgentInterposer"), quoteYAMLString("AgentInterposer: "+model), quoteYAMLString(model), quoteYAMLString(baseURL+"/v1"), quoteYAMLString(localClientPlaceholder)), nil
}

func RenderClaudeCodeEnv(model, gatewayURL string) (string, error) {
	model, baseURL, err := validateInputs(model, gatewayURL)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("export ANTHROPIC_BASE_URL=%s\nexport ANTHROPIC_AUTH_TOKEN=%s\nexport ANTHROPIC_MODEL=%s\n",
		quoteShell(baseURL), quoteShell(localClientPlaceholder), quoteShell(model)), nil
}

func validateInputs(model, gatewayURL string) (string, string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", "", errors.New("model is required")
	}

	parsed, err := url.Parse(strings.TrimSpace(gatewayURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", "", errors.New("gateway URL must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", errors.New("gateway URL must not contain user info, query parameters, or a fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(parsed.Path, "/v1") {
		parsed.Path = strings.TrimSuffix(parsed.Path, "/v1")
	}
	return model, strings.TrimRight(parsed.String(), "/"), nil
}

func quoteYAMLString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func quoteTOMLBasicString(value string) string {
	var quoted strings.Builder
	quoted.Grow(len(value) + 2)
	quoted.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			quoted.WriteString(`\"`)
		case '\\':
			quoted.WriteString(`\\`)
		case '\b':
			quoted.WriteString(`\b`)
		case '\t':
			quoted.WriteString(`\t`)
		case '\n':
			quoted.WriteString(`\n`)
		case '\f':
			quoted.WriteString(`\f`)
		case '\r':
			quoted.WriteString(`\r`)
		default:
			if r <= 0x1f || r == 0x7f {
				fmt.Fprintf(&quoted, `\u%04X`, r)
				continue
			}
			quoted.WriteRune(r)
		}
	}
	quoted.WriteByte('"')
	return quoted.String()
}

func quoteShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
