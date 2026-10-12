package planner

import (
	"reflect"
	"testing"

	"github.com/carsimax/brunnr-code/internal/model"
)

// TestResolverSkillsAloneResolvesWithoutEngram guards #3554: selecting only
// the Skills component must not force-install any other component.
func TestResolverSkillsAloneResolvesWithoutEngram(t *testing.T) {
	resolver := NewResolver(MVPGraph())

	selection := model.Selection{
		Agents:     []model.AgentID{model.AgentClaudeCode},
		Components: []model.ComponentID{model.ComponentSkills},
	}

	plan, err := resolver.Resolve(selection)
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}

	if !reflect.DeepEqual(plan.Agents, []model.AgentID{model.AgentClaudeCode}) {
		t.Fatalf("Resolve() agents = %v", plan.Agents)
	}

	if !reflect.DeepEqual(plan.OrderedComponents, []model.ComponentID{model.ComponentSkills}) {
		t.Fatalf("Resolve() ordered components = %v, want [skills] only", plan.OrderedComponents)
	}

	if len(plan.AddedDependencies) != 0 {
		t.Fatalf("Resolve() added dependencies = %v, want none", plan.AddedDependencies)
	}
}

func TestResolverOrdersPersonaBeforeEngram(t *testing.T) {
	resolver := NewResolver(MVPGraph())

	selection := model.Selection{
		Components: []model.ComponentID{model.ComponentPersona, model.ComponentEngram},
	}

	plan, err := resolver.Resolve(selection)
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}

	if !reflect.DeepEqual(plan.OrderedComponents, []model.ComponentID{model.ComponentPersona, model.ComponentEngram}) {
		t.Fatalf("Resolve() ordered components = %v, want [persona, engram]", plan.OrderedComponents)
	}

	if len(plan.AddedDependencies) != 0 {
		t.Fatalf("Resolve() added dependencies = %v, want none", plan.AddedDependencies)
	}
}

func TestResolverExcludesUnsupportedAgents(t *testing.T) {
	resolver := NewResolver(MVPGraph())

	selection := model.Selection{
		Agents: []model.AgentID{model.AgentClaudeCode, model.AgentCodex, model.AgentID("opencode")},
	}

	plan, err := resolver.Resolve(selection)
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}

	if !reflect.DeepEqual(plan.Agents, []model.AgentID{model.AgentClaudeCode, model.AgentCodex}) {
		t.Fatalf("Resolve() agents = %v", plan.Agents)
	}

	if !reflect.DeepEqual(plan.UnsupportedAgents, []model.AgentID{model.AgentID("opencode")}) {
		t.Fatalf("Resolve() unsupported agents = %v", plan.UnsupportedAgents)
	}
}
