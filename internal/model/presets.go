package model

// VisualPolishComponents returns the complete managed visual-polish inventory.
// Cleanup flows use this inventory, which intentionally includes the generic theme
// even though presets do not install it.
func VisualPolishComponents() []ComponentID {
	return []ComponentID{ComponentTheme, ComponentClaudeTheme}
}

// installSafePresetVisualComponents returns only the agent-specific visual
// components that presets can install without overwriting a generic theme.
func installSafePresetVisualComponents() []ComponentID {
	return []ComponentID{ComponentClaudeTheme}
}

// ComponentsForPreset returns the managed components implied by a preset. Every
// non-custom preset installs the managed (Neutral) persona.
func ComponentsForPreset(preset PresetID) []ComponentID {
	var components []ComponentID
	switch preset {
	case PresetMinimal:
		components = []ComponentID{ComponentEngram}
	case PresetEcosystemOnly:
		components = []ComponentID{ComponentEngram, ComponentSkills, ComponentContext7}
	case PresetCustom:
		return nil
	default: // full
		components = []ComponentID{
			ComponentEngram,
			ComponentSkills,
			ComponentContext7,
			ComponentPermission,
		}
		components = append(components, installSafePresetVisualComponents()...)
	}
	return append(components, ComponentPersona)
}
