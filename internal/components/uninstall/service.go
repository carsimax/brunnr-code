package uninstall

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/agents/claude"
	"github.com/carsimax/brunnr-code/internal/assets"
	"github.com/carsimax/brunnr-code/internal/backup"
	"github.com/carsimax/brunnr-code/internal/components/agentguidance"
	"github.com/carsimax/brunnr-code/internal/components/engram"
	"github.com/carsimax/brunnr-code/internal/components/filemerge"
	"github.com/carsimax/brunnr-code/internal/components/persona"
	"github.com/carsimax/brunnr-code/internal/components/reviewassets"
	"github.com/carsimax/brunnr-code/internal/components/skills"
	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/state"
	"github.com/carsimax/brunnr-code/internal/statecoord"
)

type Manager interface {
	PartialUninstall(agentIDs []model.AgentID, componentIDs []model.ComponentID) (Result, error)
	CompleteUninstall() (Result, error)
}

type Snapshotter interface {
	Create(snapshotDir string, paths []string) (backup.Manifest, error)
}

type Result struct {
	Manifest               backup.Manifest
	BackupPath             string
	ChangedFiles           []string
	RemovedFiles           []string
	RemovedDirectories     []string
	ManualActions          []string
	AgentsRemovedFromState []model.AgentID
	// FailedAgents lists the agents whose cleanup did not complete. They are
	// deliberately left in state.json so the recorded state keeps matching the
	// disk, and each one is named in ManualActions with the command that
	// retries it.
	FailedAgents []model.AgentID
}

type Service struct {
	homeDir      string
	workspaceDir string
	backupRoot   string
	appVersion   string
	snapshotter  Snapshotter
	registry     *agents.Registry
	now          func() time.Time

	// engramUninstallScope controls whether Engram cleanup removes global
	// integration files/config (global) or project-local .engram data only.
	engramUninstallScope model.EngramUninstallScope
}

type opType int

const (
	opRewriteFile opType = iota
	opRemoveFile
	opRemoveTree
	opRemoveIfEmpty
)

var (
	allManagedComponents = []model.ComponentID{
		model.ComponentPersona,
		model.ComponentEngram,
		model.ComponentPermission,
		model.ComponentSkills,
	}
	fullAgentRemovalComponents = []model.ComponentID{
		model.ComponentPersona,
		model.ComponentEngram,
		model.ComponentPermission,
		model.ComponentSkills,
	}
)

type operation struct {
	typeID opType
	path   string
	// agents records which agents' cleanup contributed this operation. It is
	// what turns a flat operation list into a per-agent commit boundary: an
	// operation that fails only blocks the agents that own it. An operation
	// nobody owns blocks the whole batch, because nothing distinguishes whose
	// cleanup it was.
	agents []model.AgentID
	apply  func(path string) (changed bool, removed bool, err error)
	// report, when set, adds to the result what an operation changed
	// beyond its own path. It runs after apply, even when apply failed.
	report func(result *Result)
	// notes, when set, names what a successful apply kept for the user to
	// decide on.
	notes func() []string
}

// operationFailure records one operation that did not complete, so the run can
// keep going and still report exactly what was left undone.
type operationFailure struct {
	path   string
	agents []model.AgentID
	err    error
}

func NewService(homeDir, workspaceDir, appVersion string) (*Service, error) {
	registry, err := agents.NewDefaultRegistry()
	if err != nil {
		return nil, fmt.Errorf("create adapter registry: %w", err)
	}

	backupRoot := filepath.Join(homeDir, ".brunnr-code", "backups")
	if err := os.MkdirAll(backupRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create backup root %q: %w", backupRoot, err)
	}

	return &Service{
		homeDir:              homeDir,
		workspaceDir:         workspaceDir,
		backupRoot:           backupRoot,
		appVersion:           appVersion,
		snapshotter:          backup.NewSnapshotter(),
		registry:             registry,
		now:                  time.Now,
		engramUninstallScope: model.EngramUninstallScopeGlobal,
	}, nil
}

func PartialUninstall(homeDir, workspaceDir, appVersion string, agentIDs []string, componentIDs []string) (Result, error) {
	svc, err := NewService(homeDir, workspaceDir, appVersion)
	if err != nil {
		return Result{}, err
	}

	agentsTyped := make([]model.AgentID, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		agentsTyped = append(agentsTyped, model.AgentID(agentID))
	}

	componentsTyped := make([]model.ComponentID, 0, len(componentIDs))
	for _, componentID := range componentIDs {
		componentsTyped = append(componentsTyped, model.ComponentID(componentID))
	}

	return svc.PartialUninstall(agentsTyped, componentsTyped)
}

func PartialUninstallWithEngramScopeSelection(homeDir, workspaceDir, appVersion string, agentIDs []string, componentIDs []string, engramScope model.EngramUninstallScope) (Result, error) {
	svc, err := NewService(homeDir, workspaceDir, appVersion)
	if err != nil {
		return Result{}, err
	}

	agentsTyped := make([]model.AgentID, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		agentsTyped = append(agentsTyped, model.AgentID(agentID))
	}

	componentsTyped := make([]model.ComponentID, 0, len(componentIDs))
	for _, componentID := range componentIDs {
		componentsTyped = append(componentsTyped, model.ComponentID(componentID))
	}

	return svc.PartialUninstallWithEngramScope(agentsTyped, componentsTyped, engramScope)
}

func CompleteUninstall(homeDir, workspaceDir, appVersion string) (Result, error) {
	svc, err := NewService(homeDir, workspaceDir, appVersion)
	if err != nil {
		return Result{}, err
	}
	return svc.CompleteUninstall()
}

func (s *Service) PartialUninstall(agentIDs []model.AgentID, componentIDs []model.ComponentID) (Result, error) {
	s.engramUninstallScope = model.EngramUninstallScopeGlobal

	return s.partialUninstall(agentIDs, componentIDs)
}

func (s *Service) PartialUninstallWithEngramScope(agentIDs []model.AgentID, componentIDs []model.ComponentID, engramScope model.EngramUninstallScope) (Result, error) {
	s.SetEngramUninstallScope(engramScope)
	defer func() {
		s.engramUninstallScope = model.EngramUninstallScopeGlobal
	}()

	return s.partialUninstall(agentIDs, componentIDs)
}

// partialUninstall is the shared body of PartialUninstall and
// PartialUninstallWithEngramScope. It resolves the requested components and
// executes the resulting plan.
func (s *Service) partialUninstall(agentIDs []model.AgentID, componentIDs []model.ComponentID) (Result, error) {
	if len(agentIDs) == 0 {
		return Result{}, fmt.Errorf("partial uninstall requires at least one agent")
	}

	components := componentIDs
	if len(components) == 0 {
		components = slices.Clone(allManagedComponents)
	}

	plan, err := s.buildPlan(agentIDs, components)
	if err != nil {
		return Result{}, err
	}

	stateRemovals := stateAgentsToRemove(agentIDs, components)
	result, err := s.executePlan(plan, stateRemovals)
	return result, err
}

func (s *Service) SetEngramUninstallScope(scope model.EngramUninstallScope) {
	if scope == model.EngramUninstallScopeProject {
		s.engramUninstallScope = model.EngramUninstallScopeProject
		return
	}
	s.engramUninstallScope = model.EngramUninstallScopeGlobal
}

func (s *Service) CompleteUninstall() (Result, error) {
	s.engramUninstallScope = model.EngramUninstallScopeGlobal

	allAgents := s.registry.SupportedAgents()
	plan, err := s.buildPlan(allAgents, allManagedComponents)
	if err != nil {
		return Result{}, err
	}
	result, err := s.executePlan(plan, allAgents)
	if err != nil {
		return result, err
	}

	result.ManualActions = append(result.ManualActions, "To completely remove brunnr-code from your system, delete the executable (e.g., rm -f $(which brunnr-code))")
	return result, nil
}

type plan struct {
	backupTargets []string
	operations    []operation
}

func (s *Service) buildPlan(agentIDs []model.AgentID, componentIDs []model.ComponentID) (plan, error) {
	backupTargets := map[string]struct{}{}
	operationsByKey := map[string]operation{}

	for _, agentID := range agentIDs {
		adapter, ok := s.registry.Get(agentID)
		if !ok {
			return plan{}, fmt.Errorf("unsupported agent %q", agentID)
		}

		for _, componentID := range componentIDs {
			ops, targets, err := s.componentOperations(adapter, componentID)
			if err != nil {
				return plan{}, fmt.Errorf("plan uninstall for %q/%q: %w", agentID, componentID, err)
			}
			for _, target := range targets {
				files, err := expandBackupTarget(target)
				if err != nil {
					return plan{}, fmt.Errorf("expand backup target %q: %w", target, err)
				}
				for _, file := range files {
					backupTargets[file] = struct{}{}
				}
			}
			for _, op := range ops {
				op.agents = []model.AgentID{agentID}
				key := operationKey(op)
				existing, ok := operationsByKey[key]
				if !ok {
					operationsByKey[key] = op
					continue
				}
				if op.typeID == opRewriteFile {
					// Merge rewrite operations on the same file so both
					// mutations apply (e.g. persona + engram on system prompt).
					operationsByKey[key] = mergeRewriteOps(existing, op)
					continue
				}
				// Deduplicated non-rewrite operation: one apply still stands
				// for every agent that asked for it, so ownership widens.
				existing.agents = appendUniqueAgents(existing.agents, op.agents...)
				operationsByKey[key] = existing
			}
		}
	}

	backupTargets[state.Path(s.homeDir)] = struct{}{}
	if removesAllAgentComponents(componentIDs) {
		for _, agentID := range agentIDs {
			adapter, _ := s.registry.Get(agentID)
			var path string
			switch agentID {
			case model.AgentClaudeCode:
				path = adapter.SettingsPath(s.homeDir)
			case model.AgentCodex:
				path = filepath.Join(adapter.GlobalConfigDir(s.homeDir), "hooks.json")
			}
			if path != "" {
				backupTargets[path] = struct{}{}
				op := rewriteSkillRegistryHook(path)
				op.agents = []model.AgentID{agentID}
				key := operationKey(op)
				if existing, ok := operationsByKey[key]; ok {
					op = mergeRewriteOps(existing, op)
				}
				operationsByKey[key] = op
			}
		}
	}
	if slices.Contains(agentIDs, model.AgentClaudeCode) && removesAllAgentComponents(componentIDs) {
		// Only a complete Claude removal retires the module pilot (see
		// retireClaudeGlobalModules); snapshot every file it may change. An
		// unreadable ledger path fails the plan before any write.
		paths, err := agentguidance.ClaudeGlobalModulePaths(s.homeDir)
		if err != nil {
			return plan{}, err
		}
		for _, path := range paths {
			backupTargets[path] = struct{}{}
		}
	}
	orderedTargets := make([]string, 0, len(backupTargets))
	for target := range backupTargets {
		orderedTargets = append(orderedTargets, target)
	}
	slices.Sort(orderedTargets)

	operations := make([]operation, 0, len(operationsByKey))
	for _, op := range operationsByKey {
		operations = append(operations, op)
	}
	slices.SortFunc(operations, compareOperations)

	return plan{backupTargets: orderedTargets, operations: operations}, nil
}

func (s *Service) executePlan(p plan, agentsToRemove []model.AgentID) (Result, error) {
	snapshotDir := filepath.Join(s.backupRoot, s.now().UTC().Format("20060102150405.000000000"))
	manifest, err := s.snapshotter.Create(snapshotDir, p.backupTargets)
	if err != nil {
		return Result{}, fmt.Errorf("create uninstall snapshot: %w", err)
	}

	manifest.Source = backup.BackupSourceUninstall
	manifest.Description = "pre-uninstall snapshot"
	manifest.CreatedByVersion = s.appVersion
	if err := backup.WriteManifest(filepath.Join(snapshotDir, backup.ManifestFilename), manifest); err != nil {
		return Result{}, fmt.Errorf("write uninstall manifest metadata: %w", err)
	}

	result := Result{
		Manifest:   manifest,
		BackupPath: snapshotDir,
	}
	// A batch has no per-agent transaction, so aborting at the first failure
	// left later agents untouched, state.json stale, and no record of how far
	// the run got. Every operation is attempted, failures are attributed to the
	// agents that own them, and only agents with no failed operation commit
	// their state removal.
	failures := make([]operationFailure, 0)

	for _, op := range p.operations {
		changed, removed, err := op.apply(op.path)
		if op.report != nil {
			op.report(&result)
		}
		if err != nil {
			failures = append(failures, operationFailure{path: op.path, agents: op.agents, err: err})
			continue
		}
		if op.notes != nil {
			result.ManualActions = append(result.ManualActions, op.notes()...)
		}
		if op.typeID == opRemoveIfEmpty && !removed {
			if note, ok := manualActionForNonEmptyDirectory(op.path); ok {
				result.ManualActions = append(result.ManualActions, note)
			}
		}
		if !changed {
			continue
		}
		switch op.typeID {
		case opRewriteFile:
			result.ChangedFiles = append(result.ChangedFiles, op.path)
		case opRemoveFile:
			if removed {
				result.RemovedFiles = append(result.RemovedFiles, op.path)
			}
		case opRemoveTree, opRemoveIfEmpty:
			if removed {
				result.RemovedDirectories = append(result.RemovedDirectories, op.path)
			}
		}
	}

	if slices.Contains(agentsToRemove, model.AgentClaudeCode) {
		if err := s.retireClaudeGlobalModules(&result); err != nil {
			failures = append(failures, operationFailure{
				path:   claude.NewAdapter().GlobalConfigDir(s.homeDir),
				agents: []model.AgentID{model.AgentClaudeCode},
				err:    fmt.Errorf("retire Claude orchestrator modules: %w", err),
			})
		}
	}

	result.FailedAgents = failedAgents(failures, agentsToRemove)
	result.ManualActions = append(result.ManualActions, failureManualActions(failures, agentsToRemove, s.homeDir)...)

	committable := make([]model.AgentID, 0, len(agentsToRemove))
	for _, agentID := range agentsToRemove {
		if !slices.Contains(result.FailedAgents, agentID) {
			committable = append(committable, agentID)
		}
	}

	removed, stateErr := updateStateAfterUninstall(s.homeDir, committable)
	result.AgentsRemovedFromState = removed
	result.ManualActions = dedupeSortedStrings(result.ManualActions)

	errs := make([]error, 0, len(failures)+1)
	for _, failure := range failures {
		errs = append(errs, failure.err)
	}
	if stateErr != nil {
		errs = append(errs, stateErr)
	}
	if len(errs) > 0 {
		return result, errors.Join(errs...)
	}
	return result, nil
}

// retireClaudeGlobalModules returns a complete Claude removal from the module
// pilot to the monolithic orchestrator, and does nothing without a module
// ledger. Each known path is reported as it is on disk afterwards: removed or
// changed when retirement touched it, and kept for manual review otherwise.
// Nothing else in the module directory is enumerated.
func (s *Service) retireClaudeGlobalModules(result *Result) error {
	paths, err := agentguidance.ClaudeGlobalModulePaths(s.homeDir)
	if err != nil || paths == nil {
		return err
	}
	retired, err := agentguidance.RetireClaudeGlobalModules(s.homeDir, reviewassets.ReviewExecutionContractFor)
	return reportClaudeModuleRetirement(result, paths, retired, err)
}

// reportClaudeModuleRetirement records in result what retiring the module
// pilot did to paths, the files ClaudeGlobalModulePaths planned. A failed
// retirement that still reports Changed did not finish its rollback, so which
// files it left changed is unknown: none is reported as removed or changed,
// and the user is asked to inspect the core and the module directory.
func reportClaudeModuleRetirement(result *Result, paths []string, retired agentguidance.Result, err error) error {
	if err != nil {
		if retired.Changed {
			result.ManualActions = append(result.ManualActions, fmt.Sprintf(
				"Inspect %s and %s before rerunning the uninstall: the failed Claude orchestrator module retirement did not finish its rollback, so they may be partially changed",
				paths[0], filepath.Dir(paths[len(paths)-1])))
		}
		return err
	}
	for _, path := range paths {
		_, statErr := os.Lstat(path)
		switch {
		case slices.Contains(retired.Files, path) && os.IsNotExist(statErr):
			result.RemovedFiles = append(result.RemovedFiles, path)
		case slices.Contains(retired.Files, path):
			if !slices.Contains(result.ChangedFiles, path) {
				result.ChangedFiles = append(result.ChangedFiles, path)
			}
		case statErr == nil:
			result.ManualActions = append(result.ManualActions, fmt.Sprintf(
				"Remove manually if no longer needed: %s (orchestrator module modified or not owned by Brunnr-Code, kept)", path))
		}
	}
	return nil
}

// failedAgents reports which agents cannot claim a completed uninstall. A
// failure that no agent owns blocks every agent in the batch: nothing
// distinguishes whose cleanup it belonged to, so none of them may be recorded
// as removed.
func failedAgents(failures []operationFailure, batch []model.AgentID) []model.AgentID {
	if len(failures) == 0 {
		return nil
	}
	blocked := make([]model.AgentID, 0, len(batch))
	for _, failure := range failures {
		if len(failure.agents) == 0 {
			blocked = appendUniqueAgents(blocked, batch...)
			continue
		}
		blocked = appendUniqueAgents(blocked, failure.agents...)
	}
	if len(blocked) == 0 {
		return nil
	}

	// Report in the order the caller named the agents so the summary reads in
	// the same order as the invocation, with any agent outside the batch last.
	ordered := make([]model.AgentID, 0, len(blocked))
	for _, agentID := range batch {
		if slices.Contains(blocked, agentID) {
			ordered = append(ordered, agentID)
		}
	}
	return appendUniqueAgents(ordered, blocked...)
}

func failureManualActions(failures []operationFailure, batch []model.AgentID, homeDir string) []string {
	actions := make([]string, 0, len(failures))
	for _, failure := range failures {
		owners := failure.agents
		if len(owners) == 0 {
			owners = batch
		}
		labels := make([]string, 0, len(owners))
		retry := make([]string, 0, len(owners))
		for _, agentID := range owners {
			labels = append(labels, string(agentID))
			retry = append(retry, "--agent "+string(agentID))
		}
		scope := strings.Join(labels, ", ")
		if scope == "" {
			scope = "this uninstall"
		}
		location := failure.path
		if location == "" {
			location = homeDir
		}
		command := "brunnr-code uninstall --all --yes"
		if len(retry) > 0 {
			command = "brunnr-code uninstall " + strings.Join(retry, " ") + " --yes"
		}
		actions = append(actions, fmt.Sprintf(
			"Uninstall did not complete for %s at %s: %v. Those agents are still recorded in %s. Resolve the file, then rerun `%s`.",
			scope, location, failure.err, state.Path(homeDir), command))
	}
	return actions
}

func appendUniqueAgents(existing []model.AgentID, additions ...model.AgentID) []model.AgentID {
	for _, agentID := range additions {
		if !slices.Contains(existing, agentID) {
			existing = append(existing, agentID)
		}
	}
	return existing
}

func manualActionForNonEmptyDirectory(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", false
	}
	if len(entries) == 0 {
		return "", false
	}
	return fmt.Sprintf("Remove manually if no longer needed: %s (directory still contains non-managed files)", path), true
}

func dedupeSortedStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	cloned := slices.Clone(items)
	slices.Sort(cloned)
	return slices.Compact(cloned)
}

// settingsTargets returns the JSON settings files generic cleaners may rewrite.
func settingsTargets(homeDir string, adapter agents.Adapter) []string {
	path := agents.JSONSettingsPath(homeDir, adapter)
	if path == "" {
		return nil
	}
	return []string{path}
}

func (s *Service) componentOperations(adapter agents.Adapter, componentID model.ComponentID) ([]operation, []string, error) {
	ops := make([]operation, 0)
	targets := make([]string, 0)
	homeDir := s.homeDir

	switch componentID {
	case model.ComponentPersona:
		if adapter.SupportsSystemPrompt() {
			path := adapter.SystemPromptFile(homeDir)
			targets = append(targets, path)
			ops = append(ops, rewriteMarkdownFile(path, func(content string) (string, bool) {
				return removeMarkdownSections(content, "persona")
			}))
		}
		if adapter.SupportsOutputStyles() {
			styleDir := adapter.OutputStyleDir(homeDir)
			for _, path := range append([]string{persona.OutputStylePath(styleDir)}, persona.LegacyOutputStylePaths(styleDir)...) {
				targets = append(targets, path)
				ops = append(ops, removeFile(path))
			}
			ops = append(ops, removeDirIfEmpty(adapter.OutputStyleDir(homeDir)))
		}
		for _, path := range settingsTargets(homeDir, adapter) {
			targets = append(targets, path)
			jsonPaths := []jsonPath{{"outputStyle"}}
			ops = append(ops, rewriteJSONFile(path, jsonPaths...))
		}
	case model.ComponentEngram:
		if s.engramUninstallScope == model.EngramUninstallScopeProject {
			projectDataPath := filepath.Join(s.workspaceDir, ".engram")
			if strings.TrimSpace(s.workspaceDir) != "" {
				targets = append(targets, projectDataPath)
				ops = append(ops, removeTree(projectDataPath))
			}
			break
		}

		targets = append(targets, engramTargets(adapter, homeDir)...)
		ops = append(ops, engramOperations(adapter, homeDir)...)
		if adapter.SupportsSystemPrompt() {
			path := adapter.SystemPromptFile(homeDir)
			targets = append(targets, path)
			ops = append(ops, rewriteMarkdownFile(path, func(content string) (string, bool) {
				return removeMarkdownSections(content, "engram-protocol")
			}))
		}
	case model.ComponentPermission:
		for _, path := range settingsTargets(homeDir, adapter) {
			targets = append(targets, path)
			switch adapter.Agent() {
			case model.AgentClaudeCode:
				ops = append(ops, rewriteJSONFile(path, jsonPath{"permissions"}))
			}
		}
	case model.ComponentSkills:
		if !adapter.SupportsSkills() {
			break
		}
		skillDir := adapter.SkillsDir(homeDir)
		if skillDir == "" {
			break
		}
		entries, err := fs.ReadDir(assets.FS, "skills")
		if err != nil {
			return nil, nil, fmt.Errorf("read embedded skills: %w", err)
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == "_shared" {
				continue
			}
			dirPath := filepath.Join(skillDir, entry.Name())
			targets = append(targets, dirPath)
			ops = append(ops, removeTree(dirPath), removeDirIfEmpty(skillDir))
		}
		// _shared may hold user-authored notes, so only the embedded references
		// the skills component writes are removed, never the whole directory.
		shared, err := skills.SharedReferencePaths(skillDir)
		if err != nil {
			return nil, nil, err
		}
		for _, path := range shared {
			targets = append(targets, path)
			ops = append(ops, removeFile(path))
		}
		// Upgraded installs may still hold the obsolete generated marker; install
		// only ever removes it as a regular file, so uninstall does the same.
		marker := skills.LegacySharedMarkerPath(skillDir)
		targets = append(targets, marker)
		ops = append(ops, removeRegularFile(marker))
		ops = append(ops, removeDirIfEmpty(filepath.Join(skillDir, "_shared")))
	default:
		return nil, nil, fmt.Errorf("unsupported component ID %q", componentID)
	}

	return ops, targets, nil
}

func engramTargets(adapter agents.Adapter, homeDir string) []string {
	targets := make([]string, 0, 3)
	switch adapter.MCPStrategy() {
	case model.StrategySeparateMCPFiles:
		if adapter.Agent() == model.AgentClaudeCode {
			return []string{claude.UserConfigPath(homeDir), adapter.MCPConfigPath(homeDir, "engram")}
		}
		targets = append(targets, adapter.MCPConfigPath(homeDir, "engram"))
	case model.StrategyTOMLFile:
		targets = append(targets,
			adapter.MCPConfigPath(homeDir, "engram"),
			filepath.Join(homeDir, ".codex", "engram-instructions.md"),
			filepath.Join(homeDir, ".codex", "engram-compact-prompt.md"),
		)
	}
	return targets
}

func engramOperations(adapter agents.Adapter, homeDir string) []operation {
	switch adapter.MCPStrategy() {
	case model.StrategySeparateMCPFiles:
		path := adapter.MCPConfigPath(homeDir, "engram")
		if adapter.Agent() == model.AgentClaudeCode {
			return []operation{rewriteClaudeUserConfig(homeDir, jsonPath{"mcpServers", "engram"}), removeManagedEngramFile(path)}
		}
		return []operation{removeFile(path), removeDirIfEmpty(filepath.Dir(path))}
	case model.StrategyTOMLFile:
		configPath := adapter.MCPConfigPath(homeDir, "engram")
		instructionsPath := filepath.Join(homeDir, ".codex", "engram-instructions.md")
		compactPath := filepath.Join(homeDir, ".codex", "engram-compact-prompt.md")
		return []operation{
			rewriteTOMLFile(configPath, cleanCodexTOML),
			removeFile(instructionsPath),
			removeFile(compactPath),
			removeDirIfEmpty(filepath.Dir(instructionsPath)),
		}
	default:
		return nil
	}
}

func rewriteMarkdownFile(path string, mutate func(content string) (string, bool)) operation {
	return operation{
		typeID: opRewriteFile,
		path:   path,
		apply: func(path string) (bool, bool, error) {
			content, err := readFileOrEmpty(path)
			if err != nil {
				return false, false, err
			}
			eol := detectEOL(content)
			updated, changed := mutate(content)
			if !changed {
				return false, false, nil
			}
			updated = restoreEOL(updated, eol)
			if strings.TrimSpace(updated) == "" {
				if err := removeFileIfExists(path); err != nil {
					return false, false, err
				}
				return true, true, nil
			}
			_, err = filemerge.WriteFileAtomic(path, []byte(updated), 0o644)
			if err != nil {
				return false, false, err
			}
			return true, false, nil
		},
	}
}

func rewriteJSONFile(path string, jsonPaths ...jsonPath) operation {
	return operation{
		typeID: opRewriteFile,
		path:   path,
		apply: func(path string) (bool, bool, error) {
			raw, err := readManagedFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					return false, false, nil
				}
				return false, false, fmt.Errorf("read json file %q: %w", path, err)
			}
			updated, changed, err := removeJSONPaths(raw, jsonPaths...)
			if err != nil {
				return false, false, fmt.Errorf("clean json file %q: %w", path, err)
			}
			if !changed {
				return false, false, nil
			}
			if jsonIsEmptyObject(updated) {
				if err := removeFileIfExists(path); err != nil {
					return false, false, err
				}
				return true, true, nil
			}
			// Preserve the file's existing mode: an uninstall rewrite must
			// not widen permissions on a file the user or agent restricted.
			perm := os.FileMode(0o644)
			if info, statErr := os.Lstat(path); statErr == nil {
				perm = info.Mode().Perm()
			}
			_, err = filemerge.WriteFileAtomic(path, updated, perm)
			if err != nil {
				return false, false, err
			}
			return true, false, nil
		},
	}
}

// rewriteClaudeUserConfig removes managed entries from ~/.claude.json. Unlike
// rewriteJSONFile it never deletes the file when the result is empty: the
// registry belongs to Claude Code, so uninstall only ever writes the emptied
// object back with the file's mode preserved.
func rewriteClaudeUserConfig(homeDir string, jsonPaths ...jsonPath) operation {
	path := claude.UserConfigPath(homeDir)
	return operation{
		typeID: opRewriteFile,
		path:   path,
		apply: func(path string) (bool, bool, error) {
			raw, err := readManagedFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					return false, false, nil
				}
				return false, false, fmt.Errorf("read json file %q: %w", path, err)
			}
			updated, changed, err := removeJSONPaths(raw, jsonPaths...)
			if err != nil {
				return false, false, fmt.Errorf("clean json file %q: %w", path, err)
			}
			if !changed {
				return false, false, nil
			}
			perm := os.FileMode(0o600)
			if info, statErr := os.Lstat(path); statErr == nil {
				perm = info.Mode().Perm()
			}
			if _, err := filemerge.WriteFileAtomic(path, updated, perm); err != nil {
				return false, false, err
			}
			return true, false, nil
		},
	}
}

func rewriteSkillRegistryHook(path string) operation {
	return operation{
		typeID: opRewriteFile,
		path:   path,
		apply: func(path string) (bool, bool, error) {
			raw, err := readManagedFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					return false, false, nil
				}
				return false, false, fmt.Errorf("read skill-registry hook config %q: %w", path, err)
			}
			updated, changed, err := removeSkillRegistryHook(raw)
			if err != nil {
				return false, false, fmt.Errorf("clean skill-registry hook %q: %w", path, err)
			}
			if !changed {
				return false, false, nil
			}
			// An emptied object that still holds JSONC comments is user text.
			if jsonIsEmptyObject(updated) && json.Valid(updated) {
				if err := removeFileIfExists(path); err != nil {
					return false, false, err
				}
				return true, true, nil
			}
			_, err = filemerge.WriteFileAtomic(path, updated, 0o644)
			if err != nil {
				return false, false, err
			}
			return true, false, nil
		},
	}
}

// Only exact installed commands are removed; arbitrary user commands remain intact.
func managedRetainedHookCommand(cmd string) bool {
	return cmd == `brunnr-code skill-registry refresh --quiet --no-gitignore --cwd "${CLAUDE_PROJECT_DIR:-$PWD}" || true` ||
		cmd == `brunnr-code skill-registry refresh --quiet --no-gitignore --cwd "$PWD" || true` ||
		cmd == "brunnr-code review stop-hook --agent "+string(model.AgentClaudeCode)
}

// removeSkillRegistryHook removes the managed hook commands. Strict JSON is
// re-encoded; JSONC keeps every byte outside the hooks value and refuses,
// rather than normalizes, hooks spelled with escapes or holding comments.
func removeSkillRegistryHook(raw []byte) ([]byte, bool, error) {
	strict := json.Valid(raw)
	root := map[string]any{}
	if strict {
		if err := json.Unmarshal(raw, &root); err != nil {
			return nil, false, err
		}
	} else {
		decoded, err := filemerge.UnmarshalJSONObject(raw)
		if err != nil {
			return nil, false, err
		}
		root = decoded
	}
	hooksMap, ok := root["hooks"].(map[string]any)
	if !ok {
		return raw, false, nil
	}
	events := make([]string, 0, len(hooksMap))
	for event := range hooksMap {
		events = append(events, event)
	}
	changed := false
	for _, hookKey := range []string{"UserPromptSubmit", "SessionStart", "Stop", "SubagentStop", "PreToolUse", "PostToolUse", "SessionEnd"} {
		entries, ok := hooksMap[hookKey].([]any)
		if !ok {
			continue
		}
		keptEntries := make([]any, 0, len(entries))
		for _, entry := range entries {
			entryMap, ok := entry.(map[string]any)
			if !ok {
				keptEntries = append(keptEntries, entry)
				continue
			}
			hooks, ok := entryMap["hooks"].([]any)
			if !ok {
				keptEntries = append(keptEntries, entry)
				continue
			}
			keptHooks := make([]any, 0, len(hooks))
			for _, hook := range hooks {
				hookMap, ok := hook.(map[string]any)
				cmd, _ := hookMap["command"].(string)
				if ok && managedRetainedHookCommand(cmd) {
					changed = true
					continue
				}
				keptHooks = append(keptHooks, hook)
			}
			if len(keptHooks) == 0 {
				changed = true
				continue
			}
			entryMap["hooks"] = keptHooks
			keptEntries = append(keptEntries, entryMap)
		}
		if len(keptEntries) == 0 {
			delete(hooksMap, hookKey)
		} else {
			hooksMap[hookKey] = keptEntries
		}
	}
	if !changed {
		return raw, false, nil
	}
	if !strict && len(hooksMap) == 0 {
		updated, kept, err := filemerge.RemoveJSONCMembers(raw, []string{"hooks"}, events, true)
		if err != nil {
			return nil, false, err
		}
		if len(kept) > 0 {
			return nil, false, fmt.Errorf("refuse to remove hooks %v spelled with escapes or holding comments; remove the Brunnr-Code hooks yourself", kept)
		}
		return updated, true, nil
	}
	if !strict {
		overlay, err := json.Marshal(map[string]any{"hooks": map[string]any{"__replace__": hooksMap}})
		if err != nil {
			return nil, false, err
		}
		updated, err := filemerge.MergeJSONCObjects(raw, overlay)
		if err != nil {
			return nil, false, err
		}
		return updated, true, nil
	}
	if len(hooksMap) == 0 {
		delete(root, "hooks")
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return append(out, '\n'), true, nil
}

func rewriteTOMLFile(path string, mutate func(content string) (string, bool)) operation {
	return operation{
		typeID: opRewriteFile,
		path:   path,
		apply: func(path string) (bool, bool, error) {
			content, err := readFileOrEmpty(path)
			if err != nil {
				return false, false, err
			}
			eol := detectEOL(content)
			updated, changed := mutate(content)
			if !changed {
				return false, false, nil
			}
			updated = restoreEOL(updated, eol)
			if strings.TrimSpace(updated) == "" {
				if err := removeFileIfExists(path); err != nil {
					return false, false, err
				}
				return true, true, nil
			}
			_, err = filemerge.WriteFileAtomic(path, []byte(updated), 0o644)
			if err != nil {
				return false, false, err
			}
			return true, false, nil
		},
	}
}

func removeManagedEngramFile(path string) operation {
	return operation{
		typeID: opRemoveFile,
		path:   path,
		apply: func(path string) (bool, bool, error) {
			removed, err := engram.RemoveManagedLegacyClaudeConfig(path)
			return removed, removed, err
		},
	}
}

func removeFile(path string) operation {
	return operation{
		typeID: opRemoveFile,
		path:   path,
		apply: func(path string) (bool, bool, error) {
			_, statErr := os.Stat(path)
			if statErr != nil {
				if os.IsNotExist(statErr) {
					return false, false, nil
				}
				return false, false, statErr
			}
			if err := removeFileIfExists(path); err != nil {
				return false, false, err
			}
			return true, true, nil
		},
	}
}

// removeRegularFile removes path only when it is a regular file, leaving
// symlinks, directories, and other non-regular entries untouched.
func removeRegularFile(path string) operation {
	return operation{
		typeID: opRemoveFile,
		path:   path,
		apply: func(path string) (bool, bool, error) {
			info, err := os.Lstat(path)
			if os.IsNotExist(err) {
				return false, false, nil
			}
			if err != nil {
				return false, false, err
			}
			if !info.Mode().IsRegular() {
				return false, false, nil
			}
			if err := removeFileIfExists(path); err != nil {
				return false, false, err
			}
			return true, true, nil
		},
	}
}

func removeTree(path string) operation {
	return operation{
		typeID: opRemoveTree,
		path:   path,
		apply: func(path string) (bool, bool, error) {
			if _, err := os.Stat(path); err != nil {
				if os.IsNotExist(err) {
					return false, false, nil
				}
				return false, false, err
			}
			if err := os.RemoveAll(path); err != nil {
				return false, false, fmt.Errorf("remove directory tree %q: %w", path, err)
			}
			return true, true, nil
		},
	}
}

func removeDirIfEmpty(path string) operation {
	return operation{
		typeID: opRemoveIfEmpty,
		path:   path,
		apply: func(path string) (bool, bool, error) {
			if path == "" {
				return false, false, nil
			}
			removed, err := removeDirIfEmptyRecursive(path)
			return removed, removed, err
		},
	}
}

func removeDirIfEmptyRecursive(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if len(entries) != 0 {
		return false, nil
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("remove empty directory %q: %w", path, err)
	}
	return true, nil
}

func readFileOrEmpty(path string) (string, error) {
	data, err := readManagedFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read file %q: %w", path, err)
	}
	return string(data), nil
}

func removeFileIfExists(path string) error {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove file %q: %w", path, err)
	}
	return nil
}

func expandBackupTarget(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{path}, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return []string{path}, nil
	}

	files := make([]string, 0)
	err = filepath.WalkDir(path, func(current string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		files = append(files, current)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		// Directory exists but contains no files; return no backup targets.
		// The snapshotter expects file paths, not empty directories.
		return []string{}, nil
	}
	return files, nil
}

func operationKey(op operation) string {
	return fmt.Sprintf("%d:%s", op.typeID, op.path)
}

// mergeRewriteOps composes two rewrite operations on the same file path.
// Both apply functions run sequentially: 'a' writes first, then 'b' reads
// the updated file from disk and applies its own mutation. This works because
// rewriteMarkdownFile and rewriteJSONFile always read fresh from disk.
func mergeRewriteOps(a, b operation) operation {
	return operation{
		typeID: opRewriteFile,
		path:   a.path,
		agents: appendUniqueAgents(slices.Clone(a.agents), b.agents...),
		apply: func(path string) (bool, bool, error) {
			changed1, removed1, err1 := a.apply(path)
			if err1 != nil {
				return changed1, removed1, err1
			}
			// If the first op removed the file entirely, the second op
			// has nothing left to rewrite.
			if removed1 {
				return changed1, removed1, nil
			}
			changed2, removed2, err2 := b.apply(path)
			return changed1 || changed2, removed2, err2
		},
		notes: func() []string {
			var notes []string
			for _, op := range []operation{a, b} {
				if op.notes != nil {
					notes = append(notes, op.notes()...)
				}
			}
			return notes
		},
	}
}

func compareOperations(a, b operation) int {
	if a.typeID != b.typeID {
		return int(a.typeID) - int(b.typeID)
	}
	if a.typeID == opRemoveIfEmpty {
		// Deepest path first: a child directory such as skills/_shared must be
		// evaluated before its parent, or the parent is still non-empty when
		// checked and survives an uninstall that emptied it.
		return strings.Compare(b.path, a.path)
	}
	return strings.Compare(a.path, b.path)
}

func removesAllAgentComponents(componentIDs []model.ComponentID) bool {
	if len(componentIDs) == 0 {
		return true
	}
	for _, componentID := range fullAgentRemovalComponents {
		if !slices.Contains(componentIDs, componentID) {
			return false
		}
	}
	return true
}

func stateAgentsToRemove(agentIDs []model.AgentID, componentIDs []model.ComponentID) []model.AgentID {
	selected := make(map[model.ComponentID]struct{}, len(componentIDs))
	for _, componentID := range componentIDs {
		selected[componentID] = struct{}{}
	}
	for _, required := range fullAgentRemovalComponents {
		if _, ok := selected[required]; !ok {
			return nil
		}
	}
	return slices.Clone(agentIDs)
}

func updateStateAfterUninstall(homeDir string, toRemove []model.AgentID) ([]model.AgentID, error) {
	if len(toRemove) == 0 {
		return nil, nil
	}

	// removed must be declared outside the closure so it survives the lock;
	// it is only assigned once the corresponding write has succeeded.
	var removed []model.AgentID
	err := statecoord.WithLock(homeDir, func() error {
		// Re-read the latest state inside the lock so a concurrent writer's
		// change is not clobbered by a stale snapshot taken before the lock.
		current, err := state.Read(homeDir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("read install state: %w", err)
		}

		removeSet := make(map[string]struct{}, len(toRemove))
		for _, agentID := range toRemove {
			removeSet[string(agentID)] = struct{}{}
		}

		kept := make([]string, 0, len(current.InstalledAgents))
		removedHere := make([]model.AgentID, 0, len(toRemove))
		for _, installed := range current.InstalledAgents {
			if _, ok := removeSet[installed]; ok {
				removedHere = append(removedHere, model.AgentID(installed))
				continue
			}
			kept = append(kept, installed)
		}
		if len(removedHere) == 0 {
			return nil
		}

		updated := current
		updated.InstalledAgents = kept
		if err := state.Write(homeDir, updated); err != nil {
			return fmt.Errorf("write install state: %w", err)
		}
		removed = removedHere
		return nil
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}
