package planner

import "github.com/carsimax/brunnr-code/internal/model"

type Graph struct {
	dependencies map[model.ComponentID][]model.ComponentID
}

func NewGraph(dependencies map[model.ComponentID][]model.ComponentID) Graph {
	normalized := make(map[model.ComponentID][]model.ComponentID, len(dependencies))
	for component, deps := range dependencies {
		copyDeps := make([]model.ComponentID, len(deps))
		copy(copyDeps, deps)
		normalized[component] = copyDeps
	}

	return Graph{dependencies: normalized}
}

func (g Graph) Has(component model.ComponentID) bool {
	_, ok := g.dependencies[component]
	return ok
}

func (g Graph) DependenciesOf(component model.ComponentID) []model.ComponentID {
	deps, ok := g.dependencies[component]
	if !ok {
		return nil
	}

	copyDeps := make([]model.ComponentID, len(deps))
	copy(copyDeps, deps)
	return copyDeps
}

func MVPGraph() Graph {
	return NewGraph(map[model.ComponentID][]model.ComponentID{
		model.ComponentEngram:      nil,
		model.ComponentSkills:      nil,
		model.ComponentContext7:    nil,
		model.ComponentPersona:     nil,
		model.ComponentPermission:  nil,
		model.ComponentTheme:       nil,
		model.ComponentClaudeTheme: nil,
	})
}

// softOrderingPairs defines component pairs where the first MUST execute before
// the second when BOTH are present in the resolved plan. These are NOT hard
// dependencies — selecting one does not force-install the other.
//
// This exists because StrategyFileReplace agents (e.g.
// Codex) have Persona write the base file and Engram append to it. If
// Engram ran before Persona, Persona would overwrite the Engram sections.
//
// INVARIANT: the `first` element in every pair must have nil deps in MVPGraph.
// See applySoftOrdering() safety contract in order.go.
var softOrderingPairs = [][2]model.ComponentID{
	{model.ComponentPersona, model.ComponentEngram},
}

// SoftOrderingConstraints returns the static soft-ordering pairs.
func SoftOrderingConstraints() [][2]model.ComponentID {
	return softOrderingPairs
}
