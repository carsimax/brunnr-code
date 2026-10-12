package planner

import (
	"errors"
	"reflect"
	"testing"

	"github.com/carsimax/brunnr-code/internal/model"
)

func TestTopologicalSortOrdersDependenciesFirst(t *testing.T) {
	deps := map[model.ComponentID][]model.ComponentID{
		model.ComponentSkills:      {model.ComponentPermission},
		model.ComponentPermission:  {model.ComponentEngram},
		model.ComponentEngram:      nil,
		model.ComponentPersona:     nil,
		model.ComponentClaudeTheme: nil,
	}

	ordered, err := TopologicalSort(deps)
	if err != nil {
		t.Fatalf("TopologicalSort() returned error: %v", err)
	}

	if !reflect.DeepEqual(ordered, []model.ComponentID{
		model.ComponentClaudeTheme,
		model.ComponentEngram,
		model.ComponentPermission,
		model.ComponentPersona,
		model.ComponentSkills,
	}) {
		t.Fatalf("TopologicalSort() order = %v", ordered)
	}
}

func TestApplySoftOrderingReordersWithoutAddingDependencies(t *testing.T) {
	ordered := []model.ComponentID{
		model.ComponentClaudeTheme,
		model.ComponentEngram,
		model.ComponentPersona,
		model.ComponentSkills,
	}

	result := applySoftOrdering(ordered, [][2]model.ComponentID{{model.ComponentPersona, model.ComponentEngram}})

	if !reflect.DeepEqual(result, []model.ComponentID{
		model.ComponentClaudeTheme,
		model.ComponentPersona,
		model.ComponentEngram,
		model.ComponentSkills,
	}) {
		t.Fatalf("applySoftOrdering() = %v", result)
	}

	// If the first component is absent, nothing should be added.
	result = applySoftOrdering([]model.ComponentID{model.ComponentEngram}, [][2]model.ComponentID{{model.ComponentPersona, model.ComponentEngram}})
	if !reflect.DeepEqual(result, []model.ComponentID{model.ComponentEngram}) {
		t.Fatalf("applySoftOrdering() should not add missing components (first absent), got %v", result)
	}
}

func TestApplySoftOrderingEdgeCases(t *testing.T) {
	pair := [][2]model.ComponentID{{model.ComponentPersona, model.ComponentEngram}}

	// Second absent — no-op, no panic
	result := applySoftOrdering([]model.ComponentID{model.ComponentPersona}, pair)
	if !reflect.DeepEqual(result, []model.ComponentID{model.ComponentPersona}) {
		t.Fatalf("second absent: expected [persona], got %v", result)
	}

	// Both absent — no-op
	result = applySoftOrdering([]model.ComponentID{model.ComponentSkills}, pair)
	if !reflect.DeepEqual(result, []model.ComponentID{model.ComponentSkills}) {
		t.Fatalf("both absent: expected [skills], got %v", result)
	}

	// Already correct order — no-op (must not mutate)
	already := []model.ComponentID{model.ComponentPersona, model.ComponentEngram}
	result = applySoftOrdering(already, pair)
	if !reflect.DeepEqual(result, []model.ComponentID{model.ComponentPersona, model.ComponentEngram}) {
		t.Fatalf("already correct: expected [persona, engram], got %v", result)
	}

	// Input slice must NOT be mutated
	input := []model.ComponentID{model.ComponentEngram, model.ComponentPersona}
	_ = applySoftOrdering(input, pair)
	if !reflect.DeepEqual(input, []model.ComponentID{model.ComponentEngram, model.ComponentPersona}) {
		t.Fatalf("input slice was mutated")
	}
}

func TestApplySoftOrderingMVPPairsWithFullSelection(t *testing.T) {
	// Simulates the real scenario: topo gives [claude-theme, engram, persona, skills]
	// and the MVPGraph soft pairs must put persona before engram.
	ordered := []model.ComponentID{
		model.ComponentClaudeTheme,
		model.ComponentEngram,
		model.ComponentPersona,
		model.ComponentSkills,
	}

	result := applySoftOrdering(ordered, SoftOrderingConstraints())

	personaIdx, engramIdx := -1, -1
	for i, c := range result {
		switch c {
		case model.ComponentPersona:
			personaIdx = i
		case model.ComponentEngram:
			engramIdx = i
		}
	}

	if personaIdx < 0 || engramIdx < 0 {
		t.Fatalf("missing components in result: %v", result)
	}
	if personaIdx > engramIdx {
		t.Fatalf("Persona (%d) must be before Engram (%d), got %v", personaIdx, engramIdx, result)
	}
	// Skills must remain last
	if result[len(result)-1] != model.ComponentSkills {
		t.Fatalf("Skills must remain last, got %v", result)
	}
}

func TestTopologicalSortDetectsCycles(t *testing.T) {
	deps := map[model.ComponentID][]model.ComponentID{
		model.ComponentEngram:  {model.ComponentPersona},
		model.ComponentPersona: {model.ComponentEngram},
	}

	_, err := TopologicalSort(deps)
	if err == nil {
		t.Fatalf("TopologicalSort() expected cycle error")
	}

	if !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("TopologicalSort() error = %v, want ErrDependencyCycle", err)
	}
}
