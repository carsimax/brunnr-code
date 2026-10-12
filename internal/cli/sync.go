package cli

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/backup"
	"github.com/carsimax/brunnr-code/internal/components/agentguidance"
	"github.com/carsimax/brunnr-code/internal/components/communitytool"
	"github.com/carsimax/brunnr-code/internal/components/engram"
	"github.com/carsimax/brunnr-code/internal/components/filemerge"
	"github.com/carsimax/brunnr-code/internal/components/mcp"
	"github.com/carsimax/brunnr-code/internal/components/permissions"
	"github.com/carsimax/brunnr-code/internal/components/persona"
	"github.com/carsimax/brunnr-code/internal/components/reviewassets"
	"github.com/carsimax/brunnr-code/internal/components/skills"
	"github.com/carsimax/brunnr-code/internal/components/theme"
	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/pipeline"
	"github.com/carsimax/brunnr-code/internal/planner"
	"github.com/carsimax/brunnr-code/internal/state"
	"github.com/carsimax/brunnr-code/internal/system"
	"github.com/carsimax/brunnr-code/internal/verify"
)

// SyncFlags holds parsed CLI flags for the sync command.
type SyncFlags struct {
	Agents             []string
	Skills             []string
	StrictTDD          bool
	IncludePermissions bool
	IncludeTheme       bool
	Scope              string
	DryRun             bool

	skillsSet      bool
	strictTDDSet   bool
	permissionsSet bool
	themeSet       bool
}

// SyncResult holds the outcome of a sync execution.
type SyncResult struct {
	Agents    []model.AgentID
	Selection model.Selection
	Plan      pipeline.StagePlan
	Execution pipeline.ExecutionResult
	Verify    verify.Report
	DryRun    bool
	// NoOp is true when no managed asset changes were needed:
	// either no agents were discovered/provided, or all managed assets
	// were already current (idempotent re-sync).
	NoOp bool
	// FilesChanged is the number of deduplicated managed file paths
	// whose persisted content or existence changed during this sync.
	// Zero means all assets were already current.
	FilesChanged int
	// ChangedFiles lists deduplicated absolute paths of managed files
	// processed during this sync. Paths appear once even when multiple
	// components touch the same file. It is nil when no files changed.
	ChangedFiles  []string
	ManualActions []string
	// DryRunStops lists refusals a dry-run found that the real sync would stop
	// on before writing anything.
	DryRunStops []string

	// SkippedAgents lists selected agents this sync deliberately did not
	// touch, each with the actionable reason. A non-empty list makes the sync
	// partial: the returned error is a *PartialSyncError.
	SkippedAgents []SyncSkippedAgent
}

// SyncSkippedAgent is one selected agent that a partial sync left untouched.
type SyncSkippedAgent struct {
	Agent model.AgentID
	// Part names the skipped part of the agent; empty means the whole agent.
	Part   string
	Reason string
}

// PartialSyncError reports a sync that applied every other selected agent but
// skipped some. The result returned with it is complete for the applied part.
type PartialSyncError struct {
	Skipped []SyncSkippedAgent
}

func (e *PartialSyncError) Error() string {
	parts := make([]string, 0, len(e.Skipped))
	for _, skipped := range e.Skipped {
		parts = append(parts, skipped.Action())
	}
	return "partial sync: the other selected agents were synced; " + strings.Join(parts, "; ")
}

// Action is the operator-facing line for a skipped agent, reused by the CLI
// error and the TUI manual actions.
func (s SyncSkippedAgent) Action() string {
	if s.Part != "" {
		return fmt.Sprintf("%s %s were skipped: %s; then re-run `brunnr-code sync`", s.Agent, s.Part, s.Reason)
	}
	return fmt.Sprintf("%s was skipped: %s; then re-run `brunnr-code sync`", s.Agent, s.Reason)
}

// finishPartialSync attaches skipped agents to a sync result and turns an
// otherwise successful partial sync into a *PartialSyncError.
func finishPartialSync(result SyncResult, err error, skipped []SyncSkippedAgent) (SyncResult, error) {
	result.SkippedAgents = append(append([]SyncSkippedAgent(nil), skipped...), result.SkippedAgents...)
	if err == nil && len(result.SkippedAgents) > 0 {
		err = &PartialSyncError{Skipped: result.SkippedAgents}
	}
	return result, err
}

// ParseSyncFlags parses the CLI arguments for the sync subcommand.
func ParseSyncFlags(args []string) (SyncFlags, error) {
	var opts SyncFlags

	// Usage is captured (not discarded) so a mistyped flag error can carry the
	// FlagSet's own canonical usage text (install/sync surface audit finding
	// 4): the flag package's failf() calls fs.usage() on every parse error
	// (undefined flag, bad value, or -h/--help itself), so capturing
	// fs.Output() gives the exact registered-flag list without hand-listing
	// it anywhere that could drift.
	var usage bytes.Buffer
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(&usage)
	registerListFlag(fs, "agent", &opts.Agents)
	registerListFlag(fs, "agents", &opts.Agents)
	registerListFlag(fs, "skill", &opts.Skills)
	registerListFlag(fs, "skills", &opts.Skills)
	fs.BoolVar(&opts.StrictTDD, "strict-tdd", false, "retired: ODD uses applicable test-first development by default")
	fs.BoolVar(&opts.IncludePermissions, "include-permissions", false, "include permissions component in sync")
	fs.BoolVar(&opts.IncludeTheme, "include-theme", false, "include theme component in sync")
	fs.StringVar(&opts.Scope, "scope", "", "sync scope: global (default) or workspace — env: BRUNNR_CODE_INSTALL_SCOPE")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "preview plan without executing")

	if err := fs.Parse(args); err != nil {
		// The flag package's own failf/usage() may have already printed the
		// error text once before the "Usage of sync:" block; trim that
		// duplicate so the wrapped error below does not repeat it.
		usageText := usage.String()
		if idx := strings.Index(usageText, "Usage of "); idx >= 0 {
			usageText = usageText[idx:]
		}
		usageText = strings.TrimRight(usageText, "\n")
		if usageText != "" {
			return SyncFlags{}, fmt.Errorf("%w — run `brunnr-code sync --help` for the supported flags:\n%s", err, usageText)
		}
		return SyncFlags{}, fmt.Errorf("%w — run `brunnr-code sync --help` for the supported flags", err)
	}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "skill", "skills":
			opts.skillsSet = true
		case "strict-tdd":
			opts.strictTDDSet = true
		case "include-permissions":
			opts.permissionsSet = true
		case "include-theme":
			opts.themeSet = true
		}
	})

	if opts.strictTDDSet {
		return SyncFlags{}, fmt.Errorf("--strict-tdd is retired: ODD uses applicable test-first development by default; rerun `brunnr-code sync` without --strict-tdd (retain any other flags)")
	}

	if fs.NArg() > 0 {
		return SyncFlags{}, fmt.Errorf("unexpected sync argument %q — pass agents with the --agent %s flag, not a positional argument", fs.Arg(0), fs.Arg(0))
	}

	return opts, nil
}

func PrintSyncHelp(w io.Writer) {
	fmt.Fprint(w, `USAGE
  brunnr-code sync [flags]

FLAGS
  --agent, --agents <list>           Agents to sync
  --skill, --skills <list>           Skills to sync
  --strict-tdd                       Retired (rejected); applicable test-first ODD is default
  --include-permissions              Include permissions component
  --include-theme                    Include theme component
  --scope global|workspace           Sync scope (env: BRUNNR_CODE_INSTALL_SCOPE)
                                     workspace refreshes only workspace-scoped files and never mutates global state, backups, plugins, or routing guidance; components managed only globally are skipped
  --dry-run                          Preview plan without executing
  --help, -h                         Show this help
`)
}

// BuildSyncSelection builds a model.Selection for the sync command.
//
// Default sync scope: Engram, Context7, Skills, Persona.
// Excluded by default: Permissions, Theme (no markers; managed via JSON
// overlays where user customization cannot be safely diff-merged).
// Permissions and Theme can be opted-in via flags.
//
// Persona is included because its content lives between
// <!-- brunnr-code:persona --> markers — that block is harness-managed and
// must propagate embedded-asset changes across versions. Content outside
// the markers (user-authored sections) is preserved by InjectMarkdownSection.
//
// This is the reusable managed-asset sync contract. A future `upgrade --sync`
// flow can call this function to get the same managed-only selection semantics.
func BuildSyncSelection(flags SyncFlags, agentIDs []model.AgentID) model.Selection {
	// Persona runs first so its marker section precedes the sections that
	// Engram/MCP inject afterwards.
	components := []model.ComponentID{
		model.ComponentPersona,
		model.ComponentEngram,
		model.ComponentContext7,
		model.ComponentSkills,
	}

	if flags.IncludePermissions {
		components = append(components, model.ComponentPermission)
	}
	if flags.IncludeTheme {
		components = append(components, model.ComponentTheme)
	}

	var skillIDs []model.SkillID
	for _, raw := range flags.Skills {
		skillIDs = append(skillIDs, model.SkillID(raw))
	}

	return model.Selection{
		Agents:     agentIDs,
		Components: components,
		Skills:     skillIDs,
		// Preset is set to full so selectedSkillIDs() returns the
		// correct default skill set when no explicit skills are provided.
		Preset: model.PresetFull,
		// Persona is left as zero-value here. RunSync resolves it from state.json
		// when present. A missing persona field resolves to neutral; invalid state
		// is rejected so sync cannot silently reactivate regional persona behavior.
	}
}

// installOrderedComponents returns the selected components in the order the
// install planner applies them. Persisted state records components in
// selection order (persona last for full), and components write the
// same files: persona replaces the whole prompt for FileReplace agents, and
// Engram re-appends its MCP block after Context7. Applying them in a different
// order than install makes the first sync rewrite what install just wrote.
// Components the planner does not know keep their relative order at the end.
func installOrderedComponents(components []model.ComponentID) []model.ComponentID {
	graph := planner.MVPGraph()
	known := make([]model.ComponentID, 0, len(components))
	for _, component := range components {
		if graph.Has(component) {
			known = append(known, component)
		}
	}
	resolved, err := planner.NewResolver(graph).Resolve(model.Selection{Components: known})
	if err != nil {
		return components
	}
	ordered := make([]model.ComponentID, 0, len(components))
	for _, component := range resolved.OrderedComponents {
		if slices.Contains(components, component) && !slices.Contains(ordered, component) {
			ordered = append(ordered, component)
		}
	}
	for _, component := range components {
		if !slices.Contains(ordered, component) {
			ordered = append(ordered, component)
		}
	}
	return ordered
}

func RestorePersistedSelection(selection *model.Selection, persisted state.InstallState, flags SyncFlags) {
	if !persisted.SelectionConfigured {
		return
	}
	explicit := *selection
	persisted.RestoreSelection(selection)
	if flags.skillsSet {
		selection.Skills = explicit.Skills
		setSelectionComponent(selection, model.ComponentSkills, true, true)
	}
	setSelectionComponent(selection, model.ComponentPermission, flags.permissionsSet, flags.IncludePermissions)
	setSelectionComponent(selection, model.ComponentTheme, flags.themeSet, flags.IncludeTheme)
}

func setSelectionComponent(selection *model.Selection, component model.ComponentID, configured, included bool) {
	if !configured {
		return
	}
	filtered := selection.Components[:0]
	for _, current := range selection.Components {
		if current != component {
			filtered = append(filtered, current)
		}
	}
	selection.Components = filtered
	if included {
		selection.Components = append(selection.Components, component)
	}
}

// DiscoverAgents returns the agent IDs to sync. Persisted selections are
// authoritative, including an explicitly configured empty selection. Missing or
// incidental state falls back to filesystem discovery; unreadable state fails
// closed so sync cannot inject into unselected agent configuration.
//
// When --agents is provided explicitly, callers should pass those IDs directly
// instead of calling DiscoverAgents.
func DiscoverAgents(homeDir string) []model.AgentID {
	scope := agents.ReadSelectionScope(homeDir)
	switch scope.Mode {
	case agents.SelectionScopeConfigured:
		return scope.AgentIDs
	case agents.SelectionScopeUnavailable:
		return nil
	}

	// Fallback: filesystem discovery for legacy installs and incidental state
	// written before an installation records a user selection.
	reg, err := agents.NewDefaultRegistry()
	if err != nil {
		// Registry construction only fails if a duplicate adapter is registered,
		// which would indicate a programming error. Treat as no agents found
		// rather than propagating — callers treat an empty result as a no-op.
		return nil
	}

	installed := agents.DiscoverInstalled(reg, homeDir)
	ids := make([]model.AgentID, 0, len(installed))
	for _, a := range installed {
		ids = append(ids, a.ID)
	}
	return ids
}

// syncRuntime mirrors installRuntime but builds a sync-scoped StagePlan.
// It reuses backup/rollback infrastructure but only calls inject functions —
// no agentInstallStep, no engram setup, no persona.
type syncRuntime struct {
	homeDir              string
	workspaceDir         string
	scope                InstallScope
	selection            model.Selection
	agentIDs             []model.AgentID
	backupRoot           string
	workspaceSnapshotDir string
	state                *runtimeState
	managedPaths         []string
	changedFiles         []string // accumulates candidate paths reported by component injectors
	skippedActions       []string // workspace-scope skips of global-only operations, surfaced as manual actions
	skippedParts         []SyncSkippedAgent
}

// newSyncRuntimeWithScope builds the sync runtime for the requested scope.
// ScopeWorkspace never touches the global backup store: the rollback snapshot
// lives in a temporary transaction directory that is removed when the run ends
// (issue #1074), and the global compatibility-skills transaction is not
// created because the shared compatibility tree is global-only.
func newSyncRuntimeWithScope(homeDir string, selection model.Selection, scope InstallScope) (*syncRuntime, error) {
	workspaceDir, _ := os.Getwd()
	backupRoot := state.BackupsDir(homeDir)
	state := &runtimeState{}
	if scope == ScopeWorkspace {
		snapshotDir, err := os.MkdirTemp("", "brunnr-code-rollback-*")
		if err != nil {
			return nil, fmt.Errorf("create workspace sync snapshot directory: %w", err)
		}
		state.rollbackSnapshotDir = snapshotDir
		backupRoot = ""
	} else {
		compatibilityTransaction, err := newCompatibilityRefreshTransaction(homeDir, selection.Components, selection)
		if err != nil {
			return nil, err
		}
		state.compatibilityTransaction = compatibilityTransaction
	}

	runtime := &syncRuntime{
		homeDir:              homeDir,
		workspaceDir:         workspaceDir,
		scope:                scope,
		selection:            selection,
		agentIDs:             selection.Agents,
		backupRoot:           backupRoot,
		workspaceSnapshotDir: state.rollbackSnapshotDir,
		state:                state,
	}
	return runtime, nil
}

func (r *syncRuntime) stagePlan() pipeline.StagePlan {
	adapters := resolveAdapters(r.agentIDs)
	targets, targetErr := syncBackupTargetsScoped(r.homeDir, r.workspaceDir, r.scope, r.selection, adapters)
	r.managedPaths = targets

	snapshotDir := filepath.Join(r.backupRoot, time.Now().UTC().Format("20060102150405.000000000"))
	if r.scope == ScopeWorkspace && r.workspaceSnapshotDir != "" {
		snapshotDir = r.workspaceSnapshotDir
	}
	prepare := []pipeline.Step{
		prepareBackupStep{
			id:          "prepare:backup-snapshot",
			snapshotter: backup.NewSnapshotter(),
			snapshotDir: snapshotDir,
			targets:     targets,
			targetErr:   targetErr,
			state:       r.state,
			backupRoot:  r.backupRoot,
			source:      backup.BackupSourceSync,
			description: "pre-sync snapshot",
			appVersion:  AppVersion,
		},
	}

	apply := []pipeline.Step{
		rollbackRestoreStep{id: "apply:rollback-restore", state: r.state, homeDir: r.homeDir, workspaceDir: r.workspaceDir},
	}

	for _, component := range installOrderedComponents(r.selection.Components) {
		apply = append(apply, componentSyncStep{
			id:           "sync:component:" + string(component),
			component:    component,
			homeDir:      r.homeDir,
			workspaceDir: r.workspaceDir,
			scope:        r.scope,
			agents:       r.agentIDs,
			selection:    r.selection,
			changedFiles: &r.changedFiles,
			skipped:      &r.skippedActions,
			state:        r.state,
		})
	}
	if r.scope == ScopeGlobal && needsCompatibilitySkillsRefresh(r.selection.Components) {
		apply = append(apply, compatibilitySkillsRefreshStep{
			id:           "sync:compatibility-skills-refresh",
			homeDir:      r.homeDir,
			components:   r.selection.Components,
			selection:    r.selection,
			changedFiles: &r.changedFiles,
			transaction:  r.state.compatibilityTransaction,
			anchored:     usesAnchoredCompatibilityTransaction(),
		})
	}

	for _, agent := range r.agentIDs {
		if nativeReviewAgentSupported(agent) {
			apply = append(apply, nativeReviewAgentStep{id: "sync:agent:native-review:" + string(agent), agent: agent, homeDir: r.homeDir, workspaceDir: r.workspaceDir, scope: r.scope, selection: r.selection, changedFiles: &r.changedFiles, state: r.state})
		}
	}

	// Routing guidance is refreshed per agent and outside the component loop, for
	// the same reason install schedules it there: a persisted selection without
	// any optional component must still leave every agent able to choose an
	// implementation route (issue #1794). It runs after the components so their
	// managed assets are in place before guidance is merged.
	for _, agent := range r.agentIDs {
		if r.scope == ScopeWorkspace && workspaceRoutingGuidanceGlobalOnly(agent) {
			// The routing guidance step couples prompt guidance with
			// home-level state mutation for these agents (global orchestrator
			// settings, hook installation). A workspace
			// sync must never mutate the home root, so the operation is
			// skipped, not leaked.
			r.skippedActions = append(r.skippedActions, fmt.Sprintf("workspace scope skipped routing guidance for %s: managed only in the global scope", agent))
			continue
		}
		apply = append(apply, agentRoutingGuidanceStep{
			codexPhaseModels: r.selection.CodexPhaseModelAssignments,
			codexEfforts:     r.selection.CodexModelAssignments,
			codexCarrils:     r.selection.CodexCarrilModelAssignments,
			id:               "sync:agent-guidance:" + string(agent),
			agent:            agent,
			homeDir:          r.homeDir,
			workspaceDir:     r.workspaceDir,
			scope:            r.scope,
			changedFiles:     &r.changedFiles,
		})
	}

	if r.scope == ScopeGlobal && r.selection.HasCommunityTool(model.CommunityToolCodeGraph) {
		apply = append(apply, &codeGraphGuidanceSyncStep{
			id:                 "sync:community-tool:codegraph-guidance",
			homeDir:            r.homeDir,
			runner:             codeGraphHomeRunner{homeDir: r.homeDir},
			changedFiles:       &r.changedFiles,
			guidanceBeforeSync: communitytool.HasAnyCodeGraphGuidance(r.homeDir),
		})
	}

	return pipeline.StagePlan{Prepare: prepare, Apply: apply}
}

// syncBackupTargets returns the file paths that need to be backed up
// before sync executes. Uses syncComponentPaths so that the backup/verify
// contract matches the actual files sync touches (which differ from install
// for ComponentPersona, see syncComponentPaths). One deliberate exception:
// persona backup also captures the non-selected managed output-style file so a
// failed persona switch can be rolled back (verification still declares only
// the selected file).
// syncBackupTargetsScoped returns the file paths that need to be backed up
// before a scoped sync executes. ScopeWorkspace declares only workspace-scoped
// targets: global-only components, plugins, routing guidance, compatibility
// skills, CodeGraph tooling, and background launchers are skipped so a
// workspace sync never snapshots — or rolls back — global state (issue #1074).
func syncBackupTargetsScoped(homeDir, workspaceDir string, scope InstallScope, selection model.Selection, adapters []agents.Adapter) ([]string, error) {
	workspace := scope == ScopeWorkspace
	paths := map[string]struct{}{}
	for _, component := range selection.Components {
		if workspace && workspaceGlobalOnlyComponent(component) {
			continue
		}
		componentAdapters := adapters
		if workspace {
			componentAdapters = workspaceScopedAdapters(component, adapters)
		}
		for _, path := range syncComponentPathsWithWorkspaceScoped(homeDir, workspaceDir, scope, selection, componentAdapters, component) {
			paths[path] = struct{}{}
		}
		if component == model.ComponentContext7 {
			for _, path := range claudeMCPSettingsCleanupPaths(homeDir, workspaceDir, scope, componentAdapters) {
				paths[path] = struct{}{}
			}
		}
		if component == model.ComponentEngram {
			for _, adapter := range componentAdapters {
				if adapter.Agent() == model.AgentClaudeCode {
					// Scope the declared engram MCP target like the sync step
					// writes it: workspace scope uses the workspace MCP config
					// (<workspace>/.claude/mcp/engram.json); global scope keeps
					// the user registry migration source.
					mcpRoot := componentInjectionDirScoped(homeDir, workspaceDir, scope, adapter)
					paths[adapter.MCPConfigPath(mcpRoot, "engram")] = struct{}{}
				}
			}
		}
		if component == model.ComponentPersona {
			for _, adapter := range componentAdapters {
				if !adapter.SupportsOutputStyles() {
					continue
				}
				targetDir := componentInjectionDirScoped(homeDir, workspaceDir, scope, adapter)
				if path := persona.OutputStylePath(adapter.OutputStyleDir(targetDir)); path != "" {
					paths[path] = struct{}{}
				}
			}
		}
	}
	// Routing guidance is refreshed per agent outside the component loop, at
	// the sync scope like the step itself. A persisted selection whose components
	// do not cover the same file would otherwise be rewritten without a
	// snapshot and could never be rolled back (issue #1794). Agents whose
	// routing step mutates home-level state are skipped under ScopeWorkspace,
	// exactly like the guidance step skips them.
	for _, adapter := range adapters {
		if workspace && workspaceRoutingGuidanceGlobalOnly(adapter.Agent()) {
			continue
		}
		if path := agentguidance.StrictTDDPath(routingGuidanceDir(homeDir, workspaceDir, scope, adapter), adapter.Agent()); path != "" {
			paths[path] = struct{}{}
		}
	}
	guidanceAdapters := adapters
	if workspace {
		guidanceAdapters = nil
		for _, adapter := range adapters {
			if workspaceRoutingGuidanceGlobalOnly(adapter.Agent()) {
				continue
			}
			guidanceAdapters = append(guidanceAdapters, adapter)
		}
	}
	for _, path := range routingGuidancePaths(homeDir, workspaceDir, scope, guidanceAdapters) {
		paths[path] = struct{}{}
	}
	for _, adapter := range guidanceAdapters {
		// The routing step installs the retained review and
		// skill-registry hooks into the home Claude settings, whatever
		// optional components were selected.
		if adapter.Agent() == model.AgentClaudeCode {
			if path := adapter.SettingsPath(homeDir); path != "" {
				paths[path] = struct{}{}
			}
		}
	}
	for _, adapter := range adapters {
		if names := reviewassets.NativeAgentFileNames(adapter.Agent()); len(names) > 0 {
			dir := adapter.SubAgentsDir(componentInjectionDirScoped(homeDir, workspaceDir, scope, adapter))
			paths[filepath.Join(dir, reviewassets.OwnershipLedgerFilename)] = struct{}{}
			for _, name := range names {
				paths[filepath.Join(dir, name)] = struct{}{}
			}
		}
		if adapter.Agent() == model.AgentCodex {
			paths[filepath.Join(adapter.GlobalConfigDir(componentInjectionDirScoped(homeDir, workspaceDir, scope, adapter)), "hooks.json")] = struct{}{}
		}
	}
	adapterSkillPaths, err := syncAdapterSkillBackupTargetsScoped(homeDir, workspaceDir, scope, selection, adapters)
	if err != nil {
		return nil, err
	}
	for _, path := range adapterSkillPaths {
		paths[path] = struct{}{}
	}
	if !workspace && !usesAnchoredCompatibilityTransaction() && needsCompatibilitySkillsRefresh(selection.Components) {
		skillDir, ok, err := compatibilitySkillsDir(homeDir)
		if err != nil {
			return nil, err
		}
		if ok {
			compatibilityPaths, err := compatibilitySkillFiles(skillDir, selection.Components, selection)
			if err != nil {
				return nil, err
			}
			for _, path := range compatibilityPaths {
				paths[path] = struct{}{}
			}
		}
	}
	if !workspace && selection.HasCommunityTool(model.CommunityToolCodeGraph) {
		for _, path := range communitytool.CodeGraphManagedPaths(homeDir) {
			paths[path] = struct{}{}
		}
	}
	targets := make([]string, 0, len(paths))
	for path := range paths {
		if workspace && pathUnderHomeOnly(path, homeDir, workspaceDir) {
			// A workspace sync never writes home-only files, so they must not
			// enter the rollback contract either: a failed workspace run could
			// otherwise restore global state (issue #1074). This covers every
			// declaration source, including shared component and cleanup path
			// helpers that still resolve the global settings authority.
			continue
		}
		targets = append(targets, path)
	}
	sort.Strings(targets)
	return targets, nil
}

func syncAdapterSkillBackupTargetsScoped(homeDir, workspaceDir string, scope InstallScope, selection model.Selection, adapters []agents.Adapter) ([]string, error) {
	var paths []string
	for _, adapter := range adapters {
		if !adapter.SupportsSkills() {
			continue
		}
		if slices.Contains(selection.Components, model.ComponentSkills) {
			skillDir := adapter.SkillsDir(componentInjectionDirScoped(homeDir, workspaceDir, scope, adapter))
			if skillDir == "" {
				continue
			}
			ordinary, err := skills.DirectoryPaths(skillDir, selectedSkillIDs(selection), "")
			if err != nil {
				return nil, fmt.Errorf("enumerate %s skill backup targets: %w", adapter.Agent(), err)
			}
			paths = append(paths, ordinary...)
			support, err := skillSupportBackupTargets(componentInjectionDirScoped(homeDir, workspaceDir, scope, adapter), adapter, selection)
			if err != nil {
				return nil, err
			}
			paths = append(paths, support...)
		}
	}
	return paths, nil
}

// pathUnderHomeOnly reports whether a declared managed path resolves under
// the home root without being inside the workspace. Workspace-scoped syncs
// never write there, so such declarations must not become workspace
// verification targets.
func pathUnderHomeOnly(path, homeDir, workspaceDir string) bool {
	if pathUnder(path, workspaceDir) {
		return false
	}
	return pathUnder(path, homeDir)
}

func pathUnder(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != string(filepath.Separator)
}

// workspaceGlobalOnlyComponent reports whether a sync component is managed
// only in the global scope, so a workspace sync must skip it instead of
// leaking writes into the user's home (issue #1074).
func workspaceGlobalOnlyComponent(component model.ComponentID) bool {
	switch component {
	case model.ComponentClaudeTheme:
		return true
	}
	return false
}

// workspaceAdapterManagedGlobally reports whether the component's sync writes
// for a specific agent land in the global config root regardless of scope, so
// a workspace sync must skip that adapter instead of leaking (issue #1074).
func workspaceAdapterManagedGlobally(component model.ComponentID, agent model.AgentID) bool {
	switch component {
	case model.ComponentPermission, model.ComponentTheme:
		// Every agent writes its global settings file.
		return true
	}
	return false
}

// workspaceRoutingGuidanceGlobalOnly reports whether an agent's routing
// guidance step mutates home-level state regardless of scope: home-targeted
// hook installation (Claude Code review hooks, Codex skill-registry
// hooks). The step couples those global writes with prompt guidance and
// lives outside this file's scope, so a workspace sync skips the whole step
// instead of leaking (issue #1074).
func workspaceRoutingGuidanceGlobalOnly(agent model.AgentID) bool {
	switch agent {
	case model.AgentClaudeCode, model.AgentCodex:
		return true
	}
	return false
}

// workspaceScopedAdapters drops the adapters whose sync writes for the
// component are managed only in the global scope. Used by both the component
// step and the backup/verification path declarations so they cannot drift.
func workspaceScopedAdapters(component model.ComponentID, adapters []agents.Adapter) []agents.Adapter {
	scoped := make([]agents.Adapter, 0, len(adapters))
	for _, adapter := range adapters {
		if workspaceAdapterManagedGlobally(component, adapter.Agent()) {
			continue
		}
		scoped = append(scoped, adapter)
	}
	return scoped
}

// syncComponentPathsWithWorkspaceScoped declares the file paths a scoped sync
// writes for a component. Global scope matches syncComponentPathsWithWorkspace;
// workspace scope drops globally-managed adapters (see workspaceScopedAdapters).
func syncComponentPathsWithWorkspaceScoped(homeDir, workspaceDir string, scope InstallScope, selection model.Selection, adapters []agents.Adapter, component model.ComponentID) []string {
	if component == model.ComponentPersona {
		return syncPersonaPathsWithWorkspaceScoped(homeDir, workspaceDir, scope, selection, adapters)
	}
	return componentPathsWithWorkspaceScoped(homeDir, workspaceDir, scope, selection, adapters, component)
}

// syncPersonaPathsWithWorkspaceScoped resolves persona sync paths for the
// requested scope. Workspace scope resolves targetDir through
// componentInjectionDirScoped so the backup and verification contracts match
// the actual workspace persona writes (issue #1074).
func syncPersonaPathsWithWorkspaceScoped(homeDir, workspaceDir string, scope InstallScope, selection model.Selection, adapters []agents.Adapter) []string {
	paths := []string{}
	for _, adapter := range adapters {
		targetDir := componentInjectionDirScoped(homeDir, workspaceDir, scope, adapter)
		if !adapter.SupportsSystemPrompt() {
			continue
		}
		paths = append(paths, adapter.SystemPromptFile(targetDir))
		if adapter.SupportsOutputStyles() {
			if stylePath := persona.OutputStylePath(adapter.OutputStyleDir(targetDir)); stylePath != "" {
				paths = append(paths, stylePath)
				if p := adapter.SettingsPath(targetDir); p != "" {
					paths = append(paths, p)
				}
			}
		}
	}
	return paths
}

type componentSyncStep struct {
	id           string
	component    model.ComponentID
	homeDir      string
	workspaceDir string
	scope        InstallScope
	agents       []model.AgentID
	selection    model.Selection
	changedFiles *[]string // accumulates absolute paths of files that actually changed
	skipped      *[]string // accumulates workspace-scope skip notices for global-only operations
	state        *runtimeState

	backgroundPolicy bool
}

type codeGraphGuidanceSyncStep struct {
	id           string
	homeDir      string
	runner       communitytool.Runner
	changedFiles *[]string
	before       map[string]syncFileSnapshot
	// guidanceBeforeSync records whether CodeGraph guidance existed before the
	// sync pipeline ran. Persona may replace whole prompt files earlier in the
	// same sync, so detecting guidance at Run time would miss it and drop it.
	guidanceBeforeSync bool
}

func (s *codeGraphGuidanceSyncStep) ID() string {
	return s.id
}

func (s *codeGraphGuidanceSyncStep) Run() (runErr error) {
	before, err := snapshotSyncFiles(communitytool.CodeGraphManagedPaths(s.homeDir))
	if err != nil {
		return err
	}
	s.before = before
	defer func() {
		if runErr != nil {
			runErr = errors.Join(runErr, restoreSyncFiles(s.before))
		}
	}()

	status := communitytool.DetectStatus(model.CommunityToolCodeGraph, s.homeDir, communitytool.DetectorFunc(cmdLookPath))
	res, configured, err := communitytool.RefreshCodeGraphGuidanceIfConfigured(s.homeDir, communitytool.DetectorFunc(cmdLookPath))
	if err == nil && !configured && s.guidanceBeforeSync && status.CLI == communitytool.AvailabilityAvailable {
		res, err = communitytool.InjectCodeGraphGuidance(s.homeDir)
		configured = true
	}
	if err != nil {
		return fmt.Errorf("sync CodeGraph guidance: %w", err)
	}
	if !configured {
		res, err = communitytool.CleanLegacyCodeGraphGuidance(s.homeDir)
		if err != nil {
			return fmt.Errorf("sync legacy CodeGraph guidance cleanup: %w", err)
		}
	}
	if s.changedFiles != nil && res.Changed {
		*s.changedFiles = append(*s.changedFiles, res.Files...)
	}
	return nil
}

func (s *codeGraphGuidanceSyncStep) Rollback() error {
	return restoreSyncFiles(s.before)
}

type codeGraphHomeRunner struct {
	homeDir string
}

func (r codeGraphHomeRunner) Run(name string, args ...string) error {
	command := exec.Command(name, args...)
	system.EnsureCommandDir(command)
	actualHome, _ := os.UserHomeDir()
	if filepath.Clean(r.homeDir) != filepath.Clean(actualHome) {
		command.Env = overrideCommandEnvironment(os.Environ(), map[string]string{
			"HOME":            r.homeDir,
			"XDG_CONFIG_HOME": codeGraphConfigHome(r.homeDir),
		})
	}
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return fmt.Errorf("%w: %s", err, message)
		}
	}
	return err
}

// codeGraphConfigHome resolves the XDG config root CodeGraph uses for
// homeDir: an absolute XDG_CONFIG_HOME applies only to the real user home.
func codeGraphConfigHome(homeDir string) string {
	if xdgConfigHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); filepath.IsAbs(xdgConfigHome) {
		userHome, err := os.UserHomeDir()
		if err == nil && filepath.Clean(homeDir) == filepath.Clean(userHome) {
			return xdgConfigHome
		}
	}
	return filepath.Join(homeDir, ".config")
}

func overrideCommandEnvironment(environment []string, overrides map[string]string) []string {
	result := make([]string, 0, len(environment)+len(overrides))
	for _, entry := range environment {
		key, _, found := strings.Cut(entry, "=")
		if found {
			if _, overridden := overrides[key]; overridden {
				continue
			}
		}
		result = append(result, entry)
	}
	for key, value := range overrides {
		result = append(result, key+"="+value)
	}
	return result
}

func (s componentSyncStep) ID() string {
	return s.id
}

// skipWorkspace records that a global-only sync operation was skipped under
// ScopeWorkspace instead of leaking a write into the user's home (#1074).
func (s componentSyncStep) skipWorkspace(component model.ComponentID, agent model.AgentID) {
	if s.skipped == nil {
		return
	}
	message := fmt.Sprintf("workspace scope skipped %s", component)
	if agent != "" {
		message += fmt.Sprintf(" for %s", agent)
	}
	*s.skipped = append(*s.skipped, message+": managed only in the global scope")
}

func (s componentSyncStep) Run() error {
	adapters := resolveAdapters(s.agents)

	if s.scope == ScopeWorkspace {
		if workspaceGlobalOnlyComponent(s.component) {
			s.skipWorkspace(s.component, "")
			return nil
		}
		for _, adapter := range adapters {
			if workspaceAdapterManagedGlobally(s.component, adapter.Agent()) {
				s.skipWorkspace(s.component, adapter.Agent())
			}
		}
		adapters = workspaceScopedAdapters(s.component, adapters)
	}

	switch s.component {
	case model.ComponentEngram:
		// Sync: inject MCP config + system prompt protocol only.
		// NO binary install. NO engram setup.
		//
		// Resolve the installed engram version exactly like the install path
		// (internal/cli/run.go) so InjectOptions.Version feeds the same
		// Decision 1 slim/full gate (bug #1824): without it every sync
		// silently re-inflated the slim Claude Code engram-protocol section
		// back to the full one. Errors are intentionally ignored — an empty
		// version safely falls back to the full section.
		engramVersion, _ := resolveEngramVersion("engram")
		engramOpts := engram.InjectOptions{
			CodexOrchestratorAssignment: s.selection.CodexOrchestratorAssignment,
			CodexServiceTier:            s.selection.CodexServiceTier,
			CodexManagedServiceTier:     s.selection.CodexManagedServiceTier,
			CodexCarrilModelAssignments: s.selection.CodexCarrilModelAssignments,
			CodexModelAssignments:       s.selection.CodexModelAssignments,
			Version:                     engramVersion,
		}
		for _, adapter := range adapters {
			var res engram.InjectionResult
			var err error
			targetDir := componentInjectionDirScoped(s.homeDir, s.workspaceDir, s.scope, adapter)
			if s.scope == ScopeWorkspace {
				// The workspace delivery is the purpose-built scoped API
				// (userScope=false): it writes the adapter's workspace MCP
				// config — Claude Code: <workspace>/.claude/mcp/engram.json —
				// while the user-scope API would write the user registry
				// (<workspace>/.claude.json). Mirrors install (issue #1074).
				res, err = engram.InjectWorkspaceWithOptions(targetDir, adapter, engramOpts)
			} else {
				res, err = engram.InjectWithOptions(targetDir, adapter, engramOpts)
				s.state.noteCodexServiceTier(res)
			}
			if err != nil {
				return fmt.Errorf("sync engram for %q: %w", adapter.Agent(), err)
			}
			s.countChanged(boolToInt(res.Changed), res.Files...)
		}
		return nil

	case model.ComponentContext7:
		for _, adapter := range adapters {
			targetDir := componentInjectionDirScoped(s.homeDir, s.workspaceDir, s.scope, adapter)
			res, err := mcp.Inject(s.homeDir, targetDir, adapter)
			if err != nil {
				return fmt.Errorf("sync context7 for %q: %w", adapter.Agent(), err)
			}
			s.countChanged(boolToInt(res.Changed), res.Files...)
		}
		return nil

	case model.ComponentSkills:
		skillIDs := selectedSkillIDs(s.selection)
		if len(skillIDs) == 0 {
			return nil
		}
		for _, adapter := range adapters {
			res, err := skills.Inject(componentInjectionDirScoped(s.homeDir, s.workspaceDir, s.scope, adapter), adapter, skillIDs)
			if err != nil {
				return fmt.Errorf("sync skills for %q: %w", adapter.Agent(), err)
			}
			s.countChanged(boolToInt(res.Changed), res.Files...)
		}
		return nil

	case model.ComponentPermission:
		// Opt-in only — reached when --include-permissions is set.
		for _, adapter := range adapters {
			res, err := permissions.Inject(s.homeDir, adapter)
			if err != nil {
				return fmt.Errorf("sync permissions for %q: %w", adapter.Agent(), err)
			}
			s.countChanged(boolToInt(res.Changed), res.Files...)
		}
		return nil

	case model.ComponentPersona:
		// Sync regenerates the persona block between
		// <!-- brunnr-code:persona --> markers and (when supported) refreshes
		// the managed output-style overlay.
		for _, adapter := range adapters {
			targetDir := componentInjectionDirScoped(s.homeDir, s.workspaceDir, s.scope, adapter)
			res, err := injectSyncPersona(targetDir, adapter)
			if err != nil {
				return fmt.Errorf("sync persona for %q: %w", adapter.Agent(), err)
			}
			s.countChanged(boolToInt(res.Changed), res.Files...)
		}
		return nil

	case model.ComponentTheme:
		// Opt-in only — reached when --include-theme is set.
		for _, adapter := range adapters {
			res, err := theme.Inject(s.homeDir, adapter)
			if err != nil {
				return fmt.Errorf("sync theme for %q: %w", adapter.Agent(), err)
			}
			s.countChanged(boolToInt(res.Changed), res.Files...)
		}
		return nil

	case model.ComponentClaudeTheme:
		for _, adapter := range adapters {
			res, err := theme.InjectVisualThemes(s.homeDir, adapter)
			if err != nil {
				return fmt.Errorf("sync visual themes for %q: %w", adapter.Agent(), err)
			}
			s.countChanged(boolToInt(res.Changed), res.Files...)
		}
		return nil

	default:
		return fmt.Errorf("component %q is not supported in sync runtime", s.component)
	}
}

// countChanged records candidate changed paths from an aggregate injector result.
func (s componentSyncStep) countChanged(n int, files ...string) {
	if s.changedFiles != nil && n > 0 {
		*s.changedFiles = append(*s.changedFiles, files...)
	}
}

// dedupPaths removes duplicate and empty paths while preserving first-seen order.
func dedupPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	return out
}

type syncFileSnapshot struct {
	exists       bool
	data         []byte
	mode         os.FileMode
	symlink      bool
	linkTarget   string
	targetPath   string
	targetExists bool
}

var writeSyncFileAtomic = filemerge.WriteFileAtomicMode

func syncRestoreWriteMode(mode os.FileMode) os.FileMode {
	if mode.Perm() == 0 {
		return 0o600
	}
	return mode
}

func snapshotSyncFiles(paths []string) (map[string]syncFileSnapshot, error) {
	snapshots := make(map[string]syncFileSnapshot, len(paths))
	for _, path := range dedupPaths(paths) {
		info, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				snapshots[path] = syncFileSnapshot{}
				continue
			}
			return nil, fmt.Errorf("inspect managed sync file %q: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return nil, fmt.Errorf("read managed sync symlink %q: %w", path, err)
			}
			targetPath := linkTarget
			if !filepath.IsAbs(targetPath) {
				targetPath = filepath.Join(filepath.Dir(path), targetPath)
			}
			targetPath, targetInfo, targetExists, err := resolveSyncSymlinkTarget(targetPath)
			if err != nil {
				return nil, fmt.Errorf("resolve managed sync symlink target for %q: %w", path, err)
			}
			if !targetExists {
				snapshots[path] = syncFileSnapshot{exists: true, symlink: true, linkTarget: linkTarget, targetPath: targetPath}
				continue
			}
			data, err := os.ReadFile(targetPath)
			if err != nil {
				return nil, fmt.Errorf("snapshot managed sync symlink target %q: %w", targetPath, err)
			}
			snapshots[path] = syncFileSnapshot{
				exists:       true,
				data:         data,
				mode:         targetInfo.Mode().Perm(),
				symlink:      true,
				linkTarget:   linkTarget,
				targetPath:   targetPath,
				targetExists: true,
			}
			continue
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("snapshot managed sync file %q: %w", path, err)
		}
		snapshots[path] = syncFileSnapshot{exists: true, data: data, mode: info.Mode().Perm()}
	}
	return snapshots, nil
}

func resolveSyncSymlinkTarget(path string) (string, os.FileInfo, bool, error) {
	current, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", nil, false, err
	}
	seen := map[string]struct{}{}
	for {
		if _, exists := seen[current]; exists {
			return "", nil, false, fmt.Errorf("symlink cycle at %q", current)
		}
		seen[current] = struct{}{}
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return current, nil, false, nil
			}
			return "", nil, false, err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return current, info, true, nil
		}
		next, err := os.Readlink(current)
		if err != nil {
			return "", nil, false, err
		}
		if !filepath.IsAbs(next) {
			next = filepath.Join(filepath.Dir(current), next)
		}
		current = filepath.Clean(next)
	}
}

func restoreSyncFiles(snapshots map[string]syncFileSnapshot) error {
	var restoreErr error
	for path, snapshot := range snapshots {
		if !snapshot.exists {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				restoreErr = errors.Join(restoreErr, fmt.Errorf("remove newly created sync file %q: %w", path, err))
			}
			continue
		}
		mode := snapshot.mode
		writeMode := syncRestoreWriteMode(mode)
		if snapshot.symlink {
			if snapshot.targetExists {
				if _, err := writeSyncFileAtomic(snapshot.targetPath, snapshot.data, writeMode); err != nil {
					restoreErr = errors.Join(restoreErr, fmt.Errorf("restore sync symlink target %q: %w", snapshot.targetPath, err))
					continue
				}
				if err := os.Chmod(snapshot.targetPath, mode); err != nil {
					restoreErr = errors.Join(restoreErr, fmt.Errorf("restore sync symlink target mode %q: %w", snapshot.targetPath, err))
					continue
				}
			} else {
				if err := os.Remove(snapshot.targetPath); err != nil && !os.IsNotExist(err) {
					restoreErr = errors.Join(restoreErr, fmt.Errorf("remove newly created sync symlink target %q: %w", snapshot.targetPath, err))
					continue
				}
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				restoreErr = errors.Join(restoreErr, fmt.Errorf("replace managed sync symlink %q: %w", path, err))
				continue
			}
			if err := os.Symlink(snapshot.linkTarget, path); err != nil {
				restoreErr = errors.Join(restoreErr, fmt.Errorf("restore managed sync symlink %q: %w", path, err))
			}
			continue
		}
		if _, err := writeSyncFileAtomic(path, snapshot.data, writeMode); err != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("restore sync file %q: %w", path, err))
			continue
		}
		if err := os.Chmod(path, mode); err != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("restore sync file mode %q: %w", path, err))
		}
	}
	return restoreErr
}

func changedSyncFiles(candidates []string, before map[string]syncFileSnapshot) ([]string, error) {
	var changed []string
	for _, path := range dedupPaths(candidates) {
		after, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				if before[path].exists {
					changed = append(changed, path)
				}
				continue
			}
			return nil, fmt.Errorf("compare managed sync file %q: %w", path, err)
		}
		previous := before[path]
		if !previous.exists || !bytes.Equal(after, previous.data) {
			changed = append(changed, path)
		}
	}
	return changed, nil
}

// boolToInt converts a boolean to 0 or 1.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// validatePersistedSyncState rejects state that cannot safely drive sync.
// A missing state file is allowed for fresh homes.
func validatePersistedSyncState(readErr error) error {
	// guard:population persisted-sync-state-integrity fail-closed: legitimate persisted sync state is a missing file or a decoded state; read/decode errors remain excluded
	if readErr != nil && !os.IsNotExist(readErr) {
		return fmt.Errorf("read persisted installation state: %w", readErr)
	}
	return nil
}

// RunSyncWithSelection is the programmatic entry point for sync.
// It skips flag parsing and agent discovery — the caller provides the homeDir
// and a fully-built Selection (agents + components + options).
// This is the function the TUI calls directly to avoid CLI flag parsing.
func RunSyncWithSelection(homeDir string, selection model.Selection) (SyncResult, error) {
	return RunSyncWithSelectionScope(homeDir, selection, ScopeGlobal)
}

// RunSyncWithSelectionScope is the scope-aware programmatic entry point for
// sync. ScopeGlobal preserves the historical behavior; ScopeWorkspace runs the
// issue-#1074 workspace-only refresh with zero global mutation.
func RunSyncWithSelectionScope(homeDir string, selection model.Selection, scope InstallScope) (SyncResult, error) {
	// The exported programmatic entry must reject unsupported scopes instead of
	// silently falling back to global semantics (issue #1074).
	if _, err := parseInstallScope(string(scope)); err != nil {
		return SyncResult{}, err
	}
	result, err := runSyncWithSelectionScopeAfterSkip(homeDir, selection, scope)
	return finishPartialSync(result, err, nil)
}

func runSyncWithSelectionScopeAfterSkip(homeDir string, selection model.Selection, scope InstallScope) (SyncResult, error) {
	persistedState, persistedStateErr := state.Read(homeDir)
	if persistedStateErr != nil && !os.IsNotExist(persistedStateErr) {
		return SyncResult{Agents: selection.Agents, Selection: selection}, fmt.Errorf("read persisted installation state: %w", persistedStateErr)
	}
	return runSyncWithSelectionScope(homeDir, selection, scope, persistedState, persistedStateErr)
}

var syncStagePlan = func(runtime *syncRuntime) pipeline.StagePlan { return runtime.stagePlan() }
var compareChangedSyncFiles = changedSyncFiles

func runSyncWithSelectionScope(homeDir string, selection model.Selection, scope InstallScope, persistedState state.InstallState, persistedStateErr error) (SyncResult, error) {
	agentIDs := selection.Agents
	// The caller's preflight snapshot and its read error drive this sync.
	// Managed-asset provenance re-reads under its own lock.
	if err := validatePersistedSyncState(persistedStateErr); err != nil {
		return SyncResult{Agents: agentIDs, Selection: selection}, err
	}
	restorePersistedCommunityTools(homeDir, &selection, persistedState)

	result := SyncResult{
		Agents:    agentIDs,
		Selection: selection,
	}

	result, noOp, err := zeroAgentSyncNoOp(homeDir, scope, selection, result)
	if err != nil || noOp {
		return result, err
	}

	rt, err := newSyncRuntimeWithScope(homeDir, selection, scope)
	if err != nil {
		return result, err
	}
	defer rt.state.cleanupCompatibilityTransaction()
	defer rt.state.cleanupRollbackSnapshot()

	stagePlan := syncStagePlan(rt)
	result.Plan = stagePlan
	before, err := snapshotSyncFiles(rt.managedPaths)
	if err != nil {
		return result, err
	}

	orchestrator := pipeline.NewOrchestrator(pipeline.DefaultRollbackPolicy())
	result.Execution = orchestrator.Execute(stagePlan)
	compatibilityChanged := rt.state.compatibilityChangedFiles()
	if result.Execution.Err != nil {
		return result, fmt.Errorf("execute sync pipeline: %w", result.Execution.Err)
	}
	result.ManualActions = append(result.ManualActions, rt.state.nativeReviewActions...)
	result.ManualActions = append(result.ManualActions, rt.skippedActions...)
	result.SkippedAgents = append(result.SkippedAgents, rt.skippedParts...)

	// Capture how many managed assets were actually changed.
	// Deduplicate paths — multiple components may touch the same file
	// (e.g. Engram and Context7 both merge into settings.json).
	result.ChangedFiles, err = compareChangedSyncFiles(rt.changedFiles, before)
	if err != nil {
		return result, rollbackPostApplyError(orchestrator, result.Execution, err)
	}
	result.ChangedFiles = dedupPaths(append(result.ChangedFiles, compatibilityChanged...))
	result.FilesChanged = len(result.ChangedFiles)

	// True no-op: agents were discovered but all managed assets were already
	// current — no file was written or updated. Per spec scenario:
	// "No managed assets to sync — system completes without modifying files
	// and reports that no managed sync actions were needed."
	if result.FilesChanged == 0 {
		result.NoOp = true
	}

	// Post-apply verification reuses the same component paths as install.
	result.Verify = runPostSyncVerificationScoped(homeDir, rt.workspaceDir, scope, selection)
	result.Verify = withFailedSyncVerificationNote(result.Verify)
	if !result.Verify.Ready {
		verificationErr := fmt.Errorf("post-sync verification failed:\n%s", verify.RenderReport(result.Verify))
		rollback := orchestrator.Rollback(result.Execution)
		if rollback.Err != nil {
			verificationErr = errors.Join(verificationErr, rollback.Err)
		}
		return result, verificationErr
	}
	writer, err := deriveManagedAssetWriter()
	if err != nil {
		return result, rollbackPostApplyError(orchestrator, result.Execution, fmt.Errorf("derive managed asset writer identity: %w", err))
	}
	// A workspace sync is a pure managed-asset refresh: it never stamps the
	// global state file with provenance or community tools (issue #1074).
	if scope == ScopeGlobal {
		err := persistSyncManagedAssetState(homeDir, selection, writer)
		if err == nil {
			err = recordCodexServiceTier(homeDir, rt.state.codexServiceTier)
		}
		if err != nil {
			persistErr := fmt.Errorf("persist sync managed asset state: %w", err)
			rollback := orchestrator.Rollback(result.Execution)
			if rollback.Err != nil {
				persistErr = errors.Join(persistErr, rollback.Err)
			}
			return result, persistErr
		}
	}

	return result, nil
}

func persistSyncManagedAssetState(homeDir string, selection model.Selection, writer string) error {
	return withInstallStateLock(homeDir, func() error {
		latest, err := state.Read(homeDir)
		if errors.Is(err, os.ErrNotExist) {
			latest = state.InstallState{}
		} else if err != nil {
			return fmt.Errorf(
				"read install state for managed asset provenance: %w; run `brunnr-code install` to rewrite %s",
				err, state.Path(homeDir))
		}

		shouldWrite := false
		// #2685: stamp the binary version that performed this sync, so doctor
		// can report managed assets older than the running binary instead of
		// the user discovering the skew mid-review at START preflight.
		if latest.InstalledBinaryVersion != AppVersion {
			latest.InstalledBinaryVersion = AppVersion
			shouldWrite = true
		}
		if latest.ManagedAssetDigest != writer {
			latest.ManagedAssetDigest = writer
			shouldWrite = true
		}
		if !latest.CommunityToolsConfigured && selection.CommunityTools != nil {
			latest.CommunityTools = communityToolIDsToStrings(selection.CommunityTools)
			latest.CommunityToolsConfigured = true
			shouldWrite = true
		}
		now := time.Now().UTC()
		latest.LastSyncedAt = &now
		shouldWrite = true
		if !shouldWrite {
			return nil
		}
		if err := state.WriteReconciled(homeDir, latest); err != nil {
			return fmt.Errorf("persist managed asset provenance: %w", err)
		}
		return nil
	})
}

// RunSync is the top-level sync entry point, parallel to RunInstall.
// It parses CLI flags, discovers agents, builds the selection, then delegates
// to RunSyncWithSelection for the actual sync execution.
func RunSync(args []string) (SyncResult, error) {
	flags, err := ParseSyncFlags(args)
	if err != nil {
		return SyncResult{}, err
	}

	// Resolve the sync scope with install semantics: explicit flag >
	// BRUNNR_CODE_INSTALL_SCOPE > global default (issue #1074).
	scope, err := ResolveInstallScope(flags.Scope)
	if err != nil {
		return SyncResult{}, err
	}

	homeDir, err := osUserHomeDir()
	if err != nil {
		return SyncResult{}, fmt.Errorf("resolve user home directory: %w", err)
	}

	// Resolve agents: explicit flag takes precedence over auto-discovery.
	var agentIDs []model.AgentID
	if len(flags.Agents) > 0 {
		parsed, err := asAgentIDs(flags.Agents)
		if err != nil {
			return SyncResult{}, err
		}
		agentIDs = parsed
	} else {
		agentIDs = DiscoverAgents(homeDir)
	}
	agentIDs = unique(agentIDs)

	selection := BuildSyncSelection(flags, agentIDs)

	// Read state once for both model-assignment restoration and persona resolution.
	// A missing state file is treated as a fresh home; other read/validation
	// errors stop sync before any persona mutation or asset write.
	persistedState, persistedStateErr := state.Read(homeDir)
	if err := validatePersistedSyncState(persistedStateErr); err != nil {
		return SyncResult{Agents: agentIDs, Selection: selection}, err
	}
	RestorePersistedSelection(&selection, persistedState, flags)
	restoreCodexServiceTier(&selection, persistedState)
	restorePersistedCommunityTools(homeDir, &selection, persistedState)

	// Load persisted model assignments from state when not provided via flags.
	// Without this, every CLI sync falls back to defaults and would silently
	// overwrite the user's model choices.
	if len(selection.ClaudePhaseAssignments) == 0 && len(persistedState.ClaudePhaseAssignments) > 0 {
		m := make(map[string]model.ClaudePhaseAssignment, len(persistedState.ClaudePhaseAssignments))
		for k, v := range persistedState.ClaudePhaseAssignments {
			if k == "orchestrator" {
				continue
			}
			a := model.ClaudePhaseAssignment{Model: model.ClaudeModelAlias(v.Model), Effort: model.ClaudeEffort(v.Effort)}
			if a.Valid() {
				m[k] = a
			}
		}
		selection.ClaudePhaseAssignments = m
	}
	if len(selection.ClaudeModelAssignments) == 0 && len(selection.ClaudePhaseAssignments) == 0 && len(persistedState.ClaudeModelAssignments) > 0 {
		m := make(map[string]model.ClaudeModelAlias, len(persistedState.ClaudeModelAssignments))
		for k, v := range persistedState.ClaudeModelAssignments {
			// Claude Code controls the main session/orchestrator model itself.
			// Keep persisted assignments scoped to Agent tool calls only.
			if k == "orchestrator" {
				continue
			}
			m[k] = model.ClaudeModelAlias(v)
		}
		selection.ClaudeModelAssignments = m
	}
	if selection.CodexOrchestratorAssignment == nil && persistedState.CodexOrchestratorAssignment != nil {
		selection.CodexOrchestratorAssignment = codexOrchestratorFromState(persistedState.CodexOrchestratorAssignment)
	}

	// Restore Codex effort and carril model assignments from state so that
	// `brunnr-code sync` preserves the user's per-phase effort and per-carril
	// model choices instead of falling back to canonical defaults every time.
	// This mirrors the TUI path (loadPersistedAssignments in app.go).
	if len(selection.CodexModelAssignments) == 0 && len(persistedState.CodexModelAssignments) > 0 {
		m := make(map[string]model.CodexEffort, len(persistedState.CodexModelAssignments))
		for k, v := range persistedState.CodexModelAssignments {
			m[k] = model.CodexEffort(v)
		}
		selection.CodexModelAssignments = m
	}
	if len(selection.CodexCarrilModelAssignments) == 0 && len(persistedState.CodexCarrilModelAssignments) > 0 {
		selection.CodexCarrilModelAssignments = model.MigrateLegacyCodexCarrilDefaults(persistedState.CodexCarrilModelAssignments)
	}
	if len(selection.CodexPhaseModelAssignments) == 0 && len(persistedState.CodexPhaseModelAssignments) > 0 {
		m := make(map[string]string, len(persistedState.CodexPhaseModelAssignments))
		for k, v := range persistedState.CodexPhaseModelAssignments {
			m[k] = v
		}
		selection.CodexPhaseModelAssignments = m
	}

	if flags.DryRun {
		// Build the plan for inspection, skip execution.
		result := SyncResult{
			Agents:    agentIDs,
			Selection: selection,
			DryRun:    true,
		}
		result, noOp, err := zeroAgentSyncNoOp(homeDir, scope, selection, result)
		if err != nil || noOp {
			return result, err
		}
		rt, err := newSyncRuntimeWithScope(homeDir, selection, scope)
		if err != nil {
			return result, err
		}
		defer rt.state.cleanupCompatibilityTransaction()
		defer rt.state.cleanupRollbackSnapshot()
		result.Plan = rt.stagePlan()
		for _, step := range result.Plan.Prepare {
			if prepare, ok := step.(prepareBackupStep); ok && prepare.targetErr != nil {
				return result, fmt.Errorf("resolve backup targets: %w", prepare.targetErr)
			}
		}
		return result, nil
	}

	result, err := runSyncWithSelectionScope(homeDir, selection, scope, persistedState, persistedStateErr)
	if err != nil {
		return finishPartialSync(result, err, nil)
	}
	result.DryRun = false
	return finishPartialSync(result, nil, nil)
}

// zeroAgentSyncNoOp reports whether a sync without agents has no compatible
// shared-skill work to perform. The shared compatibility-skills tree is
// global-only, so a workspace sync without agents is always a no-op.
func zeroAgentSyncNoOp(homeDir string, scope InstallScope, selection model.Selection, result SyncResult) (SyncResult, bool, error) {
	if len(result.Agents) != 0 {
		return result, false, nil
	}
	if scope == ScopeWorkspace {
		result.NoOp = true
		return result, true, nil
	}
	refreshable, err := compatibilitySkillsRefreshable(homeDir, selection)
	if err != nil {
		return result, false, err
	}
	if refreshable {
		return result, false, nil
	}
	result.NoOp = true
	return result, true, nil
}

func restorePersistedCommunityTools(homeDir string, selection *model.Selection, persisted state.InstallState) {
	if selection.CommunityTools != nil {
		return
	}
	if persisted.CommunityToolsConfigured {
		selection.CommunityTools = make([]model.CommunityToolID, 0, len(persisted.CommunityTools))
		for _, tool := range persisted.CommunityTools {
			switch model.CommunityToolID(tool) {
			case model.CommunityToolCodeGraph:
				selection.CommunityTools = append(selection.CommunityTools, model.CommunityToolCodeGraph)
			}
		}
		return
	}
	if communitytool.HasManagedCodeGraphGuidance(homeDir) {
		selection.CommunityTools = []model.CommunityToolID{model.CommunityToolCodeGraph}
	}
}

// RenderSyncReport renders a human-readable summary of a sync execution.
//
// Unlike verify.RenderReport (which shows verification check statuses), this
// function reports the managed sync actions that were executed — matching the
// spec requirement to surface "what was done" rather than "what was checked".
//
// No-op cases:
//   - No agents were discovered or specified (NoOp=true, Agents empty).
//   - All managed assets were already current (NoOp=true, FilesChanged=0).
func RenderSyncReport(result SyncResult) string {
	var b strings.Builder
	warningReport := func() {
		for _, check := range result.Verify.Checks {
			if check.Status == verify.CheckStatusWarning {
				fmt.Fprintf(&b, "WARNING: %s\n", check.Error)
			}
		}
	}

	if result.NoOp {
		fmt.Fprintln(&b, "brunnr-code sync — no managed sync actions needed")
		if len(result.Agents) == 0 {
			fmt.Fprintln(&b, "No agents were discovered or specified. Nothing to sync.")
		} else {
			fmt.Fprintf(&b, "Agents: %s\n", joinAgentIDs(result.Agents))
			if len(result.ManualActions) == 0 {
				fmt.Fprintln(&b, "All managed assets are already up to date. No files changed.")
			} else {
				fmt.Fprintln(&b, "No managed files changed; preserved native agents were not updated.")
			}
		}
		renderSyncSkippedAgents(&b, result.SkippedAgents)
		warningReport()
		renderSyncManualActions(&b, result.ManualActions)
		return strings.TrimRight(b.String(), "\n")
	}

	if result.DryRun {
		fmt.Fprintln(&b, "brunnr-code sync — dry-run")
		fmt.Fprintf(&b, "Agents: %s\n", joinAgentIDs(result.Agents))

		compParts := make([]string, 0, len(result.Selection.Components))
		for _, c := range result.Selection.Components {
			compParts = append(compParts, string(c))
		}
		if len(compParts) > 0 {
			fmt.Fprintf(&b, "Managed components: %s\n", strings.Join(compParts, ", "))
		}
		fmt.Fprintf(&b, "Prepare steps: %d\n", len(result.Plan.Prepare))
		fmt.Fprintf(&b, "Apply steps: %d\n", len(result.Plan.Apply))
		for _, stop := range result.DryRunStops {
			fmt.Fprintf(&b, "Would stop: %s\n", stop)
		}
		warningReport()
		return strings.TrimRight(b.String(), "\n")
	}

	fmt.Fprintln(&b, "brunnr-code sync — managed sync executed")
	fmt.Fprintf(&b, "Agents synced: %s\n", joinAgentIDs(result.Agents))
	renderSyncSkippedAgents(&b, result.SkippedAgents)

	compParts := make([]string, 0, len(result.Selection.Components))
	for _, c := range result.Selection.Components {
		compParts = append(compParts, string(c))
	}
	if len(compParts) > 0 {
		fmt.Fprintf(&b, "Managed components synced: %s\n", strings.Join(compParts, ", "))
	}

	// Report actual files changed — not the count of successful pipeline steps.
	// FilesChanged is 0 only when all assets were already current (no-op path
	// above handles that case). A non-zero value here reflects real writes.
	fmt.Fprintf(&b, "Sync actions executed: %d files changed\n", result.FilesChanged)

	if len(result.ChangedFiles) > 0 {
		for _, path := range result.ChangedFiles {
			fmt.Fprintf(&b, "  - %s\n", path)
		}
	}

	if !result.Verify.Ready {
		fmt.Fprintln(&b, "")
		fmt.Fprintln(&b, "Post-sync verification:")
		fmt.Fprint(&b, verify.RenderReport(result.Verify))
	}
	warningReport()
	renderSyncManualActions(&b, result.ManualActions)

	return strings.TrimRight(b.String(), "\n")
}

func renderSyncSkippedAgents(b *strings.Builder, skipped []SyncSkippedAgent) {
	if len(skipped) == 0 {
		return
	}
	names := make([]string, 0, len(skipped))
	for _, agent := range skipped {
		if agent.Part != "" {
			names = append(names, fmt.Sprintf("%s (%s)", agent.Agent, agent.Part))
			continue
		}
		names = append(names, string(agent.Agent))
	}
	fmt.Fprintf(b, "Agents skipped: %s\n", strings.Join(names, ", "))
	for _, agent := range skipped {
		fmt.Fprintf(b, "- %s\n", agent.Action())
	}
}

func renderSyncManualActions(b *strings.Builder, actions []string) {
	if len(actions) == 0 {
		return
	}
	fmt.Fprintln(b, "Manual actions required:")
	for _, action := range actions {
		fmt.Fprintf(b, "- %s\n", action)
	}
}

// withFailedSyncVerificationNote replaces the generic
// verify.VerificationIssuesMessage with one naming the concrete command that
// retries a failed sync: `brunnr-code sync`. Unlike the install path, sync has
// no per-agent retry command -- rerunning `brunnr-code sync` re-applies every
// discovered/persisted agent, so no agent list is needed.
//
// It is scoped to exactly the generic failure text so it never clobbers a
// FinalNote that was already customized, mirroring
// withFailedVerificationNote's install-path guard.
func withFailedSyncVerificationNote(report verify.Report) verify.Report {
	if report.Ready || report.FinalNote != verify.VerificationIssuesMessage {
		return report
	}
	report.FinalNote = verify.VerificationIssuesMessageForCommand("brunnr-code sync")
	return report
}

// runPostSyncVerification verifies that managed files exist after sync.
// runPostSyncVerificationScoped verifies that the files a scoped sync writes
// exist after the run. Under ScopeWorkspace, globally-managed components are
// skipped and any declared path that still resolves under the home root is
// dropped, because a workspace sync never writes there (issue #1074).
func runPostSyncVerificationScoped(homeDir, workspaceDir string, scope InstallScope, selection model.Selection) verify.Report {
	workspace := scope == ScopeWorkspace
	checks := make([]verify.Check, 0)
	adapters := resolveAdapters(selection.Agents)

	for _, component := range selection.Components {
		if workspace && workspaceGlobalOnlyComponent(component) {
			continue
		}
		componentAdapters := adapters
		if workspace {
			componentAdapters = workspaceScopedAdapters(component, adapters)
		}
		paths := syncComponentPathsWithWorkspaceScoped(homeDir, workspaceDir, scope, selection, componentAdapters, component)
		for _, path := range paths {
			if workspace && pathUnderHomeOnly(path, homeDir, workspaceDir) {
				continue
			}
			currentPath := path
			checks = append(checks, verify.Check{
				ID:          "verify:sync:file:" + currentPath,
				Description: "synced file exists",
				Run: func(context.Context) error {
					if _, err := os.Stat(currentPath); err != nil {
						return err
					}
					return nil
				},
			})
		}
	}
	return verify.BuildReport(verify.RunChecks(context.Background(), checks))
}
