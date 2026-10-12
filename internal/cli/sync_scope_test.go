package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/backup"
	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
	"github.com/gentleman-programming/gentle-ai/v4/internal/verify"
)

// ─── Parser: sync --scope ────────────────────────────────────────────────────

func TestParseSyncFlagsScope(t *testing.T) {
	t.Parallel()
	flags, err := ParseSyncFlags([]string{"--scope", "workspace"})
	if err != nil {
		t.Fatalf("ParseSyncFlags(--scope workspace) error = %v", err)
	}
	if flags.Scope != "workspace" {
		t.Errorf("Scope = %q, want %q", flags.Scope, "workspace")
	}

	flags, err = ParseSyncFlags([]string{"--scope=global"})
	if err != nil {
		t.Fatalf("ParseSyncFlags(--scope=global) error = %v", err)
	}
	if flags.Scope != "global" {
		t.Errorf("Scope = %q, want %q", flags.Scope, "global")
	}

	flags, err = ParseSyncFlags(nil)
	if err != nil {
		t.Fatalf("ParseSyncFlags(nil) error = %v", err)
	}
	if flags.Scope != "" {
		t.Errorf("default Scope = %q, want empty (resolved to global later)", flags.Scope)
	}
}

func TestRunSyncRejectsUnsupportedScope(t *testing.T) {
	_, err := RunSync([]string{"--scope", "repo", "--agents", "claude-code"})
	if err == nil {
		t.Fatal("RunSync(--scope repo) error = nil, want unsupported scope refusal")
	}
	if !strings.Contains(err.Error(), "unsupported scope") {
		t.Fatalf("RunSync(--scope repo) error = %v, want it to name the unsupported scope", err)
	}
}

// ─── Workspace scope regression (issue #1074) ────────────────────────────────

// snapshotTree records every file below root as "permissions:sha256" (or its
// symlink target) and every directory as "dir:permissions", keyed by
// root-relative path, so a before/after comparison detects byte, permission,
// creation, and deletion differences for files AND directories.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			info, statErr := d.Info()
			if statErr != nil {
				return statErr
			}
			snapshot[rel] = fmt.Sprintf("dir:%o", info.Mode().Perm())
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			target, linkErr := os.Readlink(path)
			if linkErr != nil {
				return linkErr
			}
			snapshot[rel] = "symlink:" + target
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return statErr
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		sum := sha256.Sum256(data)
		snapshot[rel] = fmt.Sprintf("%o:%s", info.Mode().Perm(), hex.EncodeToString(sum[:]))
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot tree %q: %v", root, err)
	}
	return snapshot
}

// TestSyncWorkspaceScopeDeclaresAndVerifiesPersonaWorkspaceTarget pins the
// persona half of the #1074 ownership contract: a workspace sync must declare
// the workspace persona prompt in the backup/rollback contract and verify it
// after the run, and it must never declare or mutate the global persona file
// (or create anything else in the global tree, directories included).
func TestSyncWorkspaceScopeDeclaresAndVerifiesPersonaWorkspaceTarget(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()

	restoreBackupHome := backup.UserHomeDirFn
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	t.Cleanup(func() {
		backup.UserHomeDirFn = restoreBackupHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
	})
	backup.UserHomeDirFn = func() (string, error) { return home, nil }
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = func(name string) (string, error) { return "/usr/local/bin/" + name, nil }

	selection := model.Selection{
		Agents:     []model.AgentID{model.AgentClaudeCode},
		Components: []model.ComponentID{model.ComponentPersona},
		Persona:    model.PersonaNeutral,
	}
	adapters := resolveAdapters(selection.Agents)
	adapter := adapters[0]
	globalPrompt := adapter.SystemPromptFile(home)
	workspacePrompt := adapter.SystemPromptFile(workspace)

	// Rollback contract: the workspace persona prompt is snapshotted so a
	// failed workspace persona write can be restored; the global one is not
	// part of a workspace sync's transaction.
	declared, err := syncBackupTargetsScoped(home, workspace, ScopeWorkspace, selection, adapters)
	if err != nil {
		t.Fatalf("syncBackupTargetsScoped() error = %v", err)
	}
	if !containsString(declared, workspacePrompt) {
		t.Errorf("workspace persona prompt %q missing from rollback contract; declared = %v", workspacePrompt, declared)
	}
	if containsString(declared, globalPrompt) {
		t.Errorf("global persona prompt %q declared under workspace scope", globalPrompt)
	}

	// Verification contract: the workspace persona prompt is a post-sync
	// verification target; the global one never is.
	verification := runPostSyncVerificationScoped(home, workspace, ScopeWorkspace, selection)
	verifiedDeclaration := false
	for _, check := range verification.Checks {
		if check.ID == "verify:sync:file:"+workspacePrompt {
			verifiedDeclaration = true
		}
		if check.ID == "verify:sync:file:"+globalPrompt {
			t.Errorf("verification targets the global persona prompt %q under workspace scope", globalPrompt)
		}
	}
	if !verifiedDeclaration {
		t.Errorf("workspace persona prompt %q missing from verification targets; checks = %v", workspacePrompt, verification.Checks)
	}

	// End to end: the workspace persona write lands, is verified, and the
	// global tree — files and directories — stays byte-identical.
	userPrompt := "user claude instructions\n"
	mustWriteFile(t, globalPrompt, []byte(userPrompt))
	before := snapshotTree(t, home)

	t.Chdir(workspace)
	result, err := RunSyncWithSelectionScope(home, selection, ScopeWorkspace)
	if err != nil {
		t.Fatalf("RunSyncWithSelectionScope() error = %v", err)
	}
	if result.FilesChanged == 0 {
		t.Errorf("workspace persona sync changed nothing: changedFiles = %v", result.ChangedFiles)
	}
	workspaceVerified := false
	for _, check := range result.Verify.Checks {
		if check.ID == "verify:sync:file:"+workspacePrompt {
			workspaceVerified = true
		}
	}
	if !workspaceVerified {
		t.Errorf("workspace persona prompt %q not verified post-sync; checks = %v", workspacePrompt, result.Verify.Checks)
	}
	if !result.Verify.Ready {
		t.Errorf("workspace persona sync verification not ready: %s", verify.RenderReport(result.Verify))
	}
	got, err := os.ReadFile(globalPrompt)
	if err != nil {
		t.Fatalf("ReadFile(global prompt) error = %v", err)
	}
	if string(got) != userPrompt {
		t.Errorf("global persona prompt mutated by workspace sync: %q", got)
	}
	after := snapshotTree(t, home)
	for rel, beforeHash := range before {
		if after[rel] != beforeHash {
			t.Errorf("global tree entry mutated by workspace sync: %q", rel)
		}
	}
	for rel := range after {
		if _, ok := before[rel]; !ok {
			t.Errorf("global tree gained an entry during workspace sync: %q", rel)
		}
	}
}

// TestRunSyncWorkspaceScopeUpdatesClaudeEngramWithoutGlobalMutation closes the
// Claude Code half of the #1074 ownership contract. Install's scoped engram
// delivery is InjectWorkspaceWithOptions (userScope=false): it writes the
// adapter's workspace MCP config — Claude Code: <workspace>/.claude/mcp/
// engram.json — while the user-scope API would write <workspace>/.claude.json.
// The workspace sync must dispatch to the scoped API, declare the workspace
// file in the backup/rollback contract, verify it post-sync, and leave the
// global tree byte-identical. Context7 is included to pin the already-correct
// workspace .mcp.json target alongside it.
func TestRunSyncWorkspaceScopeUpdatesClaudeEngramWithoutGlobalMutation(t *testing.T) {
	// The global baseline must stay global even if the environment selects a
	// different install scope.
	t.Setenv(scopeEnvVar, "")
	home := t.TempDir()
	globalWorkspace := t.TempDir()
	workspace := t.TempDir()

	restoreHome := osUserHomeDir
	restoreBackupHome := backup.UserHomeDirFn
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	t.Cleanup(func() {
		osUserHomeDir = restoreHome
		backup.UserHomeDirFn = restoreBackupHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
	})

	osUserHomeDir = func() (string, error) { return home, nil }
	backup.UserHomeDirFn = func() (string, error) { return home, nil }
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = func(name string) (string, error) { return "/usr/local/bin/" + name, nil }

	// Phase 1: establish the global baseline with a default (global) sync.
	t.Chdir(globalWorkspace)
	if _, err := RunSync([]string{"--agents", "claude-code"}); err != nil {
		t.Fatalf("global sync error = %v", err)
	}
	before := snapshotTree(t, home)

	// Phase 2: workspace sync of Engram + Context7 for Claude Code.
	selection := model.Selection{
		Agents:     []model.AgentID{model.AgentClaudeCode},
		Components: []model.ComponentID{model.ComponentEngram, model.ComponentContext7},
		Persona:    model.PersonaNeutral,
	}
	t.Chdir(workspace)
	result, err := RunSyncWithSelectionScope(home, selection, ScopeWorkspace)
	if err != nil {
		t.Fatalf("workspace Claude engram sync error = %v", err)
	}
	if result.FilesChanged == 0 {
		t.Fatalf("workspace Claude engram sync changed nothing: changedFiles = %v", result.ChangedFiles)
	}
	for _, path := range result.ChangedFiles {
		if !pathUnder(path, workspace) {
			t.Errorf("workspace Claude engram sync reported a change outside the workspace: %q", path)
		}
	}

	workspaceEngram := filepath.Join(workspace, ".claude", "mcp", "engram.json")
	workspaceContext7 := filepath.Join(workspace, ".mcp.json")
	for _, target := range []string{workspaceEngram, workspaceContext7} {
		if _, err := os.Stat(target); err != nil {
			t.Errorf("workspace Claude sync did not materialize %q: %v", target, err)
		}
	}

	// Rollback contract: the workspace engram MCP file is snapshotted; the
	// global one is not part of a workspace sync's transaction.
	declared, err := syncBackupTargetsScoped(home, workspace, ScopeWorkspace, selection, resolveAdapters(selection.Agents))
	if err != nil {
		t.Fatalf("syncBackupTargetsScoped() error = %v", err)
	}
	if !containsString(declared, workspaceEngram) {
		t.Errorf("workspace engram MCP file %q missing from rollback contract; declared = %v", workspaceEngram, declared)
	}
	if containsString(declared, filepath.Join(home, ".claude", "mcp", "engram.json")) {
		t.Errorf("global engram MCP file declared under workspace scope")
	}

	// Verification: both workspace MCP targets are verified and the report is ready.
	engramVerified := false
	for _, check := range result.Verify.Checks {
		if check.ID == "verify:sync:file:"+workspaceEngram {
			engramVerified = true
		}
	}
	if !engramVerified {
		t.Errorf("workspace engram MCP file %q missing from verification targets; checks = %v", workspaceEngram, result.Verify.Checks)
	}
	if !result.Verify.Ready {
		t.Errorf("workspace Claude engram sync verification not ready: %s", verify.RenderReport(result.Verify))
	}

	// The global tree — files and directories — must stay byte-identical.
	after := snapshotTree(t, home)
	for rel, beforeHash := range before {
		if after[rel] != beforeHash {
			t.Errorf("global tree entry mutated by workspace Claude engram sync: %q", rel)
		}
	}
	for rel := range after {
		if _, ok := before[rel]; !ok {
			t.Errorf("global tree gained an entry during workspace Claude engram sync: %q", rel)
		}
	}
}

// ─── PR #5080 follow-up: scope validation, home-only rollback filtering,
// ─── legacy plugin verification, and the project settings authority.

func TestRunSyncWithSelectionScopeRejectsInvalidScope(t *testing.T) {
	home := t.TempDir()
	_, err := RunSyncWithSelectionScope(home, model.Selection{Agents: []model.AgentID{model.AgentCodex}}, "repo")
	if err == nil {
		t.Fatal("RunSyncWithSelectionScope(scope \"repo\") error = nil, want unsupported scope refusal")
	}
	if !strings.Contains(err.Error(), "unsupported scope") {
		t.Fatalf("RunSyncWithSelectionScope(scope \"repo\") error = %v, want it to name the unsupported scope", err)
	}
}

// TestSyncWorkspaceBackupTargetsExcludeHomeOnlyPaths pins the rollback
// contract for issue #1074: a workspace sync must never declare a home-only
// path — not from component declarations, not from cleanup declarations — so
// a failed workspace sync can never roll back (restore) global files.
func TestSyncWorkspaceBackupTargetsExcludeHomeOnlyPaths(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	selection := model.Selection{
		Agents:     []model.AgentID{model.AgentClaudeCode},
		Components: []model.ComponentID{model.ComponentEngram, model.ComponentContext7, model.ComponentPermission, model.ComponentPersona},
		Persona:    model.PersonaNeutral,
	}
	declared, err := syncBackupTargetsScoped(home, workspace, ScopeWorkspace, selection, resolveAdapters(selection.Agents))
	if err != nil {
		t.Fatalf("syncBackupTargetsScoped() error = %v", err)
	}
	for _, path := range declared {
		if pathUnderHomeOnly(path, home, workspace) {
			t.Errorf("workspace rollback contract declares a home-only path: %q", path)
		}
	}
}
