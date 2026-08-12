package compatibility

import "sort"

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
	Client   string `json:"client"`
	Version  string `json:"version"`
	Scenario string `json:"scenario"`
}

type Profile struct {
	Model          string
	capabilities   map[Capability]bool
	Certifications []Certification
}

func (p Profile) Supports(capability Capability) bool {
	return p.capabilities[capability]
}

func (p Profile) Capabilities() []Capability {
	result := make([]Capability, 0, len(p.capabilities))
	for capability, supported := range p.capabilities {
		if supported {
			result = append(result, capability)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

var builtinProfiles = map[string]Profile{
	"meta/llama-3.2-11b-vision-instruct": {
		Model: "meta/llama-3.2-11b-vision-instruct",
		capabilities: map[Capability]bool{
			CapabilityChatCompletions: true,
			CapabilityVisionInput:     true,
		},
		Certifications: []Certification{},
	},
	"nvidia/nemotron-3-nano-30b-a3b": {
		Model: "nvidia/nemotron-3-nano-30b-a3b",
		capabilities: map[Capability]bool{
			CapabilityChatCompletions: true,
			CapabilityResponses:       true,
		},
		Certifications: []Certification{},
	},
	"nvidia/nemotron-3-nano-omni-30b-a3b-reasoning": {
		Model: "nvidia/nemotron-3-nano-omni-30b-a3b-reasoning",
		capabilities: map[Capability]bool{
			CapabilityChatCompletions: true,
			CapabilityVisionInput:     true,
		},
		Certifications: []Certification{},
	},
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
			{Client: certifiedCodexClient, Version: certifiedCodexVersion, Scenario: "dependent-three-tool-loop"},
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

func SelectModel(candidates []string, required ...Capability) (Profile, bool) {
	for _, model := range candidates {
		profile, ok := Lookup(model)
		if !ok {
			continue
		}
		matched := true
		for _, capability := range required {
			if !profile.Supports(capability) {
				matched = false
				break
			}
		}
		if matched {
			return profile, true
		}
	}
	return Profile{}, false
}
