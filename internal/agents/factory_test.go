package agents

import (
	"errors"
	"reflect"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
)

func TestDefaultRegistrySupportedAgentsMatchesFactoryAgents(t *testing.T) {
	registry, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry() returned error: %v", err)
	}

	want := []model.AgentID{
		model.AgentClaudeCode,
		model.AgentCodex,
		model.AgentOpenCode,
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
