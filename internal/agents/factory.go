package agents

import (
	"fmt"

	"github.com/gentleman-programming/gentle-ai/v4/internal/agents/claude"
	"github.com/gentleman-programming/gentle-ai/v4/internal/agents/codex"
	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
)

var defaultAgentIDs = []model.AgentID{
	model.AgentClaudeCode,
	model.AgentCodex,
}

func NewAdapter(agent model.AgentID) (Adapter, error) {
	switch agent {
	case model.AgentClaudeCode:
		return claude.NewAdapter(), nil
	case model.AgentCodex:
		return codex.NewAdapter(), nil
	default:
		return nil, AgentNotSupportedError{Agent: agent}
	}
}

func NewDefaultRegistry() (*Registry, error) {
	adapters := make([]Adapter, 0, len(defaultAgentIDs))

	for _, agent := range defaultAgentIDs {
		adapter, err := NewAdapter(agent)
		if err != nil {
			return nil, fmt.Errorf("create %s adapter: %w", agent, err)
		}
		adapters = append(adapters, adapter)
	}

	registry, err := NewRegistry(adapters...)
	if err != nil {
		return nil, fmt.Errorf("create registry: %w", err)
	}

	return registry, nil
}
