package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/carsimax/brunnr-code/internal/catalog"
	"github.com/carsimax/brunnr-code/internal/components/skills"
	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/system"
)

type InstallInput struct {
	Selection model.Selection
	Scope     InstallScope
	DryRun    bool

	// ClaudeOrchestratorModules opts a global install that selects Claude into
	// the user-global module pilot (#5256).
	ClaudeOrchestratorModules bool
}

func NormalizeInstallFlags(flags InstallFlags, detection system.DetectionResult) (InstallInput, error) {
	selection := model.Selection{}

	agents := defaultAgentsFromDetection(detection)
	if len(flags.Agents) > 0 {
		parsed, err := asAgentIDs(flags.Agents)
		if err != nil {
			return InstallInput{}, err
		}
		agents = parsed
	}
	selection.Agents = unique(agents)

	selection.Preset = model.PresetFull
	selection.Components = model.SetupComponents()
	selection.Skills = skills.SetupSkills()

	scope, err := ResolveInstallScope(flags.Scope)
	if err != nil {
		return InstallInput{}, err
	}
	// Rejected here, before the runtime is created or anything is written.
	if flags.ClaudeOrchestratorModules {
		hasClaude := containsAgent(selection.Agents, model.AgentClaudeCode)
		if scope != ScopeGlobal || !hasClaude {
			// --agent selects installation targets here, not the runtime calling
			// review. Preserve the caller's selection in the corrected command.
			agentNames := make([]string, 0, len(selection.Agents)+1)
			for _, agent := range selection.Agents {
				agentNames = append(agentNames, string(agent))
			}
			if !hasClaude {
				agentNames = append(agentNames, string(model.AgentClaudeCode))
			}
			agents := strings.Join(agentNames, ",")
			if scope != ScopeGlobal {
				return InstallInput{}, fmt.Errorf("--claude-orchestrator-modules requires --scope global, got %q; rerun brunnr-code install --agent %s --scope global --claude-orchestrator-modules", scope, agents)
			}
			return InstallInput{}, fmt.Errorf("--claude-orchestrator-modules requires the claude-code agent in the selection; rerun brunnr-code install --agent %s --scope global --claude-orchestrator-modules", agents)
		}
	}

	return InstallInput{Selection: selection, Scope: scope, DryRun: flags.DryRun, ClaudeOrchestratorModules: flags.ClaudeOrchestratorModules}, nil
}

func defaultAgentsFromDetection(detection system.DetectionResult) []model.AgentID {
	agents := []model.AgentID{}
	for _, state := range detection.Configs {
		if !state.Exists {
			continue
		}

		switch id := model.AgentID(strings.TrimSpace(state.Agent)); id {
		case model.AgentClaudeCode, model.AgentCodex:
			agents = append(agents, id)
		}
	}

	if len(agents) > 0 {
		return agents
	}

	catalogAgents := catalog.AllAgents()
	agents = make([]model.AgentID, 0, len(catalogAgents))
	for _, agent := range catalogAgents {
		agents = append(agents, agent.ID)
	}

	return agents
}

// asAgentIDs converts raw --agent/--agents flag values into model.AgentID,
// rejecting any value that is not a real, supported agent. The valid set is
// derived from catalog.AllAgents() -- the same canonical agent registry used
// by internal/app/app.go's default agent list -- so it can never drift from
// a hand-written list (install/sync surface audit finding 3: an unknown
// value like `cluade` previously converted silently and was later dropped
// without any error, so `brunnr-code sync --agent cluade` reported success
// having synced nothing).
func asAgentIDs(values []string) ([]model.AgentID, error) {
	supported := catalog.AllAgents()
	allowed := make(map[model.AgentID]struct{}, len(supported))
	names := make([]string, 0, len(supported))
	for _, agent := range supported {
		allowed[agent.ID] = struct{}{}
		names = append(names, string(agent.ID))
	}
	sort.Strings(names)

	agents := make([]model.AgentID, 0, len(values))
	for _, value := range values {
		id := model.AgentID(value)
		if _, ok := allowed[id]; !ok {
			return nil, fmt.Errorf("unsupported agent %q (valid: %s)", value, strings.Join(names, ", "))
		}
		agents = append(agents, id)
	}

	return agents, nil
}

func unique[T comparable](items []T) []T {
	seen := make(map[T]struct{}, len(items))
	result := make([]T, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}

	return result
}
