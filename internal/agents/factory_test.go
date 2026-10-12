package agents

import (
	"errors"
	"reflect"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
)

func TestFactoryResolvesPiAdapter(t *testing.T) {
	adapter, err := NewAdapter(model.AgentPi)
	if err != nil {
		t.Fatalf("NewAdapter(%q) returned error: %v", model.AgentPi, err)
	}

	if got := adapter.Agent(); got != model.AgentPi {
		t.Fatalf("adapter.Agent() = %q, want %q", got, model.AgentPi)
	}
}

func TestDefaultRegistryIncludesPi(t *testing.T) {
	registry, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry() returned error: %v", err)
	}

	adapter, ok := registry.Get(model.AgentPi)
	if !ok {
		t.Fatalf("registry missing %s adapter", model.AgentPi)
	}

	if got := adapter.Agent(); got != model.AgentPi {
		t.Fatalf("registry adapter.Agent() = %q, want %q", got, model.AgentPi)
	}
}

func TestDefaultRegistrySupportedAgentsMatchesFactoryAgents(t *testing.T) {
	registry, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry() returned error: %v", err)
	}

	want := []model.AgentID{
		model.AgentClaudeCode,
		model.AgentCodex,
		model.AgentOpenCode,
		model.AgentPi,
	}

	if got := registry.SupportedAgents(); !reflect.DeepEqual(got, want) {
		t.Fatalf("SupportedAgents() = %v, want %v", got, want)
	}
}

func TestFactoryRejectsRemovedAgents(t *testing.T) {
	for _, id := range []model.AgentID{"openclaw", "cursor", "gemini-cli", "kimi", "windsurf"} {
		_, err := NewAdapter(id)
		if !errors.Is(err, ErrAgentNotSupported) {
			t.Fatalf("NewAdapter(%q) error = %v, want ErrAgentNotSupported", id, err)
		}
	}
}
