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
		{Client: "codex", Version: "0.147.0", Scenario: "single-tool"}:               false,
		{Client: "codex", Version: "0.147.0", Scenario: "dependent-tool-loop"}:       false,
		{Client: "codex", Version: "0.147.0", Scenario: "dependent-three-tool-loop"}: false,
		{Client: "claude-code", Version: "2.1.226", Scenario: "single-tool"}:         false,
		{Client: "claude-code", Version: "2.1.226", Scenario: "dependent-tool-loop"}: false,
		{Client: "claude-code", Version: "2.1.226", Scenario: "error-recovery"}:      false,
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
