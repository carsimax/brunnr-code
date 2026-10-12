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
// All current agents receive the full SDD orchestrator, skill files, MCP config,
// and system prompt injection. The tier is kept as metadata for display purposes.
type SupportTier string

const (
	// TierFull — the agent receives all ecosystem features: SDD orchestrator,
	// skill files, MCP servers, system prompt, and sub-agent delegation.
	TierFull SupportTier = "full"
)

type ComponentID string

const (
	ComponentEngram      ComponentID = "engram"
	ComponentSDD         ComponentID = "sdd"
	ComponentSkills      ComponentID = "skills"
	ComponentContext7    ComponentID = "context7"
	ComponentPersona     ComponentID = "persona"
	ComponentPermission  ComponentID = "permissions"
	ComponentTheme       ComponentID = "theme"
	ComponentClaudeTheme ComponentID = "claude-theme"
)

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
	SkillSDDInit             SkillID = "sdd-init"
	SkillSDDApply            SkillID = "sdd-apply"
	SkillSDDVerify           SkillID = "sdd-verify"
	SkillSDDExplore          SkillID = "sdd-explore"
	SkillSDDResearch         SkillID = "sdd-research"
	SkillSDDPropose          SkillID = "sdd-propose"
	SkillSDDSpec             SkillID = "sdd-spec"
	SkillSDDDesign           SkillID = "sdd-design"
	SkillSDDTasks            SkillID = "sdd-tasks"
	SkillSDDArchive          SkillID = "sdd-archive"
	SkillSDDOnboard          SkillID = "sdd-onboard"
	SkillGoTesting           SkillID = "go-testing"
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
	SkillGentleAIBench       SkillID = "gentle-ai-bench"
)

// SystemPromptStrategy defines how an agent's system prompt file is managed.
type SystemPromptStrategy int

const (
	// StrategyMarkdownSections uses <!-- gentle-ai:ID --> markers to inject sections
	// into an existing file without clobbering user content (Claude Code CLAUDE.md).
	StrategyMarkdownSections SystemPromptStrategy = iota
	// StrategyFileReplace replaces the entire system prompt file (e.g. Codex AGENTS.md).
	StrategyFileReplace
)

// MCPStrategy defines how MCP server configs are written for an agent.
type MCPStrategy int

const (
	// StrategySeparateMCPFiles writes one JSON file per server in a dedicated directory
	// (e.g., ~/.claude/mcp/context7.json).
	StrategySeparateMCPFiles MCPStrategy = iota
	// StrategyTOMLFile writes MCP config to a TOML file (e.g., Codex ~/.codex/config.toml).
	StrategyTOMLFile
)

type PresetID string

const (
	PresetFull          PresetID = "full"
	PresetEcosystemOnly PresetID = "ecosystem-only"
	PresetMinimal       PresetID = "minimal"
	PresetCustom        PresetID = "custom"
)

type SDDModeID string

const (
	SDDModeSingle SDDModeID = "single"
	SDDModeMulti  SDDModeID = "multi"
)

type CommunityToolID string

const (
	CommunityToolCodeGraph CommunityToolID = "codegraph"
)
