package model

import "testing"

func TestKiroModelPresetsUseActiveRoles(t *testing.T) {
	presets := []struct {
		name   string
		values map[string]KiroModelAlias
	}{
		{"balanced", KiroModelPresetBalanced()},
		{"performance", KiroModelPresetPerformance()},
		{"economy", KiroModelPresetEconomy()},
		{"open-weight", KiroModelPresetOpenWeight()},
	}
	for _, preset := range presets {
		t.Run(preset.name, func(t *testing.T) {
			for _, role := range []string{"odd-explorer", "odd-worker", "odd-verify", "risk", "readability", "reliability", "resilience", "refuter", "validator", "jd-judge-a", "default"} {
				if !preset.values[role].Valid() {
					t.Errorf("missing or invalid model for %s", role)
				}
			}
			for role := range preset.values {
				if len(role) >= 4 && role[:4] == "sdd-" {
					t.Errorf("retired role %s in preset", role)
				}
			}
		})
	}
	if KiroModelPresetBalanced()["odd-explorer"] != KiroModelAuto || KiroModelPresetPerformance()["odd-explorer"] != KiroModelSonnet || KiroModelPresetEconomy()["odd-explorer"] != KiroModelQwen {
		t.Fatal("Kiro presets lost their distinct model strategies")
	}
}
