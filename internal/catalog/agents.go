package catalog

import "github.com/gentleman-programming/gentle-ai/v4/internal/model"

type Agent struct {
	ID         model.AgentID
	Name       string
	Tier       model.SupportTier
	ConfigPath string

	// ReviewNote, when non-empty, is surfaced on the install review screen
	// whenever this agent is selected. It exists for detection/catalog-only
	// integrations whose selection deserves an explicit expectation setting;
	// writable agents must leave it empty.
	ReviewNote string
}

var allAgents = []Agent{
	{ID: model.AgentClaudeCode, Name: "Claude Code", Tier: model.TierFull, ConfigPath: "~/.claude"},
	{ID: model.AgentOpenCode, Name: "OpenCode", Tier: model.TierFull, ConfigPath: "~/.config/opencode"},
	{ID: model.AgentCodex, Name: "Codex", Tier: model.TierFull, ConfigPath: "~/.codex"},
	{ID: model.AgentPi, Name: "Pi", Tier: model.TierFull, ConfigPath: "~/.pi"},
}

// mvpAgents are the original MVP agents (Claude Code, OpenCode).
var mvpAgents = []Agent{
	{ID: model.AgentClaudeCode, Name: "Claude Code", Tier: model.TierFull, ConfigPath: "~/.claude"},
	{ID: model.AgentOpenCode, Name: "OpenCode", Tier: model.TierFull, ConfigPath: "~/.config/opencode"},
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
