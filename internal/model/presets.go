package model

// ComponentsForPreset returns the managed components implied by a preset. Every
// non-custom preset installs the managed (Neutral) persona.
func ComponentsForPreset(preset PresetID) []ComponentID {
	var components []ComponentID
	switch preset {
	case PresetMinimal:
		components = []ComponentID{ComponentEngram}
	case PresetEcosystemOnly:
		components = []ComponentID{ComponentEngram, ComponentSkills}
	case PresetCustom:
		return nil
	default: // full
		components = []ComponentID{
			ComponentEngram,
			ComponentSkills,
			ComponentPermission,
		}
	}
	return append(components, ComponentPersona)
}
