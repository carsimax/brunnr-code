package cli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/agents/claude"
	codexagent "github.com/carsimax/brunnr-code/internal/agents/codex"
	"github.com/carsimax/brunnr-code/internal/assets"
	"github.com/carsimax/brunnr-code/internal/backup"
	"github.com/carsimax/brunnr-code/internal/components/agentguidance"
	"github.com/carsimax/brunnr-code/internal/components/agenthooks"
	"github.com/carsimax/brunnr-code/internal/components/communitytool"
	"github.com/carsimax/brunnr-code/internal/components/engram"
	"github.com/carsimax/brunnr-code/internal/components/filemerge"
	"github.com/carsimax/brunnr-code/internal/components/legacyassets"
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

type InstallResult struct {
	Selection     model.Selection
	Resolved      planner.ResolvedPlan
	Review        planner.ReviewPayload
	Plan          pipeline.StagePlan
	Execution     pipeline.ExecutionResult
	Verify        verify.Report
	Dependencies  system.DependencyReport
	ManualActions []string
	DryRun        bool
}

var (
	osUserHomeDir                = os.UserHomeDir
	osSetenv                     = os.Setenv
	osStat                       = os.Stat
	runCommand                   = executeCommand
	cmdLookPath                  = exec.LookPath
	streamCommandOutput          = true
	installCommunityTool         = communitytool.Install
	installCommunityToolWithHome = communitytool.InstallWithHome
	injectInstallPersona         = persona.Inject
	injectSyncPersona            = persona.InjectForSync
	pathEnvEntries               = func(profile system.PlatformProfile) []string {
		return splitPathForOS(os.Getenv("PATH"), profile.OS)
	}
	addUserPath         = system.AddToUserPath
	ensureUserPathFirst = system.PrioritizeUserPath
	userPathEntries     = system.UserPathEntries

	// engramDownloadFn is the function used to download the engram binary on non-brew platforms.
	// Package-level var for testability — tests can replace this to avoid real HTTP calls.
	engramDownloadFn = engram.DownloadLatestBinary

	// verifyEngramVersion resolves the installed engram binary version, threaded
	// into InjectOptions.Version (Decision 1 gate) and used to compute the
	// per-slug --protocol forwarding verdict. Package-level var for
	// testability — tests replace this to avoid depending on a real
	// installed engram binary. Overridden to a safe fake for the whole
	// package's test run (see TestMain in protocol_probe_test.go).
	verifyEngramVersion        = engram.VerifyVersion
	verifyEngramVersionCommand = engram.VerifyVersionCommand

	// probeEngramProtocolFlag detects whether the installed engram binary
	// supports the --protocol verbosity flag (design.md Decision 4).
	// Package-level var for testability — same rationale as verifyEngramVersion.
	probeEngramProtocolFlag        = engram.ProbeProtocolFlag
	probeEngramProtocolFlagCommand = engram.ProbeProtocolFlagCommand

	// AppVersion is the gentle-ai version that will be written into backup manifests.
	// It is set by app.go before any CLI operation so that every backup created during
	// an install or sync records which version of gentle-ai made it.
	// Default "dev" matches the ldflags default in app.Version.
	AppVersion = "dev"
)

// SetCommandOutputStreaming toggles whether command stdout/stderr is streamed
// directly to the terminal. It returns a restore function.
func SetCommandOutputStreaming(enabled bool) func() {
	previous := streamCommandOutput
	streamCommandOutput = enabled
	return func() {
		streamCommandOutput = previous
	}
}

// Test seams are restored with t.Cleanup; their tests are not parallel.
var deriveManagedAssetWriter = managedAssetDigest
var installStagePlan = func(runtime *installRuntime) pipeline.StagePlan { return runtime.stagePlan() }

// PreparedInstall is a validated install plan, ready for execution after app preflight.
// Its fields stay private so dispatch reuses the exact parsed and resolved input.
type PreparedInstall struct {
	flags    InstallFlags
	input    InstallInput
	resolved planner.ResolvedPlan
}

// PrepareInstall resolves and validates without reading or writing install state.
func PrepareInstall(args []string, detection system.DetectionResult) (PreparedInstall, error) {
	flags, err := ParseInstallFlags(args)
	if err != nil {
		return PreparedInstall{}, err
	}

	input, err := NormalizeInstallFlags(flags, detection)
	if err != nil {
		return PreparedInstall{}, err
	}

	resolved, err := planner.NewResolver(planner.MVPGraph()).Resolve(input.Selection)
	if err != nil {
		return PreparedInstall{}, err
	}
	if err := validateInstallModifierConsumers(flags, input.Selection, resolved.OrderedComponents); err != nil {
		return PreparedInstall{}, err
	}
	return PreparedInstall{flags: flags, input: input, resolved: resolved}, nil
}

// RunPreparedInstall executes a plan returned by PrepareInstall.
func RunPreparedInstall(prepared PreparedInstall, detection system.DetectionResult) (InstallResult, error) {
	flags, input, resolved := prepared.flags, prepared.input, prepared.resolved
	profile := ResolveInstallProfile(detection)
	resolved.PlatformDecision = planner.PlatformDecisionFromProfile(profile)
	homeDir, err := osUserHomeDir()
	if err != nil {
		return InstallResult{}, fmt.Errorf("resolve user home directory: %w", err)
	}
	persistedState, stateErr := state.Read(homeDir)
	if errors.Is(stateErr, os.ErrNotExist) {
		persistedState = state.InstallState{}
	} else if stateErr != nil {
		return InstallResult{}, fmt.Errorf("persist install state preflight: %w", stateErr)
	}
	restoreCodexServiceTier(&input.Selection, persistedState)
	review := planner.BuildReviewPayload(input.Selection, resolved)
	stagePlan := buildStagePlan(input.Selection, resolved)

	result := InstallResult{
		Selection:    input.Selection,
		Resolved:     resolved,
		Review:       review,
		Plan:         stagePlan,
		Dependencies: detection.Dependencies,
		DryRun:       input.DryRun,
	}

	if input.DryRun {
		return result, nil
	}
	if err := validateCallerEngramRuns(resolved.OrderedComponents); err != nil {
		return result, err
	}

	if input.Scope == ScopeGlobal {
		fmt.Fprintf(os.Stderr,
			"WARNING: installing with --scope=global (default). Agent config files (system prompts, skills/, agents/, etc.)\n"+
				"will be written to each selected agent's global config directory and will affect ALL workspaces for those agents on this machine.\n"+
				"To install only into the current workspace, rerun with --scope=workspace.\n\n")
	}
	runtime, err := newInstallRuntime(homeDir, input.Scope, input.Selection, resolved, profile)
	if err != nil {
		return result, err
	}
	defer runtime.state.cleanupCompatibilityTransaction()
	defer runtime.state.cleanupRollbackSnapshot()

	// Print dependency warnings before the pipeline starts (CLI only).
	// The TUI surfaces these on the complete screen instead.
	if !detection.Dependencies.AllPresent {
		fmt.Fprintf(os.Stderr, "WARNING: missing dependencies: %s\n\n%s\n",
			strings.Join(detection.Dependencies.MissingRequired, ", "),
			system.FormatMissingDepsMessage(detection.Dependencies))
	}
	runtime.claudeOrchestratorModules = input.ClaudeOrchestratorModules

	stagePlan = installStagePlan(runtime)
	result.Plan = stagePlan

	orchestrator := pipeline.NewOrchestrator(pipeline.DefaultRollbackPolicy())
	result.Execution = orchestrator.Execute(stagePlan)
	if result.Execution.Err != nil {
		return result, fmt.Errorf("execute install pipeline: %w", result.Execution.Err)
	}
	result.ManualActions = append(result.ManualActions, runtime.state.nativeReviewActions...)
	result.ManualActions = append(result.ManualActions, runtime.state.retiredSDDActions...)
	result.Verify = runPostApplyVerification(postApplyVerificationInput{
		HomeDir:      homeDir,
		WorkspaceDir: runtime.workspaceDir,
		Scope:        input.Scope,
		Selection:    input.Selection,
		Resolved:     resolved,
		State:        runtime.state,
	})
	result.Verify = withPostInstallNotes(result.Verify, resolved)
	if !result.Verify.Ready {
		verificationErr := fmt.Errorf("post-apply verification failed:\n%s", verify.RenderReport(result.Verify))
		rollback := orchestrator.Rollback(result.Execution)
		if rollback.Err != nil {
			verificationErr = errors.Join(verificationErr, rollback.Err)
		}
		return result, verificationErr
	}

	// Persist the user's agent selection and model assignments so that future
	// `sync` runs target only the installed agents and preserve model choices.
	agentIDs := make([]string, 0, len(input.Selection.Agents))
	for _, a := range input.Selection.Agents {
		agentIDs = append(agentIDs, string(a))
	}

	// When the user ran `gentle-ai install --agent X` (explicit agent flag),
	// merge into the existing state so that previously installed agents and
	// model assignments are preserved. A full install (no --agent flag) keeps
	// overwrite semantics so the TUI selection is the source of truth.
	claudePhaseState := claudePhaseAssignmentsToState(input.Selection.ClaudePhaseAssignments)
	newState := state.InstallState{
		InstalledAgents:             agentIDs,
		InstalledBinaryVersion:      AppVersion,
		CommunityTools:              communityToolIDsToStrings(input.Selection.CommunityTools),
		CommunityToolsConfigured:    true,
		ClaudeModelAssignments:      claudeLegacyAssignmentsForState(input.Selection.ClaudeModelAssignments, claudePhaseState),
		ClaudePhaseAssignments:      claudePhaseState,
		CodexModelAssignments:       codexEffortsToStrings(input.Selection.CodexModelAssignments),
		CodexOrchestratorAssignment: codexOrchestratorToState(input.Selection.CodexOrchestratorAssignment),
		CodexCarrilModelAssignments: input.Selection.CodexCarrilModelAssignments,
		CodexPhaseModelAssignments:  input.Selection.CodexPhaseModelAssignments,
	}
	newState.SetSelection(input.Selection)
	writer, err := deriveManagedAssetWriter()
	if err != nil {
		return result, rollbackPostApplyError(orchestrator, result.Execution, fmt.Errorf("derive managed asset writer identity: %w", err))
	}
	err = persistInstallState(homeDir, newState, agentIDs, flags, writer)
	if err == nil {
		err = recordCodexServiceTier(homeDir, runtime.state.codexServiceTier)
	}
	if err != nil {
		persistErr := fmt.Errorf("persist install state: %w", err)
		rollback := orchestrator.Rollback(result.Execution)
		if rollback.Err != nil {
			persistErr = errors.Join(persistErr, rollback.Err)
		}
		return result, persistErr
	}

	return result, nil
}

func rollbackPostApplyError(orchestrator *pipeline.Orchestrator, execution pipeline.ExecutionResult, cause error) error {
	rollback := orchestrator.Rollback(execution)
	return errors.Join(cause, rollback.Err)
}

func persistInstallState(homeDir string, newState state.InstallState, agentIDs []string, flags InstallFlags, writer string) error {
	return withInstallStateLock(homeDir, func() error {
		if len(flags.Agents) > 0 {
			merged, mergeErr := mergeExplicitAgentInstallState(homeDir, newState, agentIDs, flags)
			if mergeErr != nil {
				return fmt.Errorf("merge explicit agent install state: %w", mergeErr)
			}
			newState = merged
		} else {
			existing, err := state.Read(homeDir)
			if errors.Is(err, os.ErrNotExist) {
				existing = state.InstallState{}
			} else if err != nil {
				return err
			}
			newState = mergeFullInstallState(existing, newState)
		}
		newState.ManagedAssetDigest = writer
		return state.WriteReconciled(homeDir, newState)
	})
}

func mergeFullInstallState(existing, fresh state.InstallState) state.InstallState {
	merged := existing
	merged.InstalledAgents = fresh.InstalledAgents
	merged.SelectionConfigured, merged.Components, merged.Skills = fresh.SelectionConfigured, fresh.Components, fresh.Skills
	merged.Preset, merged.SDDMode, merged.StrictTDD = fresh.Preset, fresh.SDDMode, false
	merged.CommunityTools, merged.CommunityToolsConfigured = fresh.CommunityTools, fresh.CommunityToolsConfigured
	merged.ClaudeModelAssignments, merged.ClaudePhaseAssignments = fresh.ClaudeModelAssignments, fresh.ClaudePhaseAssignments
	merged.CodexModelAssignments = fresh.CodexModelAssignments
	merged.CodexOrchestratorAssignment = fresh.CodexOrchestratorAssignment
	merged.CodexCarrilModelAssignments, merged.CodexPhaseModelAssignments = fresh.CodexCarrilModelAssignments, fresh.CodexPhaseModelAssignments
	return merged
}

// mergeExplicitAgentInstallState merges a fresh single-agent install's state
// into the previously persisted ~/.gentle-ai/state.json (so `install --agent
// X` preserves other previously installed agents and model assignments).
//
// When the existing state file is simply absent (first install, or an agent
// installed before state persistence existed), there is nothing to merge
// from — newState is used as-is. Any OTHER read failure (corrupted JSON,
// unreadable file) returns an error instead of silently falling through: the
// caller must not report a successful install while never persisting state
// (install/sync surface audit finding 2). Blindly merging from unreadable
// data would itself be wrong, so this does not attempt best-effort recovery
// — it fails loudly instead, matching the original conservative intent of
// refusing to merge from data that cannot be trusted.
func mergeExplicitAgentInstallState(homeDir string, newState state.InstallState, agentIDs []string, flags InstallFlags) (state.InstallState, error) {
	existing, readErr := state.Read(homeDir)
	if readErr != nil {
		if errors.Is(readErr, os.ErrNotExist) {
			return newState, nil
		}
		return state.InstallState{}, fmt.Errorf("read existing install state to merge agents %v: %w", agentIDs, readErr)
	}

	existing.InstalledAgents = model.SupportedAgentIDs(existing.InstalledAgents)
	merged := state.MergeAgents(existing, agentIDs)
	if newState.ClaudeModelAssignments != nil {
		merged.ClaudeModelAssignments = newState.ClaudeModelAssignments
	}
	if newState.ClaudePhaseAssignments != nil {
		merged.ClaudePhaseAssignments = newState.ClaudePhaseAssignments
		merged.ClaudeModelAssignments = nil
	}
	if newState.CodexOrchestratorAssignment != nil {
		merged.CodexOrchestratorAssignment = newState.CodexOrchestratorAssignment
	}
	if newState.CodexModelAssignments != nil {
		merged.CodexModelAssignments = newState.CodexModelAssignments
	}
	if newState.CodexCarrilModelAssignments != nil {
		merged.CodexCarrilModelAssignments = newState.CodexCarrilModelAssignments
	}
	if newState.CodexPhaseModelAssignments != nil {
		merged.CodexPhaseModelAssignments = newState.CodexPhaseModelAssignments
	}
	if !merged.SelectionConfigured && newState.SelectionConfigured {
		// A metadata-only or legacy state has no selection to preserve.
		merged.SelectionConfigured = true
		merged.Components, merged.Skills = newState.Components, newState.Skills
		merged.Preset, merged.SDDMode = newState.Preset, newState.SDDMode
	} else if merged.SelectionConfigured {
		if len(flags.Components) > 0 {
			merged.Components = newState.Components
		}
		if len(flags.Skills) > 0 {
			merged.Skills = newState.Skills
		}
		if flags.Preset != "" {
			merged.Preset = newState.Preset
		}
		if flags.SDDMode != "" {
			merged.SDDMode = newState.SDDMode
		}
	}
	return merged, nil
}

func withPostInstallNotes(report verify.Report, resolved planner.ResolvedPlan) verify.Report {
	report = withReadyAgentRunNote(report, resolved)
	report = withFailedVerificationNote(report, resolved)
	report = withGoInstallPathNote(report, resolved)
	return report
}

// readyAgentRunCommands is the fixed, ordered set of installable agents that
// have a standalone runnable CLI command a user types to start building, and
// the exact command name checked by each adapter's own Detect (e.g.
// claude/adapter.go's lookPath("claude")). Kept intentionally narrow to the
// agent the completion line originally named (fisidj's finding, organic-dx
// Phase 3f task 3f.4); expanding to every agent with a CLI binary is a
// separate, unreviewed decision left for a future task.
var readyAgentRunCommands = []struct {
	ID      model.AgentID
	Command string
}{
	{model.AgentClaudeCode, "claude"},
}

// withReadyAgentRunNote replaces the generic verify.ReadyMessage with one
// naming only the runnable agent commands actually selected this run. It is
// scoped to exactly that generic text so it never clobbers a FinalNote that
// was already customized (by a test fixture or an earlier note-builder).
func withReadyAgentRunNote(report verify.Report, resolved planner.ResolvedPlan) verify.Report {
	if !report.Ready || report.FinalNote != verify.ReadyMessage {
		return report
	}
	report.FinalNote = verify.ReadyMessageForCommands(runnableAgentCommands(resolved.Agents))
	return report
}

// withFailedVerificationNote replaces the generic verify.VerificationIssuesMessage
// with one naming the concrete command that retries the install for the
// agents that were actually resolved this run: `gentle-ai install --agent
// <agent1>,<agent2>`. There is no `repair` case in the CLI dispatcher
// (internal/app/app.go), so the old generic text named a command that could
// never succeed -- a false continuation worse than no note at all.
//
// It is scoped to exactly the generic failure text so it never clobbers a
// FinalNote that was already customized (by a test fixture or an earlier
// note-builder), mirroring withReadyAgentRunNote's ready-path guard.
func withFailedVerificationNote(report verify.Report, resolved planner.ResolvedPlan) verify.Report {
	if report.Ready || report.FinalNote != verify.VerificationIssuesMessage {
		return report
	}
	if len(resolved.Agents) == 0 {
		return report
	}
	names := make([]string, len(resolved.Agents))
	for i, agent := range resolved.Agents {
		names[i] = string(agent)
	}
	report.FinalNote = verify.VerificationIssuesMessageForCommand("gentle-ai install --agent " + strings.Join(names, ","))
	return report
}

func runnableAgentCommands(agentIDs []model.AgentID) []string {
	selected := make(map[model.AgentID]bool, len(agentIDs))
	for _, id := range agentIDs {
		selected[id] = true
	}
	commands := make([]string, 0, len(readyAgentRunCommands))
	for _, entry := range readyAgentRunCommands {
		if selected[entry.ID] {
			commands = append(commands, entry.Command)
		}
	}
	return commands
}

// withGoInstallPathNote appends a PATH guidance note when engram was installed
// on a non-brew platform (Linux/Windows). Since engram is now installed via
// direct binary download to /usr/local/bin or ~/.local/bin, this note helps
// users who may need to add the install directory to their PATH.
func withGoInstallPathNote(report verify.Report, resolved planner.ResolvedPlan) verify.Report {
	if !hasComponent(resolved.OrderedComponents, model.ComponentEngram) {
		return report
	}
	if resolved.PlatformDecision.PackageManager == "brew" {
		return report
	}
	binDir := goInstallBinDir()
	if isInPATH(binDir) {
		return report
	}
	report.FinalNote = report.FinalNote + fmt.Sprintf(
		"\n\nThe engram binary was installed to %s via `go install`.\nAdd it to your PATH: %s",
		binDir,
		engramPathGuidance(os.Getenv("SHELL")),
	)
	return report
}

// goInstallBinDir returns the directory where `go install` places binaries.
// Resolution order: $GOBIN > $GOPATH/bin > $HOME/go/bin.
func goInstallBinDir() string {
	if gobin := os.Getenv("GOBIN"); gobin != "" {
		return gobin
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		return filepath.Join(gopath, "bin")
	}
	if home, err := osUserHomeDir(); err == nil {
		return filepath.Join(home, "go", "bin")
	}
	return filepath.Join("~", "go", "bin")
}

// validateCallerEngramRuns proves a caller-supplied Engram binary runs before
// anything is installed, when the plan applies Engram.
func validateCallerEngramRuns(components []model.ComponentID) error {
	bin, ok := engram.CallerBinary()
	if !ok || !slices.Contains(components, model.ComponentEngram) {
		return nil
	}
	if err := engram.ValidateCallerBinary(); err != nil {
		return err
	}
	if _, err := verifyEngramVersionCommand(bin); err != nil {
		return fmt.Errorf("%s=%s does not run: %w", system.EngramBinEnv, bin, err)
	}
	return nil
}

// isInPATH reports whether dir is present in the current PATH.
func isInPATH(dir string) bool {
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if entry == dir {
			return true
		}
	}
	return false
}

func buildStagePlan(selection model.Selection, resolved planner.ResolvedPlan) pipeline.StagePlan {
	prepare := []pipeline.Step{
		noopStep{id: "prepare:system-check"},
		noopStep{id: "prepare:check-dependencies"},
	}
	apply := make([]pipeline.Step, 0, len(resolved.Agents)+len(resolved.OrderedComponents))

	for _, agent := range resolved.Agents {
		apply = append(apply, noopStep{id: "agent:" + string(agent)})
	}

	for _, component := range resolved.OrderedComponents {
		apply = append(apply, noopStep{id: "component:" + string(component)})
	}
	if len(selection.Agents) == 0 && len(resolved.OrderedComponents) == 0 {
		prepare = nil
	}

	return pipeline.StagePlan{Prepare: prepare, Apply: apply}
}

type installRuntime struct {
	homeDir      string
	workspaceDir string
	scope        InstallScope
	selection    model.Selection
	resolved     planner.ResolvedPlan
	profile      system.PlatformProfile
	backupRoot   string
	state        *runtimeState

	progress pipeline.ProgressFunc

	// claudeOrchestratorModules is the explicit install opt-in for the
	// user-global Claude module pilot. stagePlan hands it to both the backup
	// planner and the routing step, so it must be set before the plan is built.
	claudeOrchestratorModules bool
}

type runtimeState struct {
	manifest                 backup.Manifest
	rollbackSnapshotDir      string
	nativeReviewActions      []string
	retiredSDDActions        []string
	compatibilityTransaction compatibilityRefreshTransaction

	// engramVersionResolved, engramVersion, and engramVersionErr cache the
	// single `engram version` invocation performed by componentApplyStep.Run
	// for ComponentEngram (Decision 1 gate), so the post-apply health check
	// (engramHealthChecks) can reuse the result instead of shelling out to
	// `engram version` a second time (JD-016).
	engramVersionResolved bool
	engramVersion         string
	engramVersionErr      error

	// codexServiceTier is the tier the global engram injection left in Codex's
	// config.toml (nil = not written); state records it as the managed tier.
	codexServiceTier *string
}

func (s *runtimeState) cleanupRollbackSnapshot() {
	if s == nil || s.rollbackSnapshotDir == "" {
		return
	}
	if err := os.RemoveAll(s.rollbackSnapshotDir); err != nil {
		log.Printf("backup: remove transaction snapshot: %v", err)
		return
	}
	s.rollbackSnapshotDir = ""
}

func (s *runtimeState) cleanupCompatibilityTransaction() {
	if s == nil || s.compatibilityTransaction == nil {
		return
	}
	transaction := s.compatibilityTransaction
	s.compatibilityTransaction = nil
	if err := transaction.Close(); err != nil {
		log.Printf("compatibility: close transaction: %v", err)
	}
}

func (s *runtimeState) compatibilityChangedFiles() []string {
	if s == nil || s.compatibilityTransaction == nil {
		return nil
	}
	return s.compatibilityTransaction.ChangedFiles()
}

func newInstallRuntime(homeDir string, scope InstallScope, selection model.Selection, resolved planner.ResolvedPlan, profile system.PlatformProfile) (*installRuntime, error) {
	backupRoot := state.BackupsDir(homeDir)
	compatibilityTransaction, err := newCompatibilityRefreshTransaction(homeDir, resolved.OrderedComponents, selection)
	if err != nil {
		return nil, err
	}
	state := &runtimeState{compatibilityTransaction: compatibilityTransaction}
	if err := os.MkdirAll(backupRoot, 0o755); err != nil {
		state.cleanupCompatibilityTransaction()
		return nil, fmt.Errorf("create backup root directory %q: %w", backupRoot, err)
	}

	workspaceDir, _ := os.Getwd()

	return &installRuntime{
		homeDir:      homeDir,
		workspaceDir: workspaceDir,
		scope:        scope,
		selection:    selection,
		resolved:     resolved,
		profile:      profile,
		backupRoot:   backupRoot,
		state:        state,
	}, nil
}

func (r *installRuntime) stagePlan() pipeline.StagePlan {
	targets, targetErr := installBackupTargets(r.homeDir, r.workspaceDir, r.scope, r.selection, r.resolved, r.claudeOrchestratorModules)
	prepare := []pipeline.Step{
		prepareBackupStep{
			id:          "prepare:backup-snapshot",
			snapshotter: backup.NewSnapshotter(),
			snapshotDir: filepath.Join(r.backupRoot, time.Now().UTC().Format("20060102150405.000000000")),
			targets:     targets,
			targetErr:   targetErr,
			state:       r.state,
			backupRoot:  r.backupRoot,
			source:      backup.BackupSourceInstall,
			description: "pre-install snapshot",
			appVersion:  AppVersion,
		},
	}

	apply := make([]pipeline.Step, 0, len(r.resolved.Agents)+len(r.selection.CommunityTools)+len(r.resolved.OrderedComponents)+1)
	// Check prerequisites before anything else in the prepare stage.
	prepare = append([]pipeline.Step{checkDependenciesStep{id: "prepare:check-dependencies", profile: r.profile, homeDir: r.homeDir, selection: r.selection, resolved: r.resolved}}, prepare...)
	apply = append(apply, rollbackRestoreStep{id: "apply:rollback-restore", state: r.state, homeDir: r.homeDir, workspaceDir: r.workspaceDir})

	for _, agent := range r.resolved.Agents {
		apply = append(apply, agentInstallStep{
			id:       "agent:" + string(agent),
			agent:    agent,
			homeDir:  r.homeDir,
			profile:  r.profile,
			progress: r.progress,
		})
	}

	for _, component := range r.resolved.OrderedComponents {
		if component == model.ComponentSDD {
			continue
		}
		step := componentApplyStep{
			id:           "component:" + string(component),
			component:    component,
			homeDir:      r.homeDir,
			workspaceDir: r.workspaceDir,
			scope:        r.scope,
			agents:       r.resolved.Agents,
			selection:    r.selection,
			profile:      r.profile,
			state:        r.state,
		}
		apply = append(apply, step)
	}
	for _, agent := range r.resolved.Agents {
		if nativeReviewAgentSupported(agent) {
			apply = append(apply, nativeReviewAgentStep{id: "agent:native-review:" + string(agent), agent: agent, homeDir: r.homeDir, workspaceDir: r.workspaceDir, scope: r.scope, selection: r.selection, state: r.state})
		}
	}
	apply = append(apply, retiredSDDAssetStepList(retiredSDDAssetSteps("agent:retire-sdd-assets:", r.homeDir, r.workspaceDir, r.scope, r.resolved.Agents, nil, r.state))...)
	// After the native installer, so retired SDD subagents go last.
	apply = append(apply, retiredSDDAgentSteps("agent:retire-sdd:", r.homeDir, r.scope, r.resolved.Agents, nil, r.state)...)

	// Routing guidance is scheduled per agent and outside the component loop:
	// an agent that cannot choose between direct and delegated work is unusable,
	// so guidance must not depend on a component selection. It runs after the
	// components so their managed assets are in place before guidance is merged.
	for _, agent := range r.resolved.Agents {
		apply = append(apply, agentRoutingGuidanceStep{
			codexPhaseModels: r.selection.CodexPhaseModelAssignments,
			codexEfforts:     r.selection.CodexModelAssignments,
			codexCarrils:     r.selection.CodexCarrilModelAssignments,
			legacySDD:        false,
			id:               "agent-guidance:" + string(agent),
			agent:            agent,
			homeDir:          r.homeDir,
			workspaceDir:     r.workspaceDir,
			scope:            r.scope,
			claudeModules:    r.claudeOrchestratorModules,
		})
	}

	// Community tools run after persona and routing guidance, matching sync:
	// persona replaces whole prompt files for some agents, so CodeGraph guidance
	// injected before it would be dropped and re-added by the first sync.
	for _, tool := range r.selection.CommunityTools {
		apply = append(apply, communityToolInstallStep{id: "community-tool:" + string(tool), tool: tool, workspaceDir: r.workspaceDir, homeDir: r.homeDir, agents: r.resolved.Agents, state: r.state})
	}

	if needsCompatibilitySkillsRefresh(r.resolved.OrderedComponents) {
		apply = append(apply, compatibilitySkillsRefreshStep{
			id:          "component:compatibility-skills-refresh",
			homeDir:     r.homeDir,
			components:  r.resolved.OrderedComponents,
			selection:   r.selection,
			transaction: r.state.compatibilityTransaction,
			anchored:    usesAnchoredCompatibilityTransaction(),
		})
	}

	return pipeline.StagePlan{Prepare: prepare, Apply: apply}
}

type nativeReviewAgentStep struct {
	id, homeDir, workspaceDir string
	agent                     model.AgentID
	scope                     InstallScope
	selection                 model.Selection
	changedFiles              *[]string
	state                     *runtimeState
}

func (s nativeReviewAgentStep) ID() string { return s.id }

func nativeReviewAgentSupported(agent model.AgentID) bool {
	return reviewassets.NativeAgentsSupported(agent)
}

func (s nativeReviewAgentStep) Run() error {
	adapter := resolveAdapters([]model.AgentID{s.agent})[0]
	target := componentInjectionDirScoped(s.homeDir, s.workspaceDir, s.scope, adapter)
	res, err := reviewassets.InstallNativeAgents(target, adapter, reviewassets.InstallOptions{
		ClaudeModelAssignments:    s.selection.ClaudeModelAssignments,
		ClaudePhaseAssignments:    s.selection.ClaudePhaseAssignments,
		CodeGraphGuidanceMarkdown: nativeReviewCodeGraphGuidanceMarkdown(s.homeDir, s.selection.CommunityTools),
	})
	if err != nil {
		return fmt.Errorf("install native review agents for %q: %w", s.agent, err)
	}
	if s.changedFiles != nil {
		*s.changedFiles = append(*s.changedFiles, res.Files...)
	}
	if s.state != nil {
		for _, path := range res.Skipped {
			s.state.nativeReviewActions = append(s.state.nativeReviewActions, nativeReviewPreservedAction(path))
		}
	}
	return nil
}

// retiredSDDAssetsStep removes the SDD skills, slash commands, workflow,
// Codex profiles, and Claude Code preflight hook earlier
// releases installed for one runtime, and the SDD text they left in prompt
// files routing guidance does not migrate. It reports what it cannot prove it
// wrote (#5157). The pipeline snapshot captures the same inventory first
// (retiredSDDAssetBackupPaths).
type retiredSDDAssetsStep struct {
	id           string
	inventory    legacyassets.SDDInventory
	changedFiles *[]string
	state        *runtimeState
}

func (s retiredSDDAssetsStep) ID() string { return s.id }

func (s retiredSDDAssetsStep) Run() error {
	res, err := s.inventory.Retire()
	if s.changedFiles != nil {
		*s.changedFiles = append(*s.changedFiles, res.Removed...)
		*s.changedFiles = append(*s.changedFiles, res.Rewritten...)
	}
	if err != nil {
		return err
	}
	if s.state != nil {
		s.state.retiredSDDActions = append(s.state.retiredSDDActions, res.ManualActions...)
	}
	return nil
}

// retiredSDDAssetSteps resolves where releases installed retired SDD files
// for the selected runtimes in this scope: the legacyassets inventory under
// the scoped config root, and the shared ~/.agents/skills root only in a
// global run on platforms where the generic snapshot owns it.
func retiredSDDAssetSteps(prefix, homeDir, workspaceDir string, scope InstallScope, agentIDs []model.AgentID, changedFiles *[]string, state *runtimeState) []retiredSDDAssetsStep {
	var steps []retiredSDDAssetsStep
	for _, adapter := range resolveAdapters(agentIDs) {
		// A workspace-scoped install wrote these under the workspace too.
		root := componentInjectionDirScoped(homeDir, workspaceDir, scope, adapter)
		inventory := legacyassets.RetiredSDDInventory(adapter, root)
		// A workspace sync delivers no routing guidance to these runtimes, so
		// nothing migrates the v3.7.0 SDD block of their project prompt.
		if scope == ScopeWorkspace && workspaceRoutingGuidanceGlobalOnly(adapter.Agent()) && adapter.SupportsSystemPrompt() {
			prompt := adapter.SystemPromptFile(root)
			inventory.Blocks = append(inventory.Blocks, legacyassets.SDDPromptFile{Root: filepath.Dir(prompt), Path: prompt})
		}
		steps = append(steps, retiredSDDAssetsStep{id: prefix + string(adapter.Agent()), inventory: inventory, changedFiles: changedFiles, state: state})
	}
	if scope != ScopeWorkspace && !usesAnchoredCompatibilityTransaction() {
		if dir, ok, err := compatibilitySkillsDir(homeDir); err == nil && ok {
			steps = append(steps, retiredSDDAssetsStep{id: prefix + "compatibility-skills", inventory: legacyassets.SDDInventory{Dirs: legacyassets.SDDAssetDirs{Skills: dir}}, changedFiles: changedFiles, state: state})
		}
	}
	return steps
}

func retiredSDDAssetStepList(steps []retiredSDDAssetsStep) []pipeline.Step {
	list := make([]pipeline.Step, 0, len(steps))
	for _, step := range steps {
		list = append(list, step)
	}
	return list
}

// retiredSDDAssetBackupPaths is the snapshot half of retiredSDDAssetSteps.
// Retirement only removes files that exist, so only present inventory files
// enter the snapshot; retired SDD paths never become install targets.
func retiredSDDAssetBackupPaths(homeDir, workspaceDir string, scope InstallScope, agentIDs []model.AgentID) []string {
	var paths []string
	for _, step := range retiredSDDAssetSteps("", homeDir, workspaceDir, scope, agentIDs, nil, nil) {
		paths = append(paths, step.inventory.BackupPaths()...)
	}
	return paths
}

// retiredSDDAgentsStep removes the native SDD sub-agents earlier releases
// rendered and reports the ones whose ownership cannot be proven (#5157,
// #5253). The pipeline snapshot captures every inventory path first.
type retiredSDDAgentsStep struct {
	id, homeDir  string
	agent        model.AgentID
	changedFiles *[]string
	state        *runtimeState
}

func (s retiredSDDAgentsStep) ID() string { return s.id }

func (s retiredSDDAgentsStep) Run() error {
	adapter := resolveAdapters([]model.AgentID{s.agent})[0]
	res, err := legacyassets.RetireSDDAgents(s.agent, adapter.SubAgentsDir(s.homeDir))
	if s.changedFiles != nil {
		*s.changedFiles = append(*s.changedFiles, res.Removed...)
	}
	if err != nil {
		return fmt.Errorf("retire SDD agents for %q: %w", s.agent, err)
	}
	if s.state != nil {
		s.state.retiredSDDActions = append(s.state.retiredSDDActions, res.ManualActions()...)
	}
	return nil
}

// retiredSDDAgentSteps schedules retirement for every selected runtime that
// received native SDD agents. Releases installed them in the home directory,
// so a workspace-scoped run never touches them (#1074).
func retiredSDDAgentSteps(prefix, homeDir string, scope InstallScope, agentIDs []model.AgentID, changedFiles *[]string, state *runtimeState) []pipeline.Step {
	if scope == ScopeWorkspace {
		return nil
	}
	var steps []pipeline.Step
	for _, agent := range agentIDs {
		if len(legacyassets.RetiredSDDAgentFiles(agent)) > 0 {
			steps = append(steps, retiredSDDAgentsStep{id: prefix + string(agent), homeDir: homeDir, agent: agent, changedFiles: changedFiles, state: state})
		}
	}
	return steps
}

// retiredSDDAgentBackupPaths is the snapshot half of retiredSDDAgentSteps.
func retiredSDDAgentBackupPaths(homeDir string, scope InstallScope, adapters []agents.Adapter) []string {
	if scope == ScopeWorkspace {
		return nil
	}
	var paths []string
	for _, adapter := range adapters {
		paths = append(paths, legacyassets.RetiredSDDAgentPaths(adapter.Agent(), adapter.SubAgentsDir(homeDir))...)
	}
	return paths
}

func nativeReviewPreservedAction(path string) string {
	return fmt.Sprintf("Native review agent %s was preserved, not updated: ownership cannot be verified (missing ledger entry or differing recorded hash). This does not mean you customized the file. Keeping it unchanged is valid. To opt into management, back up this file and verify the backup, remove only this warned file, then rerun your existing gentle-ai install or gentle-ai sync command with the same runtime, scope, and model choices. Gentle AI will not adopt or delete it automatically.", path)
}

// legacyTriggerRulesSection is the retired managed section that used to carry
// prompt-owned WorkRun ceremony. Nothing authors it anymore, so any copy still
// on disk is a stale instruction to invoke authority that no longer exists —
// it has to be removed, not refreshed.
const legacyTriggerRulesSection = "trigger-rules"

// routingReviewContract renders the native review execution contract the
// orchestrator guidance embeds. reviewassets already depends on agentguidance,
// so the installer passes it through RoutingOptions.ReviewContract instead of
// agentguidance importing it. It is a variable only so tests can prove the
// routing step fails closed without it.
var routingReviewContract agentguidance.ReviewContractSource = reviewassets.ReviewExecutionContractFor

// agentRoutingGuidanceStep delivers the organic routing guidance for one agent.
//
// It is deliberately not a component step. Routing guidance is what lets an
// agent choose between direct, delegated, and proposed work at all, so every
// configured agent receives it whether or not the optional SDD component was
// selected (issue #1794).
type agentRoutingGuidanceStep struct {
	codexPhaseModels map[string]string
	codexEfforts     map[string]model.CodexEffort
	codexCarrils     map[string]string
	legacySDD        bool
	id               string
	agent            model.AgentID
	homeDir          string
	workspaceDir     string
	scope            InstallScope

	// claudeModules is install's explicit opt-in for the user-global Claude
	// module pilot. Sync leaves it false and only continues an installed pilot.
	claudeModules bool

	// changedFiles is the shared sync accumulator. Install leaves it nil and
	// reports progress through the pipeline instead.
	changedFiles *[]string
}

func (s agentRoutingGuidanceStep) ID() string { return s.id }

func (s agentRoutingGuidanceStep) Run() error {
	adapter, err := agents.NewAdapter(s.agent)
	if err != nil {
		return fmt.Errorf("create adapter for %q: %w", s.agent, err)
	}
	// Resolve delivery before any write, so a Claude module pilot that cannot
	// be inspected fails this step with nothing changed.
	options, err := scopedRoutingGuidanceOptions(s.homeDir, s.workspaceDir, s.scope, adapter, s.claudeModules)
	if err != nil {
		return err
	}

	// Claude loads both global and project settings. Keep its hooks within
	// the selected scope without changing Codex's home-level hook delivery.
	hookRoot := s.homeDir
	if adapter.Agent() == model.AgentClaudeCode {
		hookRoot = ResolveAgentConfigDir(s.scope, s.homeDir, s.workspaceDir)
	}
	retainedHooks, err := agenthooks.InstallRetainedClaudeHooks(hookRoot, adapter)
	if err != nil {
		return fmt.Errorf("install retained Claude hooks for %q: %w", s.agent, err)
	}
	if s.changedFiles != nil && retainedHooks.Changed {
		*s.changedFiles = append(*s.changedFiles, retainedHooks.Files...)
	}
	hooks, err := agenthooks.InstallSkillRegistry(hookRoot, adapter)
	if err != nil {
		return fmt.Errorf("install skill-registry hook for %q: %w", s.agent, err)
	}
	if s.changedFiles != nil && hooks.Changed {
		*s.changedFiles = append(*s.changedFiles, hooks.Files...)
	}

	if !adapter.SupportsSystemPrompt() {
		return nil
	}

	targetDir := routingGuidanceDir(s.homeDir, s.workspaceDir, s.scope, adapter)

	// Strip first: an installation upgraded from an older release still carries
	// the retired block, and leaving it beside fresh guidance would hand the
	// agent two conflicting sets of instructions.
	stripped, err := stripLegacyTriggerRules(targetDir, adapter)
	if err != nil {
		return err
	}

	if s.agent == model.AgentCodex {
		options.CodexPhaseModelAssignments = s.codexPhaseModels
		options.CodexModelAssignments = s.codexEfforts
		options.CodexCarrilModelAssignments = s.codexCarrils
	}
	injected, err := agentguidance.InjectRoutingWithOptions(targetDir, s.agent, options)
	if err != nil {
		return fmt.Errorf("inject routing guidance for %q: %w", s.agent, err)
	}

	s.recordChanged(stripped)
	s.recordChanged(injected)
	strict, err := agentguidance.InjectStrictTDDWithOptions(targetDir, s.agent, false, options)
	if err != nil {
		return fmt.Errorf("retire legacy strict TDD guidance for %q: %w", s.agent, err)
	}
	s.recordChanged(strict)
	return nil
}

func (s agentRoutingGuidanceStep) recordChanged(result agentguidance.Result) {
	if s.changedFiles == nil || !result.Changed {
		return
	}
	*s.changedFiles = append(*s.changedFiles, result.Files...)
}

// stripLegacyTriggerRules removes the retired section from the scope the agent
// actually loads, mirroring the delivery strategies routing guidance uses.
//
// Removal reuses filemerge.InjectMarkdownSection with empty content, which is
// already the defined "delete this section" operation, so no second merge
// implementation exists that could drift from the injector.
func stripLegacyTriggerRules(targetDir string, adapter agents.Adapter) (agentguidance.Result, error) {
	return stripLegacyTriggerRulesFromPrompt(adapter.SystemPromptFile(targetDir))
}

func stripLegacyTriggerRulesFromPrompt(promptPath string) (agentguidance.Result, error) {
	if strings.TrimSpace(promptPath) == "" {
		return agentguidance.Result{}, nil
	}

	existing, err := os.ReadFile(promptPath)
	if err != nil {
		if os.IsNotExist(err) {
			return agentguidance.Result{}, nil
		}
		return agentguidance.Result{}, fmt.Errorf("read system prompt %q: %w", promptPath, err)
	}

	updated := filemerge.InjectMarkdownSection(string(existing), legacyTriggerRulesSection, "")
	if updated == string(existing) {
		return agentguidance.Result{}, nil
	}

	writeResult, err := filemerge.WriteFileAtomic(promptPath, []byte(updated), 0o644)
	if err != nil {
		return agentguidance.Result{}, err
	}
	return agentguidance.Result{Changed: writeResult.Changed, Files: []string{promptPath}}, nil
}

type prepareBackupStep struct {
	id          string
	snapshotter backup.Snapshotter
	snapshotDir string
	targets     []string
	targetErr   error
	state       *runtimeState

	// backupRoot is the parent directory of all backup snapshots.
	// When set, deduplication (DuplicateManifest) and retention pruning (Prune) are
	// enabled. When empty, both are skipped (backward-compatible default).
	backupRoot string

	// source and description are optional metadata written into the manifest.
	// When set, they help users identify what created the backup.
	source      backup.BackupSource
	description string

	// appVersion is the gentle-ai version that created this backup.
	// When set, it is written into the manifest as CreatedByVersion.
	appVersion string
}

func (s prepareBackupStep) ID() string {
	return s.id
}

func manifestTargetsMatch(manifest backup.Manifest, targets []string) bool {
	current := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		current[filepath.Clean(target)] = struct{}{}
	}
	historical := make(map[string]struct{}, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		historical[entry.OriginalPath] = struct{}{}
	}
	if len(current) != len(historical) {
		return false
	}
	for target := range current {
		if _, ok := historical[target]; !ok {
			return false
		}
	}
	return true
}

func (s prepareBackupStep) Run() error {
	if s.targetErr != nil {
		return fmt.Errorf("resolve backup targets: %w", s.targetErr)
	}
	// Deduplication: skip snapshot creation when content is identical to the
	// most recent backup. Only active when backupRoot is set.
	if s.backupRoot != "" {
		checksum, err := backup.ComputeChecksum(s.targets)
		if err == nil && checksum != "" {
			if manifest, duplicate, dupErr := backup.DuplicateManifest(s.backupRoot, checksum); dupErr != nil {
				log.Printf("backup: check duplicate: %v", dupErr)
			} else if duplicate && manifestTargetsMatch(manifest, s.targets) {
				rollbackDir, err := os.MkdirTemp("", "gentle-ai-rollback-*")
				if err != nil {
					return fmt.Errorf("create transaction snapshot directory: %w", err)
				}
				manifest, err := s.snapshotter.Create(rollbackDir, s.targets)
				if err != nil {
					_ = os.RemoveAll(rollbackDir)
					return fmt.Errorf("create transaction snapshot: %w", err)
				}
				s.state.manifest = manifest
				s.state.rollbackSnapshotDir = rollbackDir
				return nil
			}
		}
	}

	manifest, err := s.snapshotter.Create(s.snapshotDir, s.targets)
	if err != nil {
		return fmt.Errorf("create backup snapshot: %w", err)
	}

	// Annotate with source metadata and version when provided, then re-write.
	// FileCount is already populated by Snapshotter.Create.
	if s.source != "" || s.appVersion != "" {
		manifest.Source = s.source
		manifest.Description = s.description
		manifest.CreatedByVersion = s.appVersion
		manifestPath := filepath.Join(s.snapshotDir, backup.ManifestFilename)
		if err := backup.WriteManifest(manifestPath, manifest); err != nil {
			// Non-fatal: metadata annotation failed but the snapshot is intact.
			// The backup is still usable — restore will work. We just lose the label.
			log.Printf("backup: annotate manifest: %v", err)
		}
	}

	s.state.manifest = manifest

	// Retention pruning: remove oldest unpinned backups beyond the limit.
	// Non-fatal: a prune failure must not prevent the install/sync from succeeding.
	if s.backupRoot != "" {
		if _, pruneErr := backup.Prune(s.backupRoot, backup.DefaultRetentionCount); pruneErr != nil {
			log.Printf("backup: prune: %v", pruneErr)
		}
	}

	return nil
}

type rollbackRestoreStep struct {
	id           string
	state        *runtimeState
	homeDir      string
	workspaceDir string
}

func (s rollbackRestoreStep) ID() string {
	return s.id
}

func (s rollbackRestoreStep) Run() error {
	return nil
}

func (s rollbackRestoreStep) Rollback() error {
	defer s.state.cleanupRollbackSnapshot()
	manifest := s.state.manifest
	if len(manifest.Entries) == 0 {
		return nil
	}
	return (backup.RestoreService{Roots: rollbackRoots(s.homeDir, s.workspaceDir)}).Restore(manifest)
}

// rollbackRoots returns the directories this install/sync run could
// legitimately have written under, for validating rollback's manifest
// entries against a caller-known root instead of anything the manifest
// itself declares.
//
// homeDir is always included. workspaceDir is included too when set and
// distinct from homeDir: componentInjectionDirScoped resolves most
// component targets there under ScopeWorkspace, while project tools retain
// their cwd independently of agent artifact scope.
// Both values are exactly what backupTargets/syncBackupTargets used to
// compute what this run actually snapshotted, so allowing rollback to
// write within them is not wider than what this run could already do.
func rollbackRoots(homeDir, workspaceDir string) []string {
	roots := []string{homeDir}
	if workspaceDir != "" && workspaceDir != homeDir {
		roots = append(roots, workspaceDir)
	}
	return roots
}

type agentInstallStep struct {
	id       string
	agent    model.AgentID
	homeDir  string
	profile  system.PlatformProfile
	progress pipeline.ProgressFunc
}

func (s agentInstallStep) ID() string {
	return s.id
}

// Run installs nothing. Gentle AI never installs an agent runtime; selected
// agents are configuration targets regardless of whether their runtime is
// present, and the step only marks each selected agent in the plan.
func (s agentInstallStep) Run() error {
	return nil
}

type componentApplyStep struct {
	id           string
	component    model.ComponentID
	homeDir      string
	workspaceDir string
	scope        InstallScope
	agents       []model.AgentID
	selection    model.Selection
	profile      system.PlatformProfile
	state        *runtimeState

	backgroundPolicy bool
}

type communityToolInstallStep struct {
	id           string
	tool         model.CommunityToolID
	workspaceDir string
	homeDir      string
	agents       []model.AgentID
	state        *runtimeState
}

func (s communityToolInstallStep) ID() string { return s.id }

func (s communityToolInstallStep) Run() error {
	if _, err := installCommunityToolWithHome(s.tool, s.workspaceDir, s.homeDir, communitytool.RunnerFunc(runCommand), communitytool.DetectorFunc(cmdLookPath)); err != nil {
		return fmt.Errorf("install community tool %q: %w", s.tool, err)
	}
	return nil
}

func (s componentApplyStep) ID() string {
	return s.id
}

// resolveAdapters creates adapters for each agent ID, skipping unsupported ones.
func resolveAdapters(agentIDs []model.AgentID) []agents.Adapter {
	adapters := make([]agents.Adapter, 0, len(agentIDs))
	for _, id := range agentIDs {
		adapter, err := agents.NewAdapter(id)
		if err != nil {
			continue
		}
		adapters = append(adapters, adapter)
	}
	return adapters
}

func shouldRefreshWindowsEngram(profile system.PlatformProfile, resolvedPath string, pathEntries []string) bool {
	if profile.OS != "windows" || profile.PackageManager == "brew" || strings.TrimSpace(resolvedPath) == "" {
		return false
	}
	return len(engramBinaryDirsOnPath(pathEntries, profile.OS)) > 1
}

func ensureRepairableWindowsEngramShadowing(profile system.PlatformProfile, installedPath, managedDir string) error {
	userEntries, err := userPathEntries(profile.OS)
	if err != nil {
		return fmt.Errorf("read user PATH: %w", err)
	}

	staleDir := filepath.Dir(installedPath)
	if !pathEntriesContainDir(userEntries, staleDir) {
		return fmt.Errorf("%s is not in the user PATH, so user-scoped PATH repair cannot guarantee future shells will resolve %s before %s", staleDir, managedDir, staleDir)
	}

	return nil
}

func pathEntriesContainDir(entries []string, dir string) bool {
	dir = strings.Trim(strings.TrimSpace(dir), `"`)
	if dir == "" {
		return false
	}
	for _, entry := range entries {
		entry = strings.Trim(strings.TrimSpace(entry), `"`)
		if entry == "" {
			continue
		}
		if strings.EqualFold(filepath.Clean(entry), filepath.Clean(dir)) {
			return true
		}
	}
	return false
}

func engramBinaryDirsOnPath(pathEntries []string, goos string) []string {
	var dirs []string
	for _, entry := range pathEntries {
		entry = strings.Trim(strings.TrimSpace(entry), `"`)
		if entry == "" {
			continue
		}
		binaryName := "engram"
		if goos == "windows" {
			binaryName = "engram.exe"
		}
		candidate := filepath.Join(entry, binaryName)
		if _, err := os.Stat(candidate); err == nil {
			dirs = append(dirs, entry)
		}
	}
	return dirs
}

func resolveEngramVersion(command string) (string, error) {
	if strings.TrimSpace(command) == "" || command == "engram" {
		return verifyEngramVersion()
	}
	return verifyEngramVersionCommand(command)
}

func resolveEngramProtocolFlag(ctx context.Context, command string) (string, error) {
	if strings.TrimSpace(command) == "" || command == "engram" {
		return probeEngramProtocolFlag(ctx)
	}
	return probeEngramProtocolFlagCommand(ctx, command)
}

func splitPathForOS(value, goos string) []string {
	separator := string(os.PathListSeparator)
	if goos == "windows" {
		separator = ";"
	}
	if value == "" {
		return nil
	}
	return strings.Split(value, separator)
}

func (s componentApplyStep) Run() error {
	adapters := resolveAdapters(s.agents)

	switch s.component {
	case model.ComponentEngram:
		engramCommand := "engram"
		var installErr error
		if callerBinary, ok := engram.CallerBinary(); ok {
			// A caller-supplied Engram is used as is: no brew, download, go
			// install or PATH change. RunPreparedInstall already checked it runs.
			engramCommand = callerBinary
		} else if installedPath, found := resolveEngramInstalledPath(s.profile); !found {
			// Engram not on PATH — install it.
			if s.profile.PackageManager == "brew" {
				// macOS (or Linux with Homebrew): use brew tap + brew install.
				commands, err := engram.InstallCommand(s.profile)
				if err != nil {
					return fmt.Errorf("resolve install command for component %q: %w", s.component, err)
				}
				commands = withResolvedBrewCommand(commands)
				installErr = runCommandSequence(commands)
				if installErr == nil {
					if installedPath, found := resolveEngramInstalledPath(s.profile); found {
						engramCommand = installedPath
					}
				}
			} else if binaryPath, err := engramDownloadFn(s.profile); err != nil {
				// Linux / Windows: download the pre-built binary from GitHub Releases.
				// No Go required — engram ships pre-built binaries.
				installErr = fmt.Errorf("download engram binary: %w", err)
			} else {
				// Add the install directory to PATH so subsequent commands
				// (engram setup, engram.Inject → resolveEngramCommand) can find it.
				// On Windows this also persists the change to the user registry via PowerShell.
				binDir := filepath.Dir(binaryPath)
				if err := addUserPath(binDir); err != nil {
					// Non-fatal: warn but continue — the binary was downloaded successfully.
					fmt.Fprintf(os.Stderr, "WARNING: could not add %s to PATH: %v\n", binDir, err)
				}
				engramCommand = binaryPath
			}
			if installErr != nil {
				if s.selection.HasComponent(model.ComponentEngram) {
					return installErr
				}
				// engram was auto-added as an sdd dependency, not requested. Its
				// failure must not abort the components the user asked for, and
				// no configuration may point at a binary that does not exist:
				// report the missing binary as a warning with its own install
				// command and leave the engram step entirely (#3725).
				fmt.Fprintf(os.Stderr, "WARNING: engram could not be installed: %v\nInstall it with `%s`.\n", installErr, engramInstallCommand(s.agents))
				if s.state != nil {
					s.state.engramVersionResolved = true
					s.state.engramVersionErr = installErr
				}
				return nil
			}
		} else if shouldRefreshWindowsEngram(s.profile, installedPath, pathEnvEntries(s.profile)) {
			binaryPath, err := engramDownloadFn(s.profile)
			if err != nil {
				return fmt.Errorf("refresh shadowed engram binary: %w", err)
			}
			engramCommand = binaryPath
			binDir := filepath.Dir(binaryPath)
			if err := ensureRepairableWindowsEngramShadowing(s.profile, installedPath, binDir); err != nil {
				return fmt.Errorf("repair Windows Engram PATH shadowing: refreshed managed Engram at %s, but cannot safely repair PATH order: %w. Move %s before %s in your user PATH or remove the stale Machine/System PATH entry, then rerun install", binaryPath, err, binDir, filepath.Dir(installedPath))
			}
			if err := ensureUserPathFirst(binDir); err != nil {
				return fmt.Errorf("repair Windows Engram PATH shadowing: refreshed managed Engram at %s, but could not move %s ahead of stale PATH entry %s: %w. Move %s before %s in your user PATH, then rerun install", binaryPath, binDir, installedPath, err, binDir, filepath.Dir(installedPath))
			}
			fmt.Fprintf(os.Stderr, "WARNING: multiple engram.exe entries were found on PATH and %s resolved first. Refreshed managed Engram at %s and moved %s ahead of the stale entry in the user PATH.\n", installedPath, binaryPath, binDir)
		} else {
			engramCommand = installedPath
		}
		setupMode := engram.ParseSetupMode(os.Getenv(engram.SetupModeEnvVar))
		setupStrict := engram.ParseSetupStrict(os.Getenv(engram.SetupStrictEnvVar))

		// Resolve the installed engram version once (Decision 1 gate). Errors are
		// intentionally ignored for gating purposes: an empty version string
		// safely falls back to the full protocol section and the full setup
		// verdict for every adapter. The result (including the error) is cached
		// on s.state so the post-apply health check reuses it instead of
		// shelling out to `engram version` a second time (JD-016).
		engramVersion, versionErr := resolveEngramVersion(engramCommand)
		if s.state != nil {
			s.state.engramVersionResolved = true
			s.state.engramVersion = engramVersion
			s.state.engramVersionErr = versionErr
		}

		// Probe --protocol support once before the adapter loop (Decision 4),
		// but only when at least one selected adapter will actually attempt
		// `engram setup` under setupMode (JD-013): under
		// GENTLE_AI_ENGRAM_SETUP_MODE=off, ShouldAttemptSetup is false for
		// every adapter, no setup invocation ever happens, and the probe's
		// result would never be used — so skip the (up to 5s) probe
		// entirely rather than run it unconditionally.
		willAttemptSetup := false
		for _, adapter := range adapters {
			if installErr == nil && engram.ShouldAttemptSetup(setupMode, adapter.Agent()) {
				willAttemptSetup = true
				break
			}
		}
		protocolFlagSupported := false
		if willAttemptSetup {
			if stdout, err := resolveEngramProtocolFlag(context.Background(), engramCommand); err == nil {
				protocolFlagSupported = strings.Contains(stdout, "--protocol")
			}
		}

		attemptedSlugs := make(map[string]struct{}, len(adapters))
		for _, adapter := range adapters {
			if willAttemptSetup && engram.ShouldAttemptSetup(setupMode, adapter.Agent()) {
				slug, _ := engram.SetupAgentSlug(adapter.Agent())
				if _, seen := attemptedSlugs[slug]; !seen {
					setupArgs := []string{"setup", slug}
					if protocolFlagSupported {
						mode := "full"
						if engram.IsVerifiedSlimAdapter(adapter.Agent(), engramVersion) {
							mode = "slim"
						}
						setupArgs = append(setupArgs, "--protocol="+mode)
					}
					if err := runCommand(engramCommand, setupArgs...); err != nil {
						if setupStrict {
							return fmt.Errorf("engram setup for %q: %w", adapter.Agent(), err)
						}
					}
					attemptedSlugs[slug] = struct{}{}
				}
			}
			engramOpts := engram.InjectOptions{
				CodexOrchestratorAssignment: s.selection.CodexOrchestratorAssignment,
				CodexServiceTier:            s.selection.CodexServiceTier,
				CodexManagedServiceTier:     s.selection.CodexManagedServiceTier,
				CodexCarrilModelAssignments: s.selection.CodexCarrilModelAssignments,
				CodexModelAssignments:       s.selection.CodexModelAssignments,
				Version:                     engramVersion,
			}
			var err error
			targetDir := componentInjectionDirScoped(s.homeDir, s.workspaceDir, s.scope, adapter)
			if s.scope == ScopeWorkspace {
				_, err = engram.InjectWorkspaceWithOptions(targetDir, adapter, engramOpts)
			} else {
				var injected engram.InjectionResult
				injected, err = engram.InjectWithOptions(targetDir, adapter, engramOpts)
				s.state.noteCodexServiceTier(injected)
			}
			if err != nil {
				return fmt.Errorf("inject engram for %q: %w", adapter.Agent(), err)
			}
		}
		return nil
	case model.ComponentContext7:
		for _, adapter := range adapters {
			targetDir := componentInjectionDirScoped(s.homeDir, s.workspaceDir, s.scope, adapter)
			if _, err := mcp.Inject(s.homeDir, targetDir, adapter); err != nil {
				return fmt.Errorf("inject context7 for %q: %w", adapter.Agent(), err)
			}
		}
		return nil
	case model.ComponentPersona:
		for _, adapter := range adapters {
			targetDir := componentInjectionDirScoped(s.homeDir, s.workspaceDir, s.scope, adapter)
			if _, err := injectInstallPersona(targetDir, adapter); err != nil {
				return fmt.Errorf("inject persona for %q: %w", adapter.Agent(), err)
			}
		}
		return nil
	case model.ComponentPermission:
		for _, adapter := range adapters {
			if _, err := permissions.Inject(s.homeDir, adapter); err != nil {
				return fmt.Errorf("inject permissions for %q: %w", adapter.Agent(), err)
			}
		}
		return nil
	case model.ComponentSDD:
		// Legacy persisted selections are readable, but their retired assets are not reinstalled.
		return nil
	case model.ComponentSkills:
		skillIDs := selectedSkillIDs(s.selection)
		if len(skillIDs) == 0 {
			return nil
		}
		for _, adapter := range adapters {
			targetDir := componentInjectionDirScoped(s.homeDir, s.workspaceDir, s.scope, adapter)
			if _, err := skills.Inject(targetDir, adapter, skillIDs); err != nil {
				return fmt.Errorf("inject skills for %q: %w", adapter.Agent(), err)
			}
		}
		return nil
	case model.ComponentTheme:
		for _, adapter := range adapters {
			if _, err := theme.Inject(s.homeDir, adapter); err != nil {
				return fmt.Errorf("inject theme for %q: %w", adapter.Agent(), err)
			}
		}
		return nil
	case model.ComponentClaudeTheme:
		for _, adapter := range adapters {
			if _, err := theme.InjectVisualThemes(s.homeDir, adapter); err != nil {
				return fmt.Errorf("inject visual themes for %q: %w", adapter.Agent(), err)
			}
		}
		return nil
	default:
		return fmt.Errorf("component %q is not supported in install runtime", s.component)
	}
}

func ensureGoAvailableAfterInstall(profile system.PlatformProfile) error {
	if _, err := cmdLookPath("go"); err == nil {
		return nil
	}

	if profile.OS != "windows" {
		return fmt.Errorf("go was installed but is still not available in PATH")
	}

	for _, candidate := range windowsGoCandidates() {
		if candidate == "" {
			continue
		}
		if _, err := osStat(candidate); err == nil {
			binDir := filepath.Dir(candidate)
			currentPath := os.Getenv("PATH")
			if currentPath == "" {
				return osSetenv("PATH", binDir)
			}
			return osSetenv("PATH", binDir+string(os.PathListSeparator)+currentPath)
		}
	}

	return fmt.Errorf("go was installed but is still not available in PATH; restart the terminal and retry")
}

func windowsGoCandidates() []string {
	programFiles := os.Getenv("ProgramFiles")
	programFilesX86 := os.Getenv("ProgramFiles(x86)")

	return []string{
		filepath.Join(programFiles, "Go", "bin", "go.exe"),
		filepath.Join(programFilesX86, "Go", "bin", "go.exe"),
		`C:\Program Files\Go\bin\go.exe`,
	}
}

// The seam lets native tests add a post-publication failure while still using
// the public TUI execution boundary and the real compatibility writer.
var tuiInstallStagePlan = func(runtime *installRuntime) pipeline.StagePlan {
	return runtime.stagePlan()
}

// ExecuteTUIInstallRecordingCodexServiceTier runs a TUI install and returns
// its orchestrator plus the service tier engram left in Codex's config.toml
// (nil = not written), for state to record.
func ExecuteTUIInstallRecordingCodexServiceTier(homeDir string, selection model.Selection, resolved planner.ResolvedPlan, profile system.PlatformProfile, onProgress pipeline.ProgressFunc) (pipeline.ExecutionResult, *pipeline.Orchestrator, *string) {
	return executeTUIInstall(homeDir, selection, resolved, profile, onProgress)
}

func executeTUIInstall(homeDir string, selection model.Selection, resolved planner.ResolvedPlan, profile system.PlatformProfile, onProgress pipeline.ProgressFunc) (pipeline.ExecutionResult, *pipeline.Orchestrator, *string) {
	runtime, err := newInstallRuntime(homeDir, ScopeGlobal, selection, resolved, profile)
	if err != nil {
		return pipeline.ExecutionResult{Err: err}, nil, nil
	}
	defer runtime.state.cleanupCompatibilityTransaction()
	runtime.progress = onProgress
	orchestrator := pipeline.NewOrchestrator(pipeline.DefaultRollbackPolicy(), pipeline.WithFailurePolicy(pipeline.ContinueOnError), pipeline.WithProgressFunc(onProgress), pipeline.WithTerminalCleanup(runtime.state.cleanupRollbackSnapshot))
	result := orchestrator.Execute(tuiInstallStagePlan(runtime))
	result.ManualActions = append(result.ManualActions, runtime.state.nativeReviewActions...)
	result.ManualActions = append(result.ManualActions, runtime.state.retiredSDDActions...)
	return result, orchestrator, runtime.state.codexServiceTier
}

// RenderInstallManualActions renders non-fatal completion actions after the
// normal verification report so CLI users receive the same drift guidance.
func RenderInstallManualActions(result InstallResult) string {
	actions := append([]string(nil), result.ManualActions...)
	if len(actions) == 0 {
		return ""
	}
	return "\nManual actions required:\n- " + strings.Join(actions, "\n- ") + "\n"
}

// ResolveInstallProfile returns the platform profile from detection, defaulting to darwin/brew.
func ResolveInstallProfile(detection system.DetectionResult) system.PlatformProfile {
	if detection.System.Profile.OS != "" {
		return detection.System.Profile
	}

	return system.PlatformProfile{
		OS:             "darwin",
		PackageManager: "brew",
		Supported:      true,
	}
}

func isExecutableFile(path string) bool {
	info, err := osStat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	// Windows has no POSIX executable permission bit (os.FileMode.Perm()
	// never carries 0o111 there), so a regular file at a known binary path
	// is treated as usable without a permission check.
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}

func standardHomebrewExecutable(name string) (string, bool) {
	for _, binDir := range []string{
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/home/linuxbrew/.linuxbrew/bin",
	} {
		path := filepath.Join(binDir, name)
		if isExecutableFile(path) {
			return path, true
		}
	}
	return "", false
}

// resolveEngramInstalledPath finds an existing Engram even when the installer's
// inherited PATH omits a standard Homebrew prefix (#4020).
func resolveEngramInstalledPath(profile system.PlatformProfile) (string, bool) {
	if path, err := cmdLookPath("engram"); err == nil {
		return path, true
	}
	if profile.OS == "darwin" || profile.PackageManager == "brew" {
		return standardHomebrewExecutable("engram")
	}
	return "", false
}

// resolveBrewCommand avoids environment-sensitive shell profile probing. The
// inherited PATH and Homebrew's documented standard prefixes are sufficient.
func resolveBrewCommand() string {
	if path, err := cmdLookPath("brew"); err == nil {
		return path
	}
	if path, found := standardHomebrewExecutable("brew"); found {
		return path
	}
	return "brew"
}

func withResolvedBrewCommand(commands [][]string) [][]string {
	brewPath := ""
	rewritten := make([][]string, len(commands))
	for i, command := range commands {
		if len(command) > 0 && command[0] == "brew" {
			if brewPath == "" {
				brewPath = resolveBrewCommand()
			}
			rewritten[i] = append([]string{brewPath}, command[1:]...)
			continue
		}
		rewritten[i] = command
	}
	return rewritten
}

// runCommandSequence runs each command in the sequence one at a time, stopping on first error.
func runCommandSequence(commands [][]string) error {
	return runCommandSequenceWithProgress(commands, nil, "")
}

// runCommandSequenceWithProgress is the common command runner used by agent
// installation steps. The optional callback reports each command as a generic
// pipeline progress event; the command producer remains responsible for the
// command content, so the pipeline does not need to know any agent-specific
// package names.
func runCommandSequenceWithProgress(commands [][]string, progress pipeline.ProgressFunc, stepPrefix string) error {
	if len(commands) == 0 {
		return fmt.Errorf("empty command sequence")
	}

	for _, command := range commands {
		if len(command) == 0 {
			return fmt.Errorf("empty command in sequence")
		}

		stepID := ""
		if stepPrefix != "" {
			stepID = stepPrefix + ":" + strings.Join(command, " ")
		}
		if progress != nil {
			progress(pipeline.ProgressEvent{StepID: stepID, Status: pipeline.StepStatusRunning})
		}

		err := runCommand(command[0], command[1:]...)
		if err != nil {
			if progress != nil {
				progress(pipeline.ProgressEvent{StepID: stepID, Status: pipeline.StepStatusFailed, Err: err})
			}
			return fmt.Errorf("run command %q: %w", strings.Join(command, " "), err)
		}
		if progress != nil {
			progress(pipeline.ProgressEvent{StepID: stepID, Status: pipeline.StepStatusSucceeded})
		}
	}

	return nil
}

// homebrewNoSideEffectEnv are the environment variables executeCommand adds
// to a brew invocation so gentle-ai's own tap/install/reinstall steps never
// trigger Homebrew's slow, network-dependent auto-update or its
// post-install cache cleanup as a side effect of an unrelated install.
var homebrewNoSideEffectEnv = []string{
	"HOMEBREW_NO_AUTO_UPDATE=1",
	"HOMEBREW_NO_INSTALL_CLEANUP=1",
}

// commandEnv returns the environment executeCommand should use for name,
// derived from base (typically os.Environ()). When name's base is "brew"
// (matching both the literal command and resolveBrewCommand's resolved
// absolute path) and the invocation is not the user-requested "brew
// update"/"brew upgrade" path (see internal/update/upgrade's own
// brewUpgrade), it adds homebrewNoSideEffectEnv, never overriding a value
// already present in base — an explicit user override always wins.
func commandEnv(name string, args []string, base []string) []string {
	if filepath.Base(name) != "brew" {
		return base
	}
	if len(args) > 0 && (args[0] == "update" || args[0] == "upgrade") {
		return base
	}

	// Copy so appending never writes into the caller's backing array.
	env := append([]string(nil), base...)
	for _, kv := range homebrewNoSideEffectEnv {
		key := strings.SplitN(kv, "=", 2)[0]
		if !envHasKey(env, key) {
			env = append(env, kv)
		}
	}
	return env
}

// envHasKey reports whether env already sets key, regardless of value.
func envHasKey(env []string, key string) bool {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return true
		}
	}
	return false
}

func executeCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Env = commandEnv(name, args, os.Environ())
	system.EnsureCommandDir(cmd)

	if streamCommandOutput {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		if len(output) > 0 {
			return fmt.Errorf("%w\noutput:\n%s", err, strings.TrimSpace(string(output)))
		}
		return err
	}

	return nil
}

// selectedSkillIDs returns the skill IDs to install. If the selection
// has explicit skills, those are used; otherwise skills are derived from the preset.
func selectedSkillIDs(selection model.Selection) []model.SkillID {
	if len(selection.Skills) > 0 {
		return selection.Skills
	}

	return skills.SkillsForPreset(selection.Preset)
}

// installBackupTargets lists every path an install is about to rewrite. An
// install that carries the explicit Claude module opt-in also plans every
// pilot path, so the snapshot can restore it.
func installBackupTargets(homeDir, workspaceDir string, scope InstallScope, selection model.Selection, resolved planner.ResolvedPlan, claudeModules bool) ([]string, error) {
	paths := map[string]struct{}{}
	adapters := resolveAdapters(resolved.Agents)

	for _, component := range resolved.OrderedComponents {
		if component == model.ComponentSDD {
			continue
		}
		for _, path := range componentPathsWithWorkspaceScoped(homeDir, workspaceDir, scope, selection, adapters, component) {
			paths[path] = struct{}{}
		}
		if component == model.ComponentContext7 {
			for _, path := range claudeMCPSettingsCleanupPaths(homeDir, workspaceDir, scope, adapters) {
				paths[path] = struct{}{}
			}
		}
		if component == model.ComponentEngram && scope == ScopeGlobal {
			for _, adapter := range adapters {
				if adapter.Agent() == model.AgentClaudeCode {
					paths[adapter.MCPConfigPath(homeDir, "engram")] = struct{}{}
				}
			}
		}
		// Persona backup captures the managed output-style file.
		if component == model.ComponentPersona {
			for _, adapter := range adapters {
				if !adapter.SupportsOutputStyles() {
					continue
				}
				if path := persona.OutputStylePath(adapter.OutputStyleDir(componentPathDirScoped(homeDir, workspaceDir, scope, adapter, model.ComponentPersona))); path != "" {
					paths[path] = struct{}{}
				}
			}
		}
	}
	// Routing guidance is delivered per agent outside the component loop, so a
	// selection whose components do not happen to cover the same file would be
	// rewritten without ever having been snapshotted (issue #1794).
	for _, path := range routingGuidancePathsWithClaudeModules(homeDir, workspaceDir, scope, adapters, claudeModules) {
		paths[path] = struct{}{}
	}
	for _, path := range retiredSDDAssetBackupPaths(homeDir, workspaceDir, scope, resolved.Agents) {
		paths[path] = struct{}{}
	}
	for _, adapter := range adapters {
		if path := agentguidance.StrictTDDPath(routingGuidanceDir(homeDir, workspaceDir, scope, adapter), adapter.Agent()); path != "" {
			paths[path] = struct{}{}
		}
	}
	for _, adapter := range adapters {
		if adapter.Agent() == model.AgentCodex {
			paths[filepath.Join(adapter.GlobalConfigDir(homeDir), "hooks.json")] = struct{}{}
		}
		for _, path := range retiredSDDAgentBackupPaths(homeDir, scope, []agents.Adapter{adapter}) {
			paths[path] = struct{}{}
		}
		// Retained Claude hooks are installed independently of optional
		// components. Snapshot the same scoped settings their writer uses.
		if adapter.Agent() == model.AgentClaudeCode {
			if path := adapter.SettingsPath(ResolveAgentConfigDir(scope, homeDir, workspaceDir)); path != "" {
				paths[path] = struct{}{}
			}
		}
		// Native review and Judgment Day agents are installed independently of SDD.
		// Retired review agents are listed too: the installer may remove them.
		if names := reviewassets.NativeAgentFileNames(adapter.Agent()); len(names) > 0 {
			dir := adapter.SubAgentsDir(componentInjectionDirScoped(homeDir, workspaceDir, scope, adapter))
			paths[filepath.Join(dir, reviewassets.OwnershipLedgerFilename)] = struct{}{}
			for _, name := range names {
				paths[filepath.Join(dir, name)] = struct{}{}
			}
		}
	}
	adapterSkillPaths, err := adapterSkillBackupTargets(homeDir, workspaceDir, scope, selection, adapters)
	if err != nil {
		return nil, err
	}
	for _, path := range adapterSkillPaths {
		paths[path] = struct{}{}
	}
	if !usesAnchoredCompatibilityTransaction() && needsCompatibilitySkillsRefresh(resolved.OrderedComponents) {
		skillDir, ok, err := compatibilitySkillsDir(homeDir)
		if err != nil {
			return nil, err
		}
		if ok {
			compatibilityPaths, err := compatibilitySkillFiles(skillDir, resolved.OrderedComponents, selection)
			if err != nil {
				return nil, err
			}
			for _, path := range compatibilityPaths {
				paths[path] = struct{}{}
			}
		}
	}
	if selection.HasCommunityTool(model.CommunityToolCodeGraph) {
		for _, path := range communitytool.CodeGraphManagedPaths(homeDir) {
			paths[path] = struct{}{}
		}
	}
	targets := make([]string, 0, len(paths))
	for path := range paths {
		targets = append(targets, path)
	}
	sort.Strings(targets)
	return targets, nil
}

func adapterSkillBackupTargets(homeDir, workspaceDir string, scope InstallScope, selection model.Selection, adapters []agents.Adapter) ([]string, error) {
	var paths []string
	for _, adapter := range adapters {
		if !adapter.SupportsSkills() {
			continue
		}
		skillDir := adapter.SkillsDir(componentInjectionDirScoped(homeDir, workspaceDir, scope, adapter))
		if skillDir == "" {
			continue
		}
		if slices.Contains(selection.Components, model.ComponentSkills) {
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

// skillSupportBackupTargets declares the shared references, skill commands,
// and obsolete shared marker the skills component writes or removes.
func skillSupportBackupTargets(targetDir string, adapter agents.Adapter, selection model.Selection) ([]string, error) {
	support, err := skills.SupportFilePaths(targetDir, adapter, selectedSkillIDs(selection))
	if err != nil {
		return nil, fmt.Errorf("enumerate %s skill support backup targets: %w", adapter.Agent(), err)
	}
	if skillDir := adapter.SkillsDir(targetDir); skillDir != "" {
		support = append(support, skills.LegacySharedMarkerPath(skillDir))
	}
	return support, nil
}

// claudeMCPSettingsCleanupPaths returns legacy Claude settings files that MCP
// injection may rewrite while removing an inert mcpServers block. These paths
// belong in the rollback snapshot, but not in post-apply verification because
// cleanup is best-effort and the file may not exist.
func claudeMCPSettingsCleanupPaths(homeDir, workspaceDir string, scope InstallScope, adapters []agents.Adapter) []string {
	paths := []string{}
	for _, adapter := range adapters {
		if adapter.Agent() != model.AgentClaudeCode || adapter.MCPStrategy() != model.StrategySeparateMCPFiles {
			continue
		}
		targetDir := componentPathDirScoped(homeDir, workspaceDir, scope, adapter, model.ComponentContext7)
		if path := adapter.SettingsPath(targetDir); path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

// routingGuidancePaths declares the files agentRoutingGuidanceStep rewrites for
// the given adapters.
//
// The target directory is resolved exactly the way that step resolves it, and
// the paths themselves come from the injector's own delivery dispatch, so the
// backup contract cannot drift from what is actually written.
func routingGuidancePaths(homeDir, workspaceDir string, scope InstallScope, adapters []agents.Adapter) []string {
	return routingGuidancePathsWithClaudeModules(homeDir, workspaceDir, scope, adapters, false)
}

// routingGuidancePathsWithClaudeModules also takes install's explicit Claude
// module opt-in, which the routing step receives too.
func routingGuidancePathsWithClaudeModules(homeDir, workspaceDir string, scope InstallScope, adapters []agents.Adapter, claudeModules bool) []string {
	paths := []string{}
	for _, adapter := range adapters {
		// Mirrors the same skip agentRoutingGuidanceStep.Run() applies: an
		// agent without a managed system prompt gets no routing guidance
		// write, so it must not be declared as a backup target either.
		if !adapter.SupportsSystemPrompt() {
			continue
		}
		targetDir := routingGuidanceDir(homeDir, workspaceDir, scope, adapter)
		options, err := scopedRoutingGuidanceOptions(homeDir, workspaceDir, scope, adapter, claudeModules)
		if err != nil {
			// The guidance step runs the same detection before any write and
			// fails loudly, so there is nothing of this agent to snapshot.
			continue
		}
		// Always with options: the Claude module delivery the step will use
		// must not be dropped.
		routing, err := agentguidance.RoutingPathsWithOptions(targetDir, adapter.Agent(), options)
		if err != nil {
			// The guidance step resolves the same delivery and fails loudly when
			// it runs. Declaring a target we could not resolve would only add a
			// path to the snapshot that is never written.
			continue
		}
		paths = append(paths, routing...)
	}
	return paths
}

func componentPathsWithWorkspaceScoped(homeDir, workspaceDir string, scope InstallScope, selection model.Selection, adapters []agents.Adapter, component model.ComponentID) []string {
	paths := []string{}
	for _, adapter := range adapters {
		targetDir := componentPathDirScoped(homeDir, workspaceDir, scope, adapter, component)
		switch component {
		case model.ComponentEngram:
			switch adapter.MCPStrategy() {
			case model.StrategySeparateMCPFiles:
				if adapter.Agent() == model.AgentClaudeCode && scope == ScopeGlobal {
					paths = append(paths, claude.UserConfigPath(homeDir))
				} else {
					paths = append(paths, adapter.MCPConfigPath(targetDir, "engram"))
				}
			case model.StrategyTOMLFile:
				if p := adapter.MCPConfigPath(targetDir, "engram"); p != "" {
					paths = append(paths, p)
					// Track the gentle-ai files written alongside the Codex config.toml
					// so they are restored on rollback and removed on uninstall.
					codexHomeDir := filepath.Dir(p)
					paths = append(paths,
						filepath.Join(codexHomeDir, "engram-instructions.md"),
						filepath.Join(codexHomeDir, "engram-compact-prompt.md"),
					)
					paths = append(paths, codexagent.SddProfilePaths(codexHomeDir)...)
				}
			}
			if adapter.SystemPromptStrategy() == model.StrategyMarkdownSections {
				paths = append(paths, adapter.SystemPromptFile(targetDir))
			}
		case model.ComponentSDD:
			if adapter.SupportsSystemPrompt() {
				paths = append(paths, adapter.SystemPromptFile(targetDir))
			}
			if adapter.SupportsSlashCommands() {
				paths = append(paths, legacyassets.SlashCommandPaths(adapter.Agent(), adapter.CommandsDir(targetDir))...)
			}
			paths = append(paths, sddSubAgentPaths(targetDir, adapter)...)
			if adapter.Agent() == model.AgentCodex {
				// SDD installs the Codex skill-registry hook outside the skills
				// directory, so it must share the component's backup contract.
				paths = append(paths, filepath.Join(adapter.GlobalConfigDir(homeDir), "hooks.json"))
			}
		case model.ComponentSkills:
			for _, skillID := range selectedSkillIDs(selection) {
				if skills.IsSDDSkill(skillID) {
					continue
				}
				path := skills.SkillPathForAgent(targetDir, adapter, skillID)
				if path != "" {
					paths = append(paths, path)
				}
			}
			if len(selectedSkillIDs(selection)) > 0 {
				// Enumeration only fails on a corrupt build; the skills step then
				// fails loudly while writing, so no path is declared here.
				if support, err := skills.SupportFilePaths(targetDir, adapter, selectedSkillIDs(selection)); err == nil {
					paths = append(paths, support...)
				}
			}
		case model.ComponentContext7:
			switch adapter.MCPStrategy() {
			case model.StrategySeparateMCPFiles:
				if adapter.Agent() == model.AgentClaudeCode {
					if targetDir == homeDir {
						// Context7 injection writes ~/.claude.json (issue #1868).
						paths = append(paths, claude.UserConfigPath(homeDir))
					} else {
						// Workspace scope writes <project-root>/.mcp.json, the file
						// Claude Code loads project-scoped MCP servers from (issue #2213).
						// The legacy .claude/settings.json block is a best-effort
						// cleanup, not a guaranteed write, so it is not declared as a
						// verification target (declaring it would force a false-negative
						// when the file never existed).
						paths = append(paths, filepath.Join(targetDir, ".mcp.json"))
					}
					break
				}
				paths = append(paths, adapter.MCPConfigPath(targetDir, "context7"))
			case model.StrategyTOMLFile:
				if p := adapter.MCPConfigPath(targetDir, "context7"); p != "" {
					paths = append(paths, p)
				}
			}
		case model.ComponentPersona:
			if adapter.SupportsSystemPrompt() {
				paths = append(paths, adapter.SystemPromptFile(targetDir))
			}
			if adapter.SupportsOutputStyles() {
				if stylePath := persona.OutputStylePath(adapter.OutputStyleDir(targetDir)); stylePath != "" {
					paths = append(paths, stylePath)
					if p := adapter.SettingsPath(targetDir); p != "" {
						paths = append(paths, p)
					}
				}
			}
		case model.ComponentPermission:
			if p := permissions.TargetPath(homeDir, adapter); p != "" {
				paths = append(paths, p)
			}
		case model.ComponentTheme:
			if p := adapter.SettingsPath(homeDir); p != "" {
				paths = append(paths, p)
			}
		case model.ComponentClaudeTheme:
			paths = append(paths, theme.VisualThemePaths(homeDir, adapter)...)
		}
	}

	return paths
}

// routingGuidanceOptions gives every runtime the review contract source its
// orchestrator render needs.
func routingGuidanceOptions(homeDir, workspaceDir string, adapter agents.Adapter) agentguidance.RoutingOptions {
	return agentguidance.RoutingOptions{ReviewContract: routingReviewContract}
}

// scopedRoutingGuidanceOptions extends routingGuidanceOptions with the one
// scope-dependent choice: a global Claude install continues the module pilot
// whose ledger is already installed under homeDir (#5256), and creates it only
// when claudeModules carries install's explicit opt-in. Otherwise, in the
// workspace scope and for every other agent the options are unchanged. The
// ledger is always looked up, so a ledger that cannot be inspected is an error
// with or without the opt-in.
//
// The routing step and the install/sync snapshot planner both resolve through
// here, so the snapshot covers the core, every module and the ledger. After a
// failure, install and sync restore those files from that snapshot like any
// other: an edit made while the run was in progress can be overwritten and a
// file created meanwhile removed. Nothing here makes that restore concurrency
// safe or atomic.
func scopedRoutingGuidanceOptions(homeDir, workspaceDir string, scope InstallScope, adapter agents.Adapter, claudeModules bool) (agentguidance.RoutingOptions, error) {
	options := routingGuidanceOptions(homeDir, workspaceDir, adapter)
	if scope != ScopeGlobal || adapter.Agent() != model.AgentClaudeCode {
		return options, nil
	}
	pilot, err := agentguidance.ClaudeGlobalModulePaths(homeDir)
	if err != nil {
		return agentguidance.RoutingOptions{}, fmt.Errorf("detect installed Claude module pilot: %w", err)
	}
	options.ClaudeGlobalModules = claudeModules || len(pilot) > 0
	return options, nil
}

// routingGuidanceDir resolves the installation root routing guidance is
// delivered under: the ordinary scoped resolution. The guidance step and the
// backup contract both resolve through here so the snapshot cannot drift from
// what the injector writes.
func routingGuidanceDir(homeDir, workspaceDir string, scope InstallScope, adapter agents.Adapter) string {
	return componentInjectionDirScoped(homeDir, workspaceDir, scope, adapter)
}

// componentInjectionDirScoped returns the directory to inject component files for the given adapter,
// taking the install scope into account. When scope is ScopeWorkspace, agent-scoped
// components write to workspaceDir instead of the selected agent's global config root.
func componentInjectionDirScoped(homeDir, workspaceDir string, scope InstallScope, adapter agents.Adapter) string {
	return ResolveAgentConfigDir(scope, homeDir, workspaceDir)
}

func nativeReviewCodeGraphGuidanceMarkdown(homeDir string, selected []model.CommunityToolID) string {
	if !shouldInjectNativeReviewCodeGraphGuidance(homeDir, selected) {
		return ""
	}
	return communitytool.CodeGraphGuidanceMarkdown()
}

func shouldInjectNativeReviewCodeGraphGuidance(homeDir string, selected []model.CommunityToolID) bool {
	for _, tool := range selected {
		if tool == model.CommunityToolCodeGraph {
			return true
		}
	}
	return false
}

func communityToolIDsToStrings(tools []model.CommunityToolID) []string {
	if tools == nil {
		return nil
	}
	result := make([]string, 0, len(tools))
	for _, tool := range tools {
		result = append(result, string(tool))
	}
	return result
}

func componentPathDir(homeDir, workspaceDir string, adapter agents.Adapter, component model.ComponentID) string {
	return componentPathDirScoped(homeDir, workspaceDir, ScopeGlobal, adapter, component)
}

func componentPathDirScoped(homeDir, workspaceDir string, scope InstallScope, adapter agents.Adapter, component model.ComponentID) string {
	switch component {
	case model.ComponentEngram, model.ComponentSDD, model.ComponentPersona, model.ComponentSkills, model.ComponentContext7:
		return componentInjectionDirScoped(homeDir, workspaceDir, scope, adapter)
	default:
		return homeDir
	}
}

func sddSubAgentPaths(homeDir string, adapter agents.Adapter) []string {
	if !adapter.SupportsSubAgents() {
		return nil
	}

	entries, err := assets.FS.ReadDir(adapter.EmbeddedSubAgentsDir())
	if err != nil {
		return nil
	}

	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		paths = append(paths, filepath.Join(adapter.SubAgentsDir(homeDir), entry.Name()))
	}

	return paths
}

// verificationComponentPaths excludes retired Codex SDD profiles from active
// health checks. The shared component path inventory retains them for
// snapshots and rollback of user-owned files.
func verificationComponentPaths(homeDir, workspaceDir string, scope InstallScope, selection model.Selection, adapters []agents.Adapter, component model.ComponentID) []string {
	paths := componentPathsWithWorkspaceScoped(homeDir, workspaceDir, scope, selection, adapters, component)
	if component != model.ComponentEngram {
		return paths
	}
	excluded := make(map[string]bool)
	for _, adapter := range adapters {
		targetDir := componentPathDirScoped(homeDir, workspaceDir, scope, adapter, component)
		switch adapter.Agent() {
		case model.AgentCodex:
			if config := adapter.MCPConfigPath(targetDir, "engram"); config != "" {
				for _, profile := range codexagent.SddProfilePaths(filepath.Dir(config)) {
					excluded[profile] = true
				}
			}
		}
	}
	active := make([]string, 0, len(paths))
	for _, path := range paths {
		if !excluded[path] {
			active = append(active, path)
		}
	}
	return active
}

type postApplyVerificationInput struct {
	HomeDir      string
	WorkspaceDir string
	Scope        InstallScope
	Selection    model.Selection
	Resolved     planner.ResolvedPlan
	State        *runtimeState
}

func runPostApplyVerification(input postApplyVerificationInput) verify.Report {
	checks := make([]verify.Check, 0)
	adapters := resolveAdapters(input.Resolved.Agents)

	seenPath := make(map[string]struct{})
	var uniqueFilePaths []string
	for _, component := range input.Resolved.OrderedComponents {
		if component == model.ComponentEngram && !input.Selection.HasComponent(model.ComponentEngram) &&
			input.State != nil && input.State.engramVersionErr != nil {
			// An auto-added engram that could not be installed wrote nothing
			// (#3725); its health check below carries the warning and the
			// install command, so its files are not required here.
			continue
		}
		for _, path := range verificationComponentPaths(input.HomeDir, input.WorkspaceDir, input.Scope, input.Selection, adapters, component) {
			if path == "" {
				continue
			}
			if _, dup := seenPath[path]; dup {
				continue
			}
			seenPath[path] = struct{}{}
			uniqueFilePaths = append(uniqueFilePaths, path)
		}
	}

	for _, currentPath := range uniqueFilePaths {
		path := currentPath
		if isRetiredManagedPath(path) {
			checks = append(checks, verify.Check{
				ID:          "verify:file:" + path,
				Description: "retired managed file removed",
				Run: func(context.Context) error {
					if _, err := os.Stat(path); err != nil {
						if os.IsNotExist(err) {
							return nil
						}
						return err
					}
					return fmt.Errorf("retired managed file still exists; rerun `gentle-ai sync` to finish retiring it")
				},
			})
			continue
		}
		checks = append(checks, verify.Check{
			ID:          "verify:file:" + path,
			Description: "required file exists",
			Run: func(context.Context) error {
				if _, err := os.Stat(path); err != nil {
					return err
				}
				return nil
			},
		})
	}

	if hasComponent(input.Resolved.OrderedComponents, model.ComponentEngram) {
		checks = append(checks, engramHealthChecks(input.State, input.Resolved.Agents)...)
	}

	return verify.BuildReport(verify.RunChecks(context.Background(), checks))
}

// isRetiredManagedPath reports whether path names a managed file that install
// and sync remove instead of write, so verification checks its absence.
func isRetiredManagedPath(path string) bool {
	return legacyassets.IsLegacyClaudeCommandPath(path)
}

func hasComponent(components []model.ComponentID, target model.ComponentID) bool {
	for _, c := range components {
		if c == target {
			return true
		}
	}
	return false
}

func containsAgent(agents []model.AgentID, target model.AgentID) bool {
	for _, agent := range agents {
		if agent == target {
			return true
		}
	}
	return false
}

// engramHealthChecks builds the post-apply engram soft checks. When state
// already carries a resolved `engram version` result (componentApplyStep.Run
// resolves it once for the Decision 1 gate whenever ComponentEngram is
// applied), the version check reuses that result instead of shelling out to
// `engram version` a second time (JD-016). The fallback path (state nil or
// not yet resolved) still routes through the verifyEngramVersion seam var
// rather than calling engram.VerifyVersion() directly, so it stays fakeable
// in tests.
func engramHealthChecks(state *runtimeState, agentIDs []model.AgentID) []verify.Check {
	return []verify.Check{
		{
			ID:          "verify:engram:binary",
			Description: "engram binary on PATH (restart shell if missing)",
			Soft:        true,
			Run: func(context.Context) error {
				if err := engram.VerifyInstalled(); err != nil {
					return fmt.Errorf("%w\nInstall it with `%s`.\nIf engram was installed via `go install`, add it to PATH:\n  %s", err, engramInstallCommand(agentIDs), engramPathGuidance(os.Getenv("SHELL")))
				}
				return nil
			},
		},
		{
			ID:          "verify:engram:version",
			Description: "engram version returns valid output",
			Soft:        true,
			Run: func(context.Context) error {
				if err := engram.VerifyInstalled(); err != nil {
					// Binary not on PATH — skip version check gracefully.
					return nil
				}
				if state != nil && state.engramVersionResolved {
					return state.engramVersionErr
				}
				_, err := verifyEngramVersion()
				return err
			},
		},
	}
}

// engramInstallCommand names the install continuation for a missing engram
// binary so the warning that reports it is actionable on its own.
func engramInstallCommand(agentIDs []model.AgentID) string {
	return fmt.Sprintf("gentle-ai install --agent %s --components engram", joinAgentIDs(agentIDs))
}

func engramPathGuidance(shellPath string) string {
	binDir := goInstallBinDir()
	if strings.Contains(shellPath, "fish") {
		return fmt.Sprintf("set -Ux fish_user_paths %s $fish_user_paths", binDir)
	}
	if strings.Contains(shellPath, "zsh") {
		return fmt.Sprintf("echo 'export PATH=\"%s:$PATH\"' >> ~/.zshrc && source ~/.zshrc", binDir)
	}
	if strings.Contains(shellPath, "bash") {
		return fmt.Sprintf("echo 'export PATH=\"%s:$PATH\"' >> ~/.bashrc && source ~/.bashrc", binDir)
	}
	return fmt.Sprintf("Add %s to your shell PATH and restart the terminal.", binDir)
}

// checkDependenciesStep verifies that required system dependencies are present.
// It logs warnings for missing optional deps but only fails if required deps are missing.
type checkDependenciesStep struct {
	id        string
	profile   system.PlatformProfile
	homeDir   string
	selection model.Selection
	resolved  planner.ResolvedPlan
}

func (s checkDependenciesStep) ID() string {
	return s.id
}

func (s checkDependenciesStep) Run() error {
	// Run detection but do NOT write to stdout/stderr — this step runs
	// inside the Bubble Tea alternate screen in TUI mode, so any raw
	// output corrupts the display (see issue #2). Missing deps are
	// surfaced on the TUI complete screen and by the actual install steps
	// failing with real error messages.
	_ = system.DetectDependencies(context.Background(), s.profile)
	return nil
}

type noopStep struct {
	id string
}

func (s noopStep) ID() string {
	return s.id
}

func (s noopStep) Run() error {
	return nil
}

// claudeAliasesToStrings converts a typed ClaudeModelAlias map to plain strings
// for JSON serialisation in state.json.
func claudeAliasesToStrings(m map[string]model.ClaudeModelAlias) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		// Claude Code owns the main session/orchestrator model; do not persist it
		// as a Gentle AI model assignment.
		if k == "orchestrator" {
			continue
		}
		out[k] = string(v)
	}
	return out
}

func claudeLegacyAssignmentsForState(
	legacy map[string]model.ClaudeModelAlias,
	phase map[string]state.ClaudePhaseAssignmentState,
) map[string]string {
	if len(phase) > 0 {
		return nil
	}
	return claudeAliasesToStrings(legacy)
}

func claudePhaseAssignmentsToState(m map[string]model.ClaudePhaseAssignment) map[string]state.ClaudePhaseAssignmentState {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]state.ClaudePhaseAssignmentState, len(m))
	for k, v := range m {
		if k == "orchestrator" || !v.Valid() {
			continue
		}
		out[k] = state.ClaudePhaseAssignmentState{Model: string(v.Model), Effort: string(v.Effort)}
	}
	return out
}

// codexEffortsToStrings converts a typed CodexEffort map to plain strings
// for JSON serialisation in state.json.
func codexEffortsToStrings(m map[string]model.CodexEffort) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = string(v)
	}
	return out
}

func codexOrchestratorToState(a *model.CodexOrchestratorAssignment) *state.CodexOrchestratorAssignmentState {
	if a == nil {
		return nil
	}
	return &state.CodexOrchestratorAssignmentState{Model: a.Model, Effort: string(a.Effort)}
}

func codexOrchestratorFromState(a *state.CodexOrchestratorAssignmentState) *model.CodexOrchestratorAssignment {
	if a == nil {
		return nil
	}
	return &model.CodexOrchestratorAssignment{Model: a.Model, Effort: model.CodexEffort(a.Effort)}
}
