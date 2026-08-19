package compatibility

import "testing"

func TestLookupKnownNemotronProfile(t *testing.T) {
	t.Parallel()

	profile, ok := Lookup("nvidia/nemotron-3-super-120b-a12b")
	if !ok {
		t.Fatal("Lookup() did not return the certified Nemotron profile")
	}
	if profile.Model != "nvidia/nemotron-3-super-120b-a12b" {
		t.Fatalf("Model = %q", profile.Model)
	}
	for _, capability := range []Capability{CapabilityChatCompletions, CapabilityResponses, CapabilityToolCalling} {
		if !profile.Supports(capability) {
			t.Errorf("profile does not support %q", capability)
		}
	}
	if profile.Supports(CapabilityVisionInput) {
		t.Fatal("profile must not assert uncertified vision input support")
	}
}

func TestLookupCertifiedLlamaVisionProfile(t *testing.T) {
	t.Parallel()

	profile, ok := Lookup("meta/llama-3.2-11b-vision-instruct")
	if !ok {
		t.Fatal("Lookup() did not return the certified Llama vision profile")
	}
	if !profile.Supports(CapabilityChatCompletions) {
		t.Fatal("Llama vision profile must assert Chat Completions support")
	}
	if !profile.Supports(CapabilityVisionInput) {
		t.Fatal("Llama vision profile must assert certified vision input support")
	}
	if profile.Supports(CapabilityResponses) {
		t.Fatal("Llama vision profile must not assert uncertified Responses support")
	}
	if profile.Supports(CapabilityToolCalling) {
		t.Fatal("Llama vision profile must not assert uncertified tool-calling support")
	}
	if len(profile.Certifications) != 0 {
		t.Fatalf("Llama vision client certifications = %#v, want none", profile.Certifications)
	}
}

func TestLookupCertifiedNemotronNanoProfile(t *testing.T) {
	t.Parallel()

	profile, ok := Lookup("nvidia/nemotron-3-nano-30b-a3b")
	if !ok {
		t.Fatal("Lookup() did not return the certified Nemotron 3 Nano profile")
	}
	if profile.Model != "nvidia/nemotron-3-nano-30b-a3b" {
		t.Fatalf("Model = %q", profile.Model)
	}
	for _, capability := range []Capability{CapabilityChatCompletions, CapabilityResponses} {
		if !profile.Supports(capability) {
			t.Errorf("Nemotron 3 Nano profile does not support %q", capability)
		}
	}
	if profile.Supports(CapabilityToolCalling) {
		t.Fatal("Nemotron 3 Nano profile must not assert unreliable tool-calling support")
	}
	if profile.Supports(CapabilityVisionInput) {
		t.Fatal("Nemotron 3 Nano profile must not assert vision input support")
	}
	if len(profile.Certifications) != 0 {
		t.Fatalf("Nemotron 3 Nano client certifications = %#v, want none", profile.Certifications)
	}
}

func TestSelectModelCanUseNemotronNanoForResponses(t *testing.T) {
	t.Parallel()

	profile, ok := SelectModel([]string{"nvidia/nemotron-3-nano-30b-a3b"}, CapabilityResponses)
	if !ok {
		t.Fatal("SelectModel() did not select the certified Nemotron 3 Nano Responses profile")
	}
	if profile.Model != "nvidia/nemotron-3-nano-30b-a3b" {
		t.Fatalf("selected model = %q", profile.Model)
	}
}

func TestLookupCertifiedNemotronOmniVisionProfile(t *testing.T) {
	t.Parallel()

	profile, ok := Lookup("nvidia/nemotron-3-nano-omni-30b-a3b-reasoning")
	if !ok {
		t.Fatal("Lookup() did not return the certified Nemotron 3 Nano Omni profile")
	}
	if profile.Model != "nvidia/nemotron-3-nano-omni-30b-a3b-reasoning" {
		t.Fatalf("Model = %q", profile.Model)
	}
	for _, capability := range []Capability{CapabilityChatCompletions, CapabilityVisionInput} {
		if !profile.Supports(capability) {
			t.Errorf("Nemotron 3 Nano Omni profile does not support %q", capability)
		}
	}
	if profile.Supports(CapabilityResponses) {
		t.Fatal("Nemotron 3 Nano Omni profile must not assert unreliable Responses support")
	}
	if profile.Supports(CapabilityToolCalling) {
		t.Fatal("Nemotron 3 Nano Omni profile must not assert uncertified tool-calling support")
	}
	if len(profile.Certifications) != 0 {
		t.Fatalf("Nemotron 3 Nano Omni client certifications = %#v, want none", profile.Certifications)
	}
}

func TestSelectModelCanUseNemotronOmniForVision(t *testing.T) {
	t.Parallel()

	profile, ok := SelectModel([]string{"nvidia/nemotron-3-nano-omni-30b-a3b-reasoning"}, CapabilityChatCompletions, CapabilityVisionInput)
	if !ok {
		t.Fatal("SelectModel() did not select the certified Nemotron 3 Nano Omni vision profile")
	}
	if profile.Model != "nvidia/nemotron-3-nano-omni-30b-a3b-reasoning" {
		t.Fatalf("selected model = %q", profile.Model)
	}
}

func TestLookupCertifiedNemotron35LightningProfile(t *testing.T) {
	t.Parallel()

	profile, ok := Lookup("nvidia/nemotron-3.5-lightning-30b-a3b")
	if !ok {
		t.Fatal("Lookup() did not return the certified Nemotron 3.5 Lightning profile")
	}
	if !profile.Supports(CapabilityChatCompletions) {
		t.Fatal("Nemotron 3.5 Lightning profile must assert Chat Completions support")
	}
	for _, capability := range []Capability{CapabilityResponses, CapabilityToolCalling, CapabilityVisionInput} {
		if profile.Supports(capability) {
			t.Fatalf("Nemotron 3.5 Lightning profile must not assert uncertified %q support", capability)
		}
	}
	if len(profile.Certifications) != 0 {
		t.Fatalf("Nemotron 3.5 Lightning client certifications = %#v, want none", profile.Certifications)
	}
}

func TestLookupCertifiedNemotron3UltraProfile(t *testing.T) {
	t.Parallel()

	profile, ok := Lookup("nvidia/nemotron-3-ultra-550b-a55b")
	if !ok {
		t.Fatal("Lookup() did not return the certified Nemotron 3 Ultra profile")
	}
	for _, capability := range []Capability{CapabilityChatCompletions, CapabilityResponses, CapabilityToolCalling} {
		if !profile.Supports(capability) {
			t.Fatalf("Nemotron 3 Ultra profile must assert %q support", capability)
		}
	}
	if profile.Supports(CapabilityVisionInput) {
		t.Fatal("Nemotron 3 Ultra profile must not assert uncertified vision input support")
	}
	want := Certification{Client: "codex", Version: "0.148.0", Scenario: "single-tool"}
	if len(profile.Certifications) != 1 || profile.Certifications[0] != want {
		t.Fatalf("Nemotron 3 Ultra certifications = %#v, want %#v", profile.Certifications, []Certification{want})
	}
}

func TestLookupUnknownModelDoesNotAssumeCapabilities(t *testing.T) {
	t.Parallel()

	if profile, ok := Lookup("provider/unknown-model"); ok {
		t.Fatalf("Lookup() = %#v, true; want no asserted profile", profile)
	}
}

func TestKnownNemotronCertificationsMatchHostedSmokeContracts(t *testing.T) {
	t.Parallel()

	profile, ok := Lookup("nvidia/nemotron-3-super-120b-a12b")
	if !ok {
		t.Fatal("missing Nemotron profile")
	}

	want := map[Certification]bool{
		{Client: "codex", Version: "0.147.0", Scenario: "single-tool"}:                           false,
		{Client: "codex", Version: "0.147.0", Scenario: "dependent-tool-loop"}:                   false,
		{Client: "codex", Version: "0.147.0", Scenario: "dependent-three-tool-loop"}:             false,
		{Client: "codex", Version: "0.148.0", Scenario: "single-tool"}:                           false,
		{Client: "codex", Version: "0.148.0", Scenario: "dependent-tool-loop"}:                   false,
		{Client: "codex", Version: "0.148.0", Scenario: "dependent-three-tool-loop"}:             false,
		{Client: "codex", Version: "0.148.0", Scenario: "dependent-four-tool-loop"}:              false,
		{Client: "claude-code", Version: "2.1.226", Scenario: "single-tool"}:                     false,
		{Client: "claude-code", Version: "2.1.226", Scenario: "dependent-tool-loop"}:             false,
		{Client: "claude-code", Version: "2.1.226", Scenario: "error-recovery"}:                  false,
		{Client: "claude-code", Version: "2.1.233", Scenario: "single-tool"}:                     false,
		{Client: "claude-code", Version: "2.1.233", Scenario: "dependent-tool-loop"}:             false,
		{Client: "claude-code", Version: "2.1.233", Scenario: "error-recovery"}:                  false,
		{Client: "claude-code", Version: "2.1.235", Scenario: "single-tool"}:                     false,
		{Client: "claude-code", Version: "2.1.235", Scenario: "normal-mode-single-tool"}:         false,
		{Client: "claude-code", Version: "2.1.235", Scenario: "normal-mode-dependent-tool-loop"}: false,
		{Client: "claude-code", Version: "2.1.235", Scenario: "dependent-tool-loop"}:             false,
		{Client: "claude-code", Version: "2.1.235", Scenario: "dependent-three-tool-loop"}:       false,
		{Client: "claude-code", Version: "2.1.235", Scenario: "error-recovery"}:                  false,
		{Client: "opencode", Version: "1.18.18", Scenario: "dependent-tool-loop"}:                false,
	}
	for _, certification := range profile.Certifications {
		if _, exists := want[certification]; exists {
			want[certification] = true
		}
	}
	for certification, found := range want {
		if !found {
			t.Errorf("missing certification %#v", certification)
		}
	}
}

func TestSelectModelSkipsUnknownCandidatesAndUsesFirstCertifiedMatch(t *testing.T) {
	t.Parallel()

	profile, ok := SelectModel(
		[]string{"provider/unknown-model", "nvidia/nemotron-3-super-120b-a12b"},
		CapabilityResponses,
		CapabilityToolCalling,
	)
	if !ok {
		t.Fatal("SelectModel() did not find a certified matching candidate")
	}
	if profile.Model != "nvidia/nemotron-3-super-120b-a12b" {
		t.Fatalf("selected model = %q", profile.Model)
	}
}

func TestSelectModelDoesNotRouteToModelMissingRequiredCapability(t *testing.T) {
	t.Parallel()

	if profile, ok := SelectModel([]string{"nvidia/nemotron-3-super-120b-a12b"}, CapabilityVisionInput); ok {
		t.Fatalf("SelectModel() = %#v, true; want no certified vision candidate", profile)
	}
}

func TestSelectModelRequiresEveryRequestedCapability(t *testing.T) {
	t.Parallel()

	if profile, ok := SelectModel(
		[]string{"nvidia/nemotron-3-super-120b-a12b"},
		CapabilityChatCompletions,
		CapabilityVisionInput,
	); ok {
		t.Fatalf("SelectModel() = %#v, true; want no candidate satisfying every capability", profile)
	}
}

func TestProfileCapabilitiesReturnsSortedPositiveAssertions(t *testing.T) {
	t.Parallel()

	profile, ok := Lookup("nvidia/nemotron-3-super-120b-a12b")
	if !ok {
		t.Fatal("missing Nemotron profile")
	}
	got := profile.Capabilities()
	want := []Capability{CapabilityChatCompletions, CapabilityResponses, CapabilityToolCalling}
	if len(got) != len(want) {
		t.Fatalf("Capabilities() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Capabilities()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
