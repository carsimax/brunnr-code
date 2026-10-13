package model

import (
	"slices"
	"testing"
)

func TestComponentsForPresetFullIsDevStackPlusPermissions(t *testing.T) {
	want := []ComponentID{ComponentEngram, ComponentSkills, ComponentPermission, ComponentPersona}
	if got := ComponentsForPreset(PresetFull); !slices.Equal(got, want) {
		t.Fatalf("ComponentsForPreset(%q) = %v, want %v", PresetFull, got, want)
	}
}

// TestComponentsForPresetAlwaysInstallsManagedPersona pins that persona is not
// a choice: every preset, custom included, installs the managed persona. The
// custom preset starts with only the mandatory components selected.
func TestComponentsForPresetAlwaysInstallsManagedPersona(t *testing.T) {
	for _, preset := range []PresetID{PresetFull, PresetEcosystemOnly, PresetMinimal, PresetCustom} {
		if got := ComponentsForPreset(preset); !slices.Contains(got, ComponentPersona) {
			t.Errorf("ComponentsForPreset(%q) = %v, want it to include %q", preset, got, ComponentPersona)
		}
	}
	if got, want := ComponentsForPreset(PresetCustom), []ComponentID{ComponentPersona}; !slices.Equal(got, want) {
		t.Errorf("ComponentsForPreset(%q) = %v, want %v", PresetCustom, got, want)
	}
}

func TestWithMandatoryComponentsAddsPersonaOnce(t *testing.T) {
	tests := []struct {
		name string
		in   []ComponentID
		want []ComponentID
	}{
		{name: "empty selection", in: nil, want: []ComponentID{ComponentPersona}},
		{name: "persona missing", in: []ComponentID{ComponentEngram}, want: []ComponentID{ComponentEngram, ComponentPersona}},
		{name: "persona present", in: []ComponentID{ComponentPersona, ComponentEngram}, want: []ComponentID{ComponentPersona, ComponentEngram}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := slices.Clone(tt.in)
			if got := WithMandatoryComponents(in); !slices.Equal(got, tt.want) {
				t.Fatalf("WithMandatoryComponents(%v) = %v, want %v", tt.in, got, tt.want)
			}
			if !slices.Equal(in, tt.in) {
				t.Fatalf("WithMandatoryComponents mutated its input: %v, want %v", in, tt.in)
			}
		})
	}
	if !IsMandatoryComponent(ComponentPersona) || IsMandatoryComponent(ComponentEngram) {
		t.Fatal("IsMandatoryComponent: want persona mandatory and engram optional")
	}
}
