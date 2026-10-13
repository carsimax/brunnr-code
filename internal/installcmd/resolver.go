package installcmd

import (
	"fmt"

	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/system"
)

// CommandSequence represents an ordered list of commands to run in sequence.
// Each inner slice is a single command with its arguments (e.g., ["npm", "install", "-g", "pkg"]).
type CommandSequence = [][]string

type Resolver interface {
	ResolveAgentInstall(profile system.PlatformProfile, agent model.AgentID) (CommandSequence, error)
}

type profileResolver struct{}

func NewResolver() Resolver {
	return profileResolver{}
}

func (profileResolver) ResolveAgentInstall(profile system.PlatformProfile, agent model.AgentID) (CommandSequence, error) {
	switch agent {
	case model.AgentClaudeCode:
		return resolveClaudeCodeInstall(profile), nil
	default:
		return nil, fmt.Errorf("install command is not supported for agent %q", agent)
	}
}

// resolveClaudeCodeInstall returns the npm install command sequence brunnr-code
// shows for Claude Code — display text only, never executed by brunnr-code
// (see agentInstallStep in internal/cli/run.go). On macOS, the only supported
// platform, sudo is never needed.
//
// --ignore-scripts blocks postinstall hooks, the primary supply-chain attack
// vector for npm packages. The version advises "latest" rather than a pin:
// a pin only guarded against a tampered "latest" tag when brunnr-code itself
// ran the command unattended. Now a human reads and runs it, and a stale
// hardcoded version goes wrong the moment a newer release ships (the same
// drift this shape fixed for Codex's GPT-5.6 update advice).
func resolveClaudeCodeInstall(_ system.PlatformProfile) CommandSequence {
	const pkg = "@anthropic-ai/claude-code@latest"
	return CommandSequence{{"npm", "install", "-g", "--ignore-scripts", pkg}}
}

// npmBasedAgents is the set of agents whose auto-install runs npm commands.
// When any of these agents is selected, npm (and therefore Node.js) must be
// present before the pipeline reaches the agent install step.
var npmBasedAgents = map[model.AgentID]struct{}{
	model.AgentClaudeCode: {},
	model.AgentCodex:      {},
}
