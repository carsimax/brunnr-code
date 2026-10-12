package model_test

import (
	"testing"

	"github.com/carsimax/brunnr-code/internal/model"
)

// TestClaudeModelAliasValid verifies that Valid accepts exactly the four
// known aliases and rejects empty, unknown, uppercase, and full-model-ID inputs.
func TestClaudeModelAliasValid(t *testing.T) {
	tests := []struct {
		name  string
		input model.ClaudeModelAlias
		want  bool
	}{
		{"fable", model.ClaudeModelFable, true},
		{"opus", model.ClaudeModelOpus, true},
		{"sonnet", model.ClaudeModelSonnet, true},
		{"haiku", model.ClaudeModelHaiku, true},
		{"empty", model.ClaudeModelAlias(""), false},
		{"junk", model.ClaudeModelAlias("junk"), false},
		{"uppercase", model.ClaudeModelAlias("FABLE"), false},
		{"full model id", model.ClaudeModelAlias("claude-fable-5"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.input.Valid(); got != tc.want {
				t.Errorf("ClaudeModelAlias(%q).Valid() = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// TestClaudeModelAliasString verifies that the fable alias renders as its
// literal string value.
func TestClaudeModelAliasString(t *testing.T) {
	if got := model.ClaudeModelFable.String(); got != "fable" {
		t.Errorf("ClaudeModelFable.String() = %q, want %q", got, "fable")
	}
}

// TestClaudeModelPresetsContainOnlyValidAliases verifies that every preset
// assigns a valid alias to all 5 role keys, guarding against preset drift
// when new aliases are introduced.
func TestClaudeModelPresetsContainOnlyValidAliases(t *testing.T) {
	presets := []struct {
		name string
		fn   func() map[string]model.ClaudeModelAlias
	}{
		{"Balanced", model.ClaudeModelPresetBalanced},
		{"Performance", model.ClaudeModelPresetPerformance},
		{"Economy", model.ClaudeModelPresetEconomy},
		{"Diversity", model.ClaudeModelPresetDiversity},
	}
	for _, tc := range presets {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.fn()
			if len(m) != 5 {
				t.Errorf("%s preset has %d keys, want 5", tc.name, len(m))
			}
			if _, ok := m["jd-fix-agent"]; !ok {
				t.Errorf("%s preset missing jd-fix-agent", tc.name)
			}
			for k, v := range m {
				if !v.Valid() {
					t.Errorf("%s preset[%q] = %q is not a valid ClaudeModelAlias", tc.name, k, v)
				}
			}
		})
	}
}

func TestClaudeEffortsForModelOfficialMatrix(t *testing.T) {
	tests := []struct {
		name  string
		alias model.ClaudeModelAlias
		want  []model.ClaudeEffort
	}{
		{"fable", model.ClaudeModelFable, []model.ClaudeEffort{"", "low", "medium", "high", "xhigh", "max"}},
		{"opus", model.ClaudeModelOpus, []model.ClaudeEffort{"", "low", "medium", "high", "xhigh", "max"}},
		{"sonnet", model.ClaudeModelSonnet, []model.ClaudeEffort{"", "low", "medium", "high", "max"}},
		{"haiku", model.ClaudeModelHaiku, []model.ClaudeEffort{""}},
		{"invalid", model.ClaudeModelAlias("bogus"), []model.ClaudeEffort{""}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := model.ClaudeEffortsForModel(tc.alias)
			if len(got) != len(tc.want) {
				t.Fatalf("ClaudeEffortsForModel(%q) len = %d, want %d (%v)", tc.alias, len(got), len(tc.want), got)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("ClaudeEffortsForModel(%q)[%d] = %q, want %q (all: %v)", tc.alias, i, got[i], tc.want[i], got)
				}
			}
		})
	}
}

func TestClaudePhaseAssignmentsFromLegacyPreservesModelsWithDefaultEffort(t *testing.T) {
	legacy := map[string]model.ClaudeModelAlias{
		"jd-judge-a": model.ClaudeModelOpus,
		"odd-worker": model.ClaudeModelSonnet,
		"bad":        model.ClaudeModelAlias("bogus"),
	}

	got := model.ClaudePhaseAssignmentsFromLegacy(legacy)
	if got["jd-judge-a"].Model != model.ClaudeModelOpus || got["jd-judge-a"].Effort != "" {
		t.Fatalf("jd-judge-a assignment = %+v, want opus with default effort", got["jd-judge-a"])
	}
	if got["odd-worker"].Model != model.ClaudeModelSonnet || got["odd-worker"].Effort != "" {
		t.Fatalf("odd-worker assignment = %+v, want sonnet with default effort", got["odd-worker"])
	}
	if _, ok := got["bad"]; ok {
		t.Fatalf("invalid legacy alias should be ignored, got %+v", got["bad"])
	}
}
