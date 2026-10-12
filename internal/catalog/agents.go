package catalog

import "github.com/carsimax/brunnr-code/internal/model"

type Agent struct {
	ID         model.AgentID
	Name       string
	Tier       model.SupportTier
	ConfigPath string
}

var allAgents = []Agent{
	{ID: model.AgentClaudeCode, Name: "Claude Code", Tier: model.TierFull, ConfigPath: "~/.claude"},
	{ID: model.AgentCodex, Name: "Codex", Tier: model.TierFull, ConfigPath: "~/.codex"},
}

// mvpAgents are the supported core agents (Claude Code, Codex).
var mvpAgents = []Agent{
	{ID: model.AgentClaudeCode, Name: "Claude Code", Tier: model.TierFull, ConfigPath: "~/.claude"},
	{ID: model.AgentCodex, Name: "Codex", Tier: model.TierFull, ConfigPath: "~/.codex"},
}

func AllAgents() []Agent {
	agents := make([]Agent, len(allAgents))
	copy(agents, allAgents)
	return agents
}

func MVPAgents() []Agent {
	agents := make([]Agent, len(mvpAgents))
	copy(agents, mvpAgents)
	return agents
}

func IsMVPAgent(agent model.AgentID) bool {
	for _, current := range mvpAgents {
		if current.ID == agent {
			return true
		}
	}

	return false
}

func IsSupportedAgent(agent model.AgentID) bool {
	for _, current := range allAgents {
		if current.ID == agent {
			return true
		}
	}

	return false
}
