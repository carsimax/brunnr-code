package model

type AgentID string

const (
	AgentClaudeCode AgentID = "claude-code"
	AgentCodex      AgentID = "codex"
)

// IsSupportedAgentID reports whether id names an agent this build supports.
func IsSupportedAgentID(id AgentID) bool {
	return id == AgentClaudeCode || id == AgentCodex
}

// SupportedAgentIDs returns the persisted agent IDs this build still supports,
// preserving order. IDs of agents that were removed are dropped silently so
// stale persisted state never blocks discovery, sync, or install.
func SupportedAgentIDs(ids []string) []string {
	kept := make([]string, 0, len(ids))
	for _, id := range ids {
		if IsSupportedAgentID(AgentID(id)) {
			kept = append(kept, id)
		}
	}
	return kept
}

// SupportTier indicates how fully an agent supports the Brunnr-Code ecosystem.
// All current agents receive the full orchestrator, skill files, MCP config,
// and system prompt injection. The tier is kept as metadata for display purposes.
type SupportTier string

const (
	// TierFull — the agent receives all ecosystem features: orchestrator,
	// skill files, MCP servers, system prompt, and sub-agent delegation.
	TierFull SupportTier = "full"
)

type ComponentID string

const (
	ComponentEngram     ComponentID = "engram"
	ComponentSkills     ComponentID = "skills"
	ComponentPersona    ComponentID = "persona"
	ComponentPermission ComponentID = "permissions"
)

// knownComponentIDs lists every component this build ships. The catalog test
// keeps it in sync with catalog.MVPComponents.
var knownComponentIDs = []ComponentID{
	ComponentEngram,
	ComponentSkills,
	ComponentPersona,
	ComponentPermission,
}

// ShippedComponentIDs returns a copy of every component ID this build ships.
func ShippedComponentIDs() []ComponentID {
	return append([]ComponentID(nil), knownComponentIDs...)
}

type UninstallMode string

const (
	UninstallModePartial      UninstallMode = "partial"
	UninstallModeFull         UninstallMode = "full"
	UninstallModeFullRemove   UninstallMode = "full-remove"
	UninstallModeCleanInstall UninstallMode = "clean-install"
)

type EngramUninstallScope string

const (
	EngramUninstallScopeGlobal  EngramUninstallScope = "global"
	EngramUninstallScopeProject EngramUninstallScope = "project"
)

type SkillID string

const (
	SkillGoTesting           SkillID = "go-testing"
	SkillDebugging           SkillID = "debugging"
	SkillCreator             SkillID = "skill-creator"
	SkillImprover            SkillID = "skill-improver"
	SkillJudgmentDay         SkillID = "judgment-day"
	SkillBranchPR            SkillID = "branch-pr"
	SkillIssueCreation       SkillID = "issue-creation"
	SkillSkillRegistry       SkillID = "skill-registry"
	SkillChainedPR           SkillID = "chained-pr"
	SkillCognitiveDoc        SkillID = "cognitive-doc-design"
	SkillCommentWriter       SkillID = "comment-writer"
	SkillWorkUnitCommits     SkillID = "work-unit-commits"
	SkillRDDDefectWorkflow   SkillID = "rdd-defect-workflow"
	SkillSystemicIssueTriage SkillID = "systemic-issue-triage"
)

// SystemPromptStrategy defines how an agent's system prompt file is managed.
type SystemPromptStrategy int

const (
	// StrategyMarkdownSections uses <!-- brunnr-code:ID --> markers to inject sections
	// into an existing file without clobbering user content (Claude Code CLAUDE.md).
	StrategyMarkdownSections SystemPromptStrategy = iota
	// StrategyFileReplace replaces the entire system prompt file (e.g. Codex AGENTS.md).
	StrategyFileReplace
)

// MCPStrategy defines how MCP server configs are written for an agent.
type MCPStrategy int

const (
	// StrategySeparateMCPFiles writes one JSON file per server in a dedicated directory
	// (e.g., ~/.claude/mcp/engram.json).
	StrategySeparateMCPFiles MCPStrategy = iota
	// StrategyTOMLFile writes MCP config to a TOML file (e.g., Codex ~/.codex/config.toml).
	StrategyTOMLFile
)

// PresetID names the setup a selection installs. Brunnr-Code has a single
// setup, "Dev Stack + Permissions"; see SetupComponents.
type PresetID string

const PresetFull PresetID = "full"

type CommunityToolID string

const (
	CommunityToolCodeGraph CommunityToolID = "codegraph"
)
