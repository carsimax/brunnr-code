package model

// mandatoryComponents are installed by every preset and every explicit
// component list. The managed (Principal Engineer) persona is the only persona
// and is not a user choice.
var mandatoryComponents = []ComponentID{ComponentPersona}

// IsMandatoryComponent reports whether component is always installed.
func IsMandatoryComponent(component ComponentID) bool {
	for _, mandatory := range mandatoryComponents {
		if mandatory == component {
			return true
		}
	}
	return false
}

// WithMandatoryComponents returns a copy of components with every mandatory
// component appended when missing. It is the single rule that keeps persona in
// each selection: presets, explicit CLI lists and restored install state.
func WithMandatoryComponents(components []ComponentID) []ComponentID {
	out := append([]ComponentID(nil), components...)
	for _, mandatory := range mandatoryComponents {
		present := false
		for _, component := range out {
			if component == mandatory {
				present = true
				break
			}
		}
		if !present {
			out = append(out, mandatory)
		}
	}
	return out
}

// ComponentsForPreset returns the managed components implied by a preset. Every
// preset, custom included, installs the mandatory components; custom starts
// with only those and lets the user add the optional ones.
func ComponentsForPreset(preset PresetID) []ComponentID {
	var components []ComponentID
	switch preset {
	case PresetMinimal:
		components = []ComponentID{ComponentEngram}
	case PresetEcosystemOnly:
		components = []ComponentID{ComponentEngram, ComponentSkills}
	case PresetCustom:
		components = nil
	default: // full
		components = []ComponentID{
			ComponentEngram,
			ComponentSkills,
			ComponentPermission,
		}
	}
	return WithMandatoryComponents(components)
}
