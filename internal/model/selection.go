package model

type Selection struct {
	Agents                           []AgentID
	Components                       []ComponentID
	Skills                           []SkillID
	Preset                           PresetID
	StrictTDD                        bool
	CodexMultiAgent                  bool                             // deprecated: Codex now always writes features.multi_agent = true; retained for state/back-compat
	ClaudeModelAssignments           map[string]ClaudeModelAlias      // key = phase name; value = fable|opus|sonnet|haiku
	ClaudePhaseAssignments           map[string]ClaudePhaseAssignment // key = phase name; value = Claude model+effort
	CodexModelAssignments            map[string]CodexEffort           // key = phase name; value = low|medium|high|xhigh|max|ultra
	CodexOrchestratorAssignment      *CodexOrchestratorAssignment     // non-nil = apply curated top-level Codex model/effort
	ClearCodexOrchestratorAssignment bool                             // true = clear persisted curated assignment while preserving config.toml
	CodexServiceTier                 string                           // global config.toml service_tier ("" = standard); written only when it differs from CodexManagedServiceTier
	CodexManagedServiceTier          string                           // service_tier Brunnr-Code previously wrote (from state); retired on standard only while config still holds it
	CodexCarrilModelAssignments      map[string]string                // key = ODD role (odd-explorer|odd-worker|odd-verify); value = model id
	CodexPhaseModelAssignments       map[string]string                // key = phase name; value = model id (Custom per-phase picker only)
	CommunityTools                   []CommunityToolID                // optional cross-agent community tools/plugins
}

func (s Selection) HasCommunityTool(tool CommunityToolID) bool {
	for _, current := range s.CommunityTools {
		if current == tool {
			return true
		}
	}
	return false
}

func (s Selection) HasAgent(agent AgentID) bool {
	for _, current := range s.Agents {
		if current == agent {
			return true
		}
	}

	return false
}

func (s Selection) HasComponent(component ComponentID) bool {
	for _, current := range s.Components {
		if current == component {
			return true
		}
	}

	return false
}

// EnsureComponent appends component to s.Components when it is not already
// present. Used to re-enable a component that a persisted selection would
// otherwise drop, once the caller has explicitly requested work that only
// that component's sync stage can perform.
func (s *Selection) EnsureComponent(component ComponentID) {
	if s.HasComponent(component) {
		return
	}
	s.Components = append(s.Components, component)
}

// SyncOverrides holds optional overrides applied to the sync selection.
// Used when the TUI "Configure Models" flow needs to persist model assignments
// without re-running the full install pipeline.
//
// Nil fields mean "no override" — the sync uses defaults from BuildSyncSelection.
// A non-nil but empty map means "reset to defaults" (explicit clear).
type SyncOverrides struct {
	// TargetAgents forces TUI sync to run the adapter(s) affected by the
	// override, even when persisted install state omits them. This is used by
	// model/profile configurators, where the user picked a concrete target agent.
	TargetAgents                     []AgentID
	ClaudeModelAssignments           map[string]ClaudeModelAlias      // nil = no override; empty map = reset to defaults
	ClaudePhaseAssignments           map[string]ClaudePhaseAssignment // nil = no override; empty map = reset to defaults
	CodexModelAssignments            map[string]CodexEffort           // nil = no override; empty map = reset to defaults
	CodexOrchestratorAssignment      *CodexOrchestratorAssignment     // non-nil = apply curated top-level Codex model/effort
	ClearCodexOrchestratorAssignment bool                             // true = clear persisted curated assignment while preserving config.toml
	CodexServiceTier                 *string                          // nil = no override; "" = standard; otherwise the selected service tier
	CodexCarrilModelAssignments      map[string]string                // nil = no override; empty map = reset to defaults
	CodexPhaseModelAssignments       map[string]string                // nil = no override (partial sync); non-nil empty = clear (preset selected); non-nil non-empty = custom per-phase assignments
	StrictTDD                        *bool                            // nil = no override; non-nil = override strict TDD mode
}
