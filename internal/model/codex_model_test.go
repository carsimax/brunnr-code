package model_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/model"
)

func TestCodexEffortValid(t *testing.T) {
	tests := []struct {
		name  string
		input model.CodexEffort
		want  bool
	}{
		{"low", model.CodexEffortLow, true},
		{"medium", model.CodexEffortMedium, true},
		{"high", model.CodexEffortHigh, true},
		{"xhigh", model.CodexEffortXHigh, true},
		{"empty", model.CodexEffort(""), false},
		{"junk", model.CodexEffort("junk"), false},
		{"uppercase", model.CodexEffort("HIGH"), false},
		{"max (runtime-gated by the picker)", model.CodexEffortMax, true},
		{"ultra (runtime-gated by the picker)", model.CodexEffortUltra, true},
		{"none is not routable", model.CodexEffort("none"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.input.Valid(); got != tc.want {
				t.Errorf("CodexEffort(%q).Valid() = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestCodexPresetsModelAndEffortPerRole(t *testing.T) {
	for _, tc := range []struct {
		preset  model.CodexPresetKey
		models  map[string]string
		efforts map[string]model.CodexEffort
	}{
		{
			preset:  model.CodexPresetLowCost,
			models:  map[string]string{"odd-explorer": "gpt-6.1-luna", "odd-worker": "gpt-6.1-luna", "odd-verify": "gpt-6.1-sol"},
			efforts: map[string]model.CodexEffort{"odd-explorer": model.CodexEffortHigh, "odd-worker": model.CodexEffortMedium, "odd-verify": model.CodexEffortMedium},
		},
		{
			// The worker writes code in an agentic loop, which is effort-sensitive,
			// so the balanced policy buys high effort on the cheaper writing model.
			preset:  model.CodexPresetRecommended,
			models:  map[string]string{"odd-explorer": "gpt-6.1-luna", "odd-worker": "gpt-6.1-luna", "odd-verify": "gpt-6.1-sol"},
			efforts: map[string]model.CodexEffort{"odd-explorer": model.CodexEffortHigh, "odd-worker": model.CodexEffortHigh, "odd-verify": model.CodexEffortMedium},
		},
		{
			preset:  model.CodexPresetPowerful,
			models:  map[string]string{"odd-explorer": "gpt-6.1-luna", "odd-worker": "gpt-6.1-sol", "odd-verify": "gpt-6.1-astra"},
			efforts: map[string]model.CodexEffort{"odd-explorer": model.CodexEffortHigh, "odd-worker": model.CodexEffortHigh, "odd-verify": model.CodexEffortXHigh},
		},
	} {
		t.Run(string(tc.preset), func(t *testing.T) {
			if got := model.CodexCarrilModelsForPreset(string(tc.preset)); !reflect.DeepEqual(got, tc.models) {
				t.Errorf("CodexCarrilModelsForPreset() = %#v, want %#v", got, tc.models)
			}
			if got := model.CodexODDEffortsForPreset(string(tc.preset)); !reflect.DeepEqual(got, tc.efforts) {
				t.Errorf("CodexODDEffortsForPreset() = %#v, want %#v", got, tc.efforts)
			}
		})
	}
}

func TestCodexPresetCarrilDefaults_UnknownPresetFallsBackToRecommended(t *testing.T) {
	got := model.CodexPresetCarrilDefaults("unknown-preset")
	want := model.CodexPresetCarrilDefaults(string(model.CodexPresetRecommended))

	for carril, wantDefault := range want {
		if got[carril] != wantDefault {
			t.Errorf("CodexPresetCarrilDefaults(unknown)[%q] = %+v, want recommended %+v", carril, got[carril], wantDefault)
		}
	}
	if len(got) != len(want) {
		t.Errorf("CodexPresetCarrilDefaults(unknown) len = %d, want %d", len(got), len(want))
	}
}

func TestCodexPresetConstantsRemainStringCompatible(t *testing.T) {
	tests := []struct {
		name string
		key  model.CodexPresetKey
		want string
	}{
		{"low cost", model.CodexPresetLowCost, "low-cost"},
		{"recommended", model.CodexPresetRecommended, "recommended"},
		{"powerful", model.CodexPresetPowerful, "powerful"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if string(tc.key) != tc.want {
				t.Errorf("string(%s) = %q, want %q", tc.name, tc.key, tc.want)
			}
		})
	}
}

// ─── WU-1: CodexAvailableModels + FilterCodexModelList ─────────────────────

func TestCodexODDPresetCarrilModelAndEffortMatrix(t *testing.T) {
	for _, tc := range []struct {
		name   string
		preset model.CodexPresetKey
		rows   []string
	}{
		{"low cost", model.CodexPresetLowCost, []string{
			"| `odd-explorer` | `gpt-6.1-luna` | `high` |",
			"| `odd-worker` | `gpt-6.1-luna` | `medium` |",
			"| `odd-verify` | `gpt-6.1-sol` | `medium` |",
		}},
		{"powerful", model.CodexPresetPowerful, []string{
			"| `odd-explorer` | `gpt-6.1-luna` | `high` |",
			"| `odd-worker` | `gpt-6.1-sol` | `high` |",
			"| `odd-verify` | `gpt-6.1-astra` | `xhigh` |",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := model.RenderCodexODDAssignments(nil, model.CodexODDEffortsForPreset(string(tc.preset)), model.CodexCarrilModelsForPreset(string(tc.preset)))
			for _, row := range tc.rows {
				if !strings.Contains(out, row) {
					t.Errorf("missing %q in %s", row, out)
				}
			}
		})
	}
}

func TestCodexODDAssignmentsDefaultsAndOverrides(t *testing.T) {
	for _, tc := range []struct {
		name    string
		models  map[string]string
		efforts map[string]model.CodexEffort
		carrils map[string]string
		rows    []string
	}{
		{"recommended fallback", nil, nil, nil, []string{
			"| `odd-explorer` | `gpt-6.1-luna` | `high` |",
			"| `odd-worker` | `gpt-6.1-luna` | `high` |",
			"| `odd-verify` | `gpt-6.1-sol` | `medium` |",
		}},
		{"custom overrides", map[string]string{"odd-worker": "gpt-6.1-astra", "rdd-risk": "gpt-6.1-astra"},
			map[string]model.CodexEffort{"odd-worker": model.CodexEffortXHigh},
			map[string]string{"odd-explorer": "gpt-6.1-luna", "odd-worker": "gpt-6.1-sol", "odd-verify": "gpt-6.1-astra"}, []string{
				"| `odd-explorer` | `gpt-6.1-luna` | `high` |",
				"| `odd-worker` | `gpt-6.1-astra` | `xhigh` |",
				"| `odd-verify` | `gpt-6.1-astra` | `medium` |",
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := model.RenderCodexODDAssignments(tc.models, tc.efforts, tc.carrils)
			for _, row := range tc.rows {
				if !strings.Contains(got, row) {
					t.Errorf("missing %q in %q", row, got)
				}
			}
			if strings.Contains(got, "rdd-risk") {
				t.Errorf("RDD role leaked into ODD table: %q", got)
			}
		})
	}
}

func TestCodexAvailableModels_Contents(t *testing.T) {
	models := model.CodexAvailableModels()
	want := []string{"gpt-6.1-astra", "gpt-6.1-sol", "gpt-6.1-luna", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5", "gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex", "gpt-5.2-codex"}
	if len(models) != len(want) {
		t.Fatalf("CodexAvailableModels() len = %d, want %d", len(models), len(want))
	}
	for i, w := range want {
		if models[i] != w {
			t.Errorf("CodexAvailableModels()[%d] = %q, want %q", i, models[i], w)
		}
	}
}

func TestFilterCodexModelList_EmptyQuery(t *testing.T) {
	// Empty query returns all models.
	all := model.CodexAvailableModels()
	result := model.FilterCodexModelList(all, "")
	if len(result) != len(all) {
		t.Errorf("FilterCodexModelList(\"\") len = %d, want %d", len(result), len(all))
	}
	for i, m := range all {
		if result[i] != m {
			t.Errorf("FilterCodexModelList(\"\")[%d] = %q, want %q", i, result[i], m)
		}
	}
}

func TestFilterCodexModelList_Match(t *testing.T) {
	tests := []struct {
		query    string
		wantAny  []string
		wantNone []string
	}{
		{
			query:    "sol",
			wantAny:  []string{"gpt-5.6-sol"},
			wantNone: []string{"gpt-5.6-terra", "gpt-5.6-luna"},
		},
		{
			query:    "terra",
			wantAny:  []string{"gpt-5.6-terra"},
			wantNone: []string{"gpt-5.6-sol", "gpt-5.6-luna"},
		},
		{
			query:    "luna",
			wantAny:  []string{"gpt-5.6-luna"},
			wantNone: []string{"gpt-5.6-sol", "gpt-5.6-terra"},
		},
		{
			query:    "gpt-5.5",
			wantAny:  []string{"gpt-5.5"},
			wantNone: []string{"gpt-5.4-mini", "gpt-5.2-codex"},
		},
		{
			query:    "codex",
			wantAny:  []string{"gpt-5.2-codex", "gpt-5.3-codex"},
			wantNone: []string{"gpt-5.5", "gpt-5.4"},
		},
		{
			query:    "CODEX", // case-insensitive
			wantAny:  []string{"gpt-5.2-codex", "gpt-5.3-codex"},
			wantNone: []string{"gpt-5.5"},
		},
		{
			query:    "mini",
			wantAny:  []string{"gpt-5.4-mini"},
			wantNone: []string{"gpt-5.5", "gpt-5.4"},
		},
	}
	all := model.CodexAvailableModels()
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			result := model.FilterCodexModelList(all, tc.query)
			resultSet := make(map[string]bool, len(result))
			for _, r := range result {
				resultSet[r] = true
			}
			for _, want := range tc.wantAny {
				if !resultSet[want] {
					t.Errorf("FilterCodexModelList(%q): expected %q in result %v", tc.query, want, result)
				}
			}
			for _, noWant := range tc.wantNone {
				if resultSet[noWant] {
					t.Errorf("FilterCodexModelList(%q): unexpected %q in result %v", tc.query, noWant, result)
				}
			}
		})
	}
}

func TestFilterCodexModelList_NoMatch(t *testing.T) {
	result := model.FilterCodexModelList(model.CodexAvailableModels(), "zzz-no-match")
	if len(result) != 0 {
		t.Errorf("FilterCodexModelList(no match) = %v, want empty", result)
	}
}

// TestCodexPresetOrchestratorAssignment_MediumEffortWithPerPresetModel pins the
// two halves of the orchestrator policy separately. The effort is one shared
// rule: the orchestrator plans, routes and adjudicates rather than doing the
// delegated work, so every preset runs it at medium. The model is per-preset:
// low-cost runs the main session on Luna so a Plus plan can still afford Sol
// in the strong lanes, where the reasoning actually pays.
func TestCodexPresetOrchestratorAssignment_MediumEffortWithPerPresetModel(t *testing.T) {
	wantModel := map[model.CodexPresetKey]string{
		model.CodexPresetLowCost:     "gpt-6.1-luna",
		model.CodexPresetRecommended: "gpt-6.1-sol",
		model.CodexPresetPowerful:    "gpt-6.1-astra",
	}
	for preset, want := range wantModel {
		a := model.CodexPresetOrchestratorAssignment(string(preset))
		if a.Model != want || a.Effort != model.CodexEffortMedium {
			t.Errorf("preset %q orchestrator = %s/%s, want %s/medium", preset, a.Model, a.Effort, want)
		}
	}
}
