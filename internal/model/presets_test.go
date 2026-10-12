package model

import (
	"slices"
	"testing"
)

func TestComponentsForPresetFullGentlemanUsesInstallSafeVisualInventory(t *testing.T) {
	got := ComponentsForPreset(PresetFullGentleman)

	if slices.Contains(got, ComponentTheme) {
		t.Fatalf("ComponentsForPreset() includes generic ComponentTheme: %v", got)
	}
	for _, want := range []ComponentID{ComponentClaudeTheme} {
		if !slices.Contains(got, want) {
			t.Errorf("ComponentsForPreset() missing safe visual component %q: %v", want, got)
		}
	}
}

// TestComponentsForPresetAlwaysInstallsNeutralPersona pins that persona is not
// a choice: every managed preset installs the (Neutral) persona component, and
// only the custom preset leaves component selection to the user.
func TestComponentsForPresetAlwaysInstallsNeutralPersona(t *testing.T) {
	for _, preset := range []PresetID{PresetFullGentleman, PresetEcosystemOnly, PresetMinimal} {
		if got := ComponentsForPreset(preset); !slices.Contains(got, ComponentPersona) {
			t.Errorf("ComponentsForPreset(%q) = %v, want it to include %q", preset, got, ComponentPersona)
		}
	}
	if got := ComponentsForPreset(PresetCustom); got != nil {
		t.Errorf("ComponentsForPreset(%q) = %v, want nil", PresetCustom, got)
	}
}

func TestVisualPolishComponentsReturnsCompleteManagedCleanupInventory(t *testing.T) {
	want := []ComponentID{ComponentTheme, ComponentClaudeTheme}
	if got := VisualPolishComponents(); !slices.Equal(got, want) {
		t.Fatalf("VisualPolishComponents() = %v, want complete cleanup inventory %v", got, want)
	}
}

func TestExistingPresetsDoNotImplyCommunityTools(t *testing.T) {
	for _, preset := range []PresetID{PresetFullGentleman, PresetEcosystemOnly, PresetMinimal, PresetCustom} {
		t.Run(string(preset), func(t *testing.T) {
			selection := Selection{
				Preset:     preset,
				Components: ComponentsForPreset(preset),
			}
			if len(selection.CommunityTools) != 0 {
				t.Fatalf("preset %q community tools = %v, want none", preset, selection.CommunityTools)
			}
		})
	}
}
