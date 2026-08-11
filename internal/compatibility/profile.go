package compatibility

type Capability string

const (
	CapabilityChatCompletions Capability = "chat_completions"
	CapabilityResponses       Capability = "responses"
	CapabilityToolCalling     Capability = "tool_calling"
	CapabilityVisionInput     Capability = "vision_input"

	certifiedCodexClient       = "codex"
	certifiedCodexVersion      = "0.147.0"
	certifiedClaudeCodeClient  = "claude-code"
	certifiedClaudeCodeVersion = "2.1.226"
)

type Certification struct {
	Client   string
	Version  string
	Scenario string
}

type Profile struct {
	Model          string
	capabilities   map[Capability]bool
	Certifications []Certification
}

func (p Profile) Supports(capability Capability) bool {
	return p.capabilities[capability]
}

var builtinProfiles = map[string]Profile{
	"nvidia/nemotron-3-super-120b-a12b": {
		Model: "nvidia/nemotron-3-super-120b-a12b",
		capabilities: map[Capability]bool{
			CapabilityChatCompletions: true,
			CapabilityResponses:       true,
			CapabilityToolCalling:     true,
		},
		Certifications: []Certification{
			{Client: certifiedCodexClient, Version: certifiedCodexVersion, Scenario: "single-tool"},
			{Client: certifiedCodexClient, Version: certifiedCodexVersion, Scenario: "dependent-tool-loop"},
			{Client: certifiedClaudeCodeClient, Version: certifiedClaudeCodeVersion, Scenario: "single-tool"},
			{Client: certifiedClaudeCodeClient, Version: certifiedClaudeCodeVersion, Scenario: "dependent-tool-loop"},
			{Client: certifiedClaudeCodeClient, Version: certifiedClaudeCodeVersion, Scenario: "error-recovery"},
		},
	},
}

func Lookup(model string) (Profile, bool) {
	profile, ok := builtinProfiles[model]
	return profile, ok
}
