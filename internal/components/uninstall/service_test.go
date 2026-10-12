package uninstall

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/carsimax/brunnr-code/internal/agents/claude"
	"github.com/carsimax/brunnr-code/internal/agents/codex"
	"github.com/carsimax/brunnr-code/internal/backup"
	"github.com/carsimax/brunnr-code/internal/components/engram"
	"github.com/carsimax/brunnr-code/internal/components/gga"
	"github.com/carsimax/brunnr-code/internal/components/skills"
	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/reviewtransaction"
	"github.com/carsimax/brunnr-code/internal/state"
	"github.com/carsimax/brunnr-code/internal/statecoord"
)

func TestRetiredSDDDefaultUninstallSkipsLegacyCleanup(t *testing.T) {
	for _, kind := range []string{"complete", "partial"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			legacy := filepath.Join(home, ".claude", "commands", "sdd-custom.md")
			if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(legacy, []byte("user customized"), 0600); err != nil {
				t.Fatal(err)
			}
			svc, err := NewService(home, t.TempDir(), "dev")
			if err != nil {
				t.Fatal(err)
			}
			svc.snapshotter = stubSnapshotter{}
			if kind == "complete" {
				_, err = svc.CompleteUninstall()
			} else {
				_, err = svc.PartialUninstall([]model.AgentID{model.AgentClaudeCode}, nil)
			}
			if err != nil {
				t.Fatalf("default uninstall dispatched retired SDD: %v", err)
			}
			if body, err := os.ReadFile(legacy); err != nil || string(body) != "user customized" {
				t.Fatalf("custom legacy file changed: %q, %v", body, err)
			}
		})
	}
}

func TestRetiredSDDExplicitUninstallFailsWithoutWrites(t *testing.T) {
	for _, kind := range []string{"partial", "profile selection"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			legacy := filepath.Join(home, ".claude", "commands", "sdd-custom.md")
			if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(legacy, []byte("user customized"), 0600); err != nil {
				t.Fatal(err)
			}
			svc, err := NewService(home, t.TempDir(), "dev")
			if err != nil {
				t.Fatal(err)
			}
			before := map[string]string{}
			err = filepath.WalkDir(home, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if !entry.IsDir() {
					data, readErr := os.ReadFile(path)
					if readErr != nil {
						return readErr
					}
					before[path] = string(data)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if kind == "partial" {
				_, err = svc.PartialUninstall([]model.AgentID{model.AgentClaudeCode}, []model.ComponentID{model.ComponentSDD})
			} else {
				_, err = svc.PartialUninstallWithEngramScope([]model.AgentID{model.AgentClaudeCode}, []model.ComponentID{model.ComponentSDD}, model.EngramUninstallScopeGlobal)
			}
			if err == nil || !strings.Contains(err.Error(), "retired") {
				t.Fatalf("expected retired component rejection, got %v", err)
			}
			after := map[string]string{}
			err = filepath.WalkDir(home, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if !entry.IsDir() {
					data, readErr := os.ReadFile(path)
					if readErr != nil {
						return readErr
					}
					after[path] = string(data)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(before, after) {
				t.Fatalf("filesystem files changed: before=%v after=%v", before, after)
			}
		})
	}
}

// TestCompleteUninstallPreservesNativeReviewAndJudgmentDayAgents proves native
// review and Judgment Day agent files survive uninstall without a dedicated
// retention allowlist: uninstall only inspects the retired SDD agent files
// of the sub-agents directory, so these permanently-installed native agents
// (installed unconditionally by reviewassets.InstallNativeAgents,
// independent of any removable component) are never touched.
func TestCompleteUninstallPreservesNativeReviewAndJudgmentDayAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	adapter := claude.NewAdapter()
	agentsDir := adapter.SubAgentsDir(home)
	if err := os.MkdirAll(agentsDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"review-risk.md", "jd-judge-a.md"} {
		if err := os.WriteFile(filepath.Join(agentsDir, name), []byte("native agent"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	svc, err := NewService(home, t.TempDir(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	svc.snapshotter = stubSnapshotter{}
	if _, err := svc.CompleteUninstall(); err != nil {
		t.Fatalf("CompleteUninstall: %v", err)
	}
	for _, name := range []string{"review-risk.md", "jd-judge-a.md"} {
		if _, err := os.Stat(filepath.Join(agentsDir, name)); err != nil {
			t.Fatalf("native agent %s removed by uninstall: %v", name, err)
		}
	}
}

type stubSnapshotter struct{}

// TestPartialUninstallKeepsSharedGGAWhileOtherAgentInstalled covers #3534:
// uninstalling one agent must not wipe the shared GGA config that another
// still-installed agent depends on, while agent-scoped assets are still
// removed as before.
func TestPartialUninstallKeepsSharedGGAWhileOtherAgentInstalled(t *testing.T) {
	homeDir := t.TempDir()
	if err := state.Write(homeDir, state.InstallState{
		InstalledAgents: []string{"claude-code", "codex"},
	}); err != nil {
		t.Fatal(err)
	}

	configPath := gga.ConfigPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("shared gga config"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc, err := NewService(homeDir, t.TempDir(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	svc.snapshotter = stubSnapshotter{}

	result, err := svc.PartialUninstall([]model.AgentID{model.AgentCodex}, allManagedComponents)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("shared GGA config = %v, want it to survive because claude-code is still installed", err)
	}

	foundNote := false
	for _, action := range result.ManualActions {
		if strings.Contains(action, "GGA") && strings.Contains(action, "claude-code") {
			foundNote = true
		}
	}
	if !foundNote {
		t.Fatalf("manual actions = %v, want a note explaining the kept shared GGA config", result.ManualActions)
	}

	got, err := state.Read(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.InstalledAgents, []string{"claude-code"}) {
		t.Fatalf("installed agents after uninstall = %v, want [claude-code]", got.InstalledAgents)
	}
}

// TestPartialUninstallRemovesSharedGGAOnLastAgent covers the other half of
// #3534: once the last agent that depends on GGA is uninstalled, the shared
// config is removed like any other managed component.
func TestPartialUninstallRemovesSharedGGAOnLastAgent(t *testing.T) {
	homeDir := t.TempDir()
	if err := state.Write(homeDir, state.InstallState{
		InstalledAgents: []string{"codex"},
	}); err != nil {
		t.Fatal(err)
	}

	configPath := gga.ConfigPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("shared gga config"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc, err := NewService(homeDir, t.TempDir(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	svc.snapshotter = stubSnapshotter{}

	result, err := svc.PartialUninstall([]model.AgentID{model.AgentCodex}, allManagedComponents)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("shared GGA config stat error = %v, want removed once no agent needs it", err)
	}
	if !slices.Contains(result.RemovedFiles, configPath) {
		t.Fatalf("removed files = %v, want %q", result.RemovedFiles, configPath)
	}
	for _, action := range result.ManualActions {
		if strings.Contains(action, "GGA") {
			t.Fatalf("manual actions = %v, want no kept-GGA note on last-agent removal", result.ManualActions)
		}
	}
}

// TestPartialUninstallExplicitGGARequestSkippedWhileSharedWithAnotherAgent
// covers requesting `--components gga` explicitly for one agent while
// another installed agent still relies on it: the safer behavior is to skip
// the shared removal (rather than fail the whole command) and say why,
// instead of silently deleting a config another agent still needs.
func TestPartialUninstallExplicitGGARequestSkippedWhileSharedWithAnotherAgent(t *testing.T) {
	homeDir := t.TempDir()
	if err := state.Write(homeDir, state.InstallState{
		InstalledAgents: []string{"claude-code", "codex"},
	}); err != nil {
		t.Fatal(err)
	}

	configPath := gga.ConfigPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("shared gga config"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc, err := NewService(homeDir, t.TempDir(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	svc.snapshotter = stubSnapshotter{}

	result, err := svc.PartialUninstall([]model.AgentID{model.AgentCodex}, []model.ComponentID{model.ComponentGGA})
	if err != nil {
		t.Fatalf("explicit --components gga while shared should be skipped, not fail: %v", err)
	}

	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("shared GGA config = %v, want it to survive an explicit but unsafe removal request", err)
	}
	foundNote := false
	for _, action := range result.ManualActions {
		if strings.Contains(action, "GGA") && strings.Contains(action, "claude-code") {
			foundNote = true
		}
	}
	if !foundNote {
		t.Fatalf("manual actions = %v, want a note explaining why the explicit gga removal was skipped", result.ManualActions)
	}
}

// TestIsStateNotFoundUnwrapsWrappedError guards the classification used by
// otherInstalledAgents: it must follow the error-wrap chain (via errors.Is)
// rather than only recognizing a bare *fs.PathError the way os.IsNotExist
// effectively does for a caller that wraps state.Read's error further.
func TestIsStateNotFoundUnwrapsWrappedError(t *testing.T) {
	wrapped := fmt.Errorf("read install state: %w", fs.ErrNotExist)
	if !isStateNotFound(wrapped) {
		t.Fatalf("isStateNotFound(%v) = false, want true for a wrapped not-exist error", wrapped)
	}

	other := fmt.Errorf("read install state: %w", os.ErrPermission)
	if isStateNotFound(other) {
		t.Fatalf("isStateNotFound(%v) = true, want false for a non-not-exist error", other)
	}
}

// TestReconcileSharedComponentsWithUnreadableState covers a corrupt
// state.json: reconcileSharedComponents cannot tell whether another agent
// still needs GGA, so it fails closed toward preservation — GGA is dropped
// from the returned component set and a note explains why — instead of
// aborting the caller's partial uninstall, unless the caller explicitly
// requested "--components gga", where there is nothing safe left to do for
// that explicit request and the error is returned instead.
func TestReconcileSharedComponentsWithUnreadableState(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(state.Path(homeDir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.Path(homeDir), []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc, err := NewService(homeDir, t.TempDir(), "dev")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("default components keep GGA and continue", func(t *testing.T) {
		components, note, err := svc.reconcileSharedComponents(
			[]model.AgentID{model.AgentCodex}, slices.Clone(allManagedComponents), false)
		if err != nil {
			t.Fatalf("reconcileSharedComponents() error = %v, want nil (fail closed, don't abort)", err)
		}
		if slices.Contains(components, model.ComponentGGA) {
			t.Fatalf("components = %v, want GGA dropped when state cannot be read", components)
		}
		if !slices.Contains(components, model.ComponentTheme) {
			t.Fatalf("components = %v, want unrelated components preserved so the rest of the uninstall proceeds", components)
		}
		if !strings.Contains(note, "GGA") || !strings.Contains(note, "could not be read") {
			t.Fatalf("note = %q, want it to explain the state could not be read", note)
		}
	})

	t.Run("explicit gga request fails instead of silently skipping", func(t *testing.T) {
		_, _, err := svc.reconcileSharedComponents(
			[]model.AgentID{model.AgentCodex}, []model.ComponentID{model.ComponentGGA}, true)
		if err == nil {
			t.Fatal("reconcileSharedComponents() error = nil, want an error for an explicit --components gga request when state is unreadable")
		}
	})
}

func mustReadServiceFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCompleteUninstallKeepsExecutableRemovalAction(t *testing.T) {
	svc, err := NewService(t.TempDir(), t.TempDir(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	svc.snapshotter = stubSnapshotter{}

	result, err := svc.CompleteUninstall()
	if err != nil {
		t.Fatal(err)
	}
	want := "To completely remove gentle-ai from your system, delete the executable (e.g., rm -f $(which gentle-ai))"
	if !slices.Contains(result.ManualActions, want) {
		t.Fatalf("ManualActions = %v, want %q", result.ManualActions, want)
	}
}

func TestExpandVisualPolishUninstallComponents(t *testing.T) {
	for _, trigger := range []model.ComponentID{model.ComponentTheme} {
		got := expandVisualPolishUninstallComponents([]model.ComponentID{trigger})
		for _, want := range model.VisualPolishComponents() {
			if !slices.Contains(got, want) {
				t.Fatalf("%q expansion missing %q: %v", trigger, want, got)
			}
		}
	}
	for _, component := range []model.ComponentID{model.ComponentClaudeTheme, model.ComponentPersona} {
		got := expandVisualPolishUninstallComponents([]model.ComponentID{component})
		if !slices.Equal(got, []model.ComponentID{component}) {
			t.Fatalf("%q should not expand: %v", component, got)
		}
	}
}

func TestPartialUninstallClaudeThemeRemovesOnlyThemeAssets(t *testing.T) {
	homeDir := t.TempDir()
	workspaceDir := t.TempDir()

	svc, err := NewService(homeDir, workspaceDir, "dev")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	svc.snapshotter = stubSnapshotter{}

	managed := []string{
		filepath.Join(homeDir, ".claude", "themes", "gentleman.json"),
		filepath.Join(homeDir, ".claude", "themes", "gentleman-cute.json"),
	}
	for _, path := range managed {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"name":"managed"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	preserved := map[string]string{
		filepath.Join(homeDir, ".claude", "settings.json"):                   `{"theme":"active","outputStyle":"gentleman"}`,
		filepath.Join(homeDir, ".claude", "CLAUDE.md"):                       "# persona\n",
		filepath.Join(homeDir, ".claude", "output-styles", "gentleman.md"):   "# output style\n",
		filepath.Join(homeDir, ".claude", "commands", "gentle-sdd-apply.md"): "# SDD asset\n",
		filepath.Join(homeDir, ".claude", "themes", "custom.json"):           `{"theme":"custom"}`,
	}
	for path, content := range preserved {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := svc.PartialUninstall(
		[]model.AgentID{model.AgentClaudeCode},
		[]model.ComponentID{model.ComponentClaudeTheme},
	); err != nil {
		t.Fatalf("PartialUninstall() error = %v", err)
	}

	for _, path := range managed {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("managed theme %q should be removed: %v", path, err)
		}
	}
	for path, want := range preserved {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Fatalf("preserved asset %q = %q, %v; want %q", path, got, err, want)
		}
	}
}

func readJSONFileForTest(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", path, err)
	}
	return root
}

func (stubSnapshotter) Create(snapshotDir string, paths []string) (backup.Manifest, error) {
	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		return backup.Manifest{}, err
	}
	return backup.Manifest{
		ID:        "snapshot-001",
		CreatedAt: time.Now().UTC(),
	}, nil
}

func TestExecutePlanReportsManualCleanupForNonEmptyDirectory(t *testing.T) {
	homeDir := t.TempDir()
	workspaceDir := t.TempDir()

	svc, err := NewService(homeDir, workspaceDir, "dev")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	svc.snapshotter = stubSnapshotter{}
	svc.now = func() time.Time { return time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC) }

	nonEmptyDir := filepath.Join(homeDir, ".codex", "skills")
	if err := os.MkdirAll(nonEmptyDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(nonEmptyDir, "user-skill.md"), []byte("keep me"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	statePath := filepath.Join(homeDir, ".gentle-ai", "state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("MkdirAll(state dir) error = %v", err)
	}
	if err := os.WriteFile(statePath, []byte(`{"installed_agents":[]}`), 0o644); err != nil {
		t.Fatalf("WriteFile(state) error = %v", err)
	}

	result, err := svc.executePlan(plan{
		backupTargets: []string{statePath},
		operations: []operation{
			removeDirIfEmpty(nonEmptyDir),
		},
	}, []model.AgentID{})
	if err != nil {
		t.Fatalf("executePlan() error = %v", err)
	}

	if len(result.ManualActions) != 1 {
		t.Fatalf("ManualActions len = %d, want 1; got %v", len(result.ManualActions), result.ManualActions)
	}
	if !strings.Contains(result.ManualActions[0], nonEmptyDir) {
		t.Fatalf("manual action should mention %q, got %q", nonEmptyDir, result.ManualActions[0])
	}
}

func TestComponentOperationsContext7ClaudeRemovesSettingsAndManagedLegacyFile(t *testing.T) {
	homeDir := t.TempDir()
	workspaceDir := t.TempDir()

	svc, err := NewService(homeDir, workspaceDir, "dev")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	adapter, ok := svc.registry.Get(model.AgentClaudeCode)
	if !ok {
		t.Fatal("Claude adapter not found in registry")
	}

	settingsPath := adapter.SettingsPath(homeDir)
	legacyPath := adapter.MCPConfigPath(homeDir, "context7")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(settings dir) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(legacy dir) error = %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"mcpServers":{"context7":{"command":"npx"},"engram":{"command":"engram"}},"theme":"dark"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(settings) error = %v", err)
	}
	legacyManaged := []byte(`{
  "command": "npx",
  "args": [
    "-y",
    "--package=@upstash/context7-mcp@1.0.0",
    "--",
    "context7-mcp"
  ]
}
`)
	if err := os.WriteFile(legacyPath, legacyManaged, 0o644); err != nil {
		t.Fatalf("WriteFile(legacy) error = %v", err)
	}

	ops, targets, err := svc.componentOperations(adapter, model.ComponentContext7)
	if err != nil {
		t.Fatalf("componentOperations() error = %v", err)
	}
	if !slices.Contains(targets, settingsPath) || !slices.Contains(targets, legacyPath) {
		t.Fatalf("targets = %#v, want settings and legacy paths", targets)
	}
	for _, op := range ops {
		if _, _, err := op.apply(op.path); err != nil {
			t.Fatalf("operation %v on %q error = %v", op.typeID, op.path, err)
		}
	}

	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy managed context7 file should be removed; stat err = %v", err)
	}
	settings := readJSONFileForTest(t, settingsPath)
	mcpServers := settings["mcpServers"].(map[string]any)
	if _, ok := mcpServers["context7"]; ok {
		t.Fatalf("settings still contains mcpServers.context7: %#v", settings)
	}
	if _, ok := mcpServers["engram"]; !ok {
		t.Fatalf("settings lost unrelated mcpServers.engram: %#v", settings)
	}
}

func TestComponentOperationsClaudeNeverDeleteUserRegistry(t *testing.T) {
	homeDir := t.TempDir()
	workspaceDir := t.TempDir()

	svc, err := NewService(homeDir, workspaceDir, "dev")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	adapter, ok := svc.registry.Get(model.AgentClaudeCode)
	if !ok {
		t.Fatal("Claude adapter not found in registry")
	}

	// The registry holds ONLY the managed server, so removing it empties the
	// file; ~/.claude.json must survive because Claude Code owns it.
	registryPath := claude.UserConfigPath(homeDir)
	seed := []byte(`{"mcpServers":{"context7":{"command":"npx"}}}`)
	if err := os.WriteFile(registryPath, seed, 0o600); err != nil {
		t.Fatalf("WriteFile(registry) error = %v", err)
	}

	ops, _, err := svc.componentOperations(adapter, model.ComponentContext7)
	if err != nil {
		t.Fatalf("componentOperations(context7) error = %v", err)
	}
	for _, op := range ops {
		if _, _, err := op.apply(op.path); err != nil {
			t.Fatalf("operation %v on %q error = %v", op.typeID, op.path, err)
		}
	}

	info, err := os.Stat(registryPath)
	if err != nil {
		t.Fatalf("~/.claude.json must survive removing the last managed server: %v", err)
	}
	registry := readJSONFileForTest(t, registryPath)
	if servers, ok := registry["mcpServers"].(map[string]any); ok {
		if _, still := servers["context7"]; still {
			t.Fatalf("registry still contains mcpServers.context7: %#v", registry)
		}
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("registry mode widened to %v, want 0600", info.Mode().Perm())
	}
}

func TestComponentOperationsEngramClaudePreservesRegistryAndRemovesManagedLegacy(t *testing.T) {
	homeDir := t.TempDir()
	svc, err := NewService(homeDir, t.TempDir(), "dev")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	adapter, ok := svc.registry.Get(model.AgentClaudeCode)
	if !ok {
		t.Fatal("Claude adapter not found in registry")
	}

	registryPath := claude.UserConfigPath(homeDir)
	registry := []byte(`{"oauthAccount":{"emailAddress":"user@example.com"},"mcpServers":{"codegraph":{"command":"codegraph"},"engram":{"command":"/usr/local/bin/engram","args":["mcp","--tools=agent"]}}}`)
	if err := os.WriteFile(registryPath, registry, 0o600); err != nil {
		t.Fatalf("WriteFile(user registry) error = %v", err)
	}
	legacyPath := adapter.MCPConfigPath(homeDir, "engram")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(legacy dir) error = %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte(`{"command":"/usr/local/bin/engram","args":["mcp","--tools=agent"]}`), 0o644); err != nil {
		t.Fatalf("WriteFile(legacy config) error = %v", err)
	}

	ops, targets, err := svc.componentOperations(adapter, model.ComponentEngram)
	if err != nil {
		t.Fatalf("componentOperations(engram) error = %v", err)
	}
	for _, want := range []string{registryPath, legacyPath} {
		if !slices.Contains(targets, want) {
			t.Fatalf("uninstall targets missing %q: %v", want, targets)
		}
	}
	for _, op := range ops {
		if _, _, err := op.apply(op.path); err != nil {
			t.Fatalf("operation %v on %q error = %v", op.typeID, op.path, err)
		}
	}

	remaining := readJSONFileForTest(t, registryPath)
	servers, _ := remaining["mcpServers"].(map[string]any)
	if _, exists := servers["engram"]; exists {
		t.Fatalf("user registry still contains mcpServers.engram: %#v", remaining)
	}
	if got := remaining["oauthAccount"].(map[string]any)["emailAddress"]; got != "user@example.com" {
		t.Fatalf("OAuth data changed during uninstall: %#v", remaining)
	}
	if _, statErr := os.Stat(legacyPath); !os.IsNotExist(statErr) {
		t.Fatalf("managed legacy config must be removed; stat error = %v", statErr)
	}
	if info, statErr := os.Stat(registryPath); statErr != nil {
		t.Fatalf("Stat(user registry) error = %v", statErr)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("user registry mode = %o; want 0600", info.Mode().Perm())
	}
}

func TestComponentOperationsContext7ClaudePreservesCustomLegacyFile(t *testing.T) {
	homeDir := t.TempDir()
	workspaceDir := t.TempDir()

	svc, err := NewService(homeDir, workspaceDir, "dev")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	adapter, ok := svc.registry.Get(model.AgentClaudeCode)
	if !ok {
		t.Fatal("Claude adapter not found in registry")
	}

	settingsPath := adapter.SettingsPath(homeDir)
	legacyPath := adapter.MCPConfigPath(homeDir, "context7")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(settings dir) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(legacy dir) error = %v", err)
	}
	custom := []byte(`{"command":"custom-context7"}`)
	if err := os.WriteFile(settingsPath, []byte(`{"mcpServers":{"context7":{"command":"npx"}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(settings) error = %v", err)
	}
	if err := os.WriteFile(legacyPath, custom, 0o644); err != nil {
		t.Fatalf("WriteFile(legacy) error = %v", err)
	}

	ops, _, err := svc.componentOperations(adapter, model.ComponentContext7)
	if err != nil {
		t.Fatalf("componentOperations() error = %v", err)
	}
	for _, op := range ops {
		if _, _, err := op.apply(op.path); err != nil {
			t.Fatalf("operation %v on %q error = %v", op.typeID, op.path, err)
		}
	}

	got, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("ReadFile(legacy) error = %v", err)
	}
	if string(got) != string(custom) {
		t.Fatalf("custom legacy file changed: %s", string(got))
	}
}

func TestComponentOperationsEngram_ProjectScopeRemovesWorkspaceDataOnly(t *testing.T) {
	homeDir := t.TempDir()
	workspaceDir := t.TempDir()

	svc, err := NewService(homeDir, workspaceDir, "dev")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	adapter, ok := svc.registry.Get(model.AgentClaudeCode)
	if !ok {
		t.Fatal("claude adapter not found in registry")
	}

	settingsPath := claude.UserConfigPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(settings dir) error = %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"mcpServers":{"engram":{"command":"engram"}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(settings) error = %v", err)
	}

	projectDataDir := filepath.Join(workspaceDir, ".engram")
	if err := os.MkdirAll(projectDataDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(projectDataDir) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDataDir, "memory.db"), []byte("db"), 0o644); err != nil {
		t.Fatalf("WriteFile(memory.db) error = %v", err)
	}

	svc.SetEngramUninstallScope(model.EngramUninstallScopeProject)

	ops, _, err := svc.componentOperations(adapter, model.ComponentEngram)
	if err != nil {
		t.Fatalf("componentOperations() error = %v", err)
	}

	for _, op := range ops {
		if _, _, err := op.apply(op.path); err != nil {
			t.Fatalf("op.apply(%q) error = %v", op.path, err)
		}
	}

	if _, err := os.Stat(projectDataDir); !os.IsNotExist(err) {
		t.Fatalf("project .engram dir should be removed; err = %v", err)
	}

	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile(settings) error = %v", err)
	}
	if !strings.Contains(string(raw), `"engram"`) {
		t.Fatalf("global engram config should be preserved in project scope, got: %s", string(raw))
	}
}

func TestComponentOperationsEngram_GlobalScopeKeepsWorkspaceProjectData(t *testing.T) {
	homeDir := t.TempDir()
	workspaceDir := t.TempDir()

	svc, err := NewService(homeDir, workspaceDir, "dev")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	adapter, ok := svc.registry.Get(model.AgentClaudeCode)
	if !ok {
		t.Fatal("claude adapter not found in registry")
	}

	settingsPath := claude.UserConfigPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(settings dir) error = %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"mcpServers":{"engram":{"command":"engram"}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(settings) error = %v", err)
	}

	projectDataDir := filepath.Join(workspaceDir, ".engram")
	if err := os.MkdirAll(projectDataDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(projectDataDir) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDataDir, "memory.db"), []byte("db"), 0o644); err != nil {
		t.Fatalf("WriteFile(memory.db) error = %v", err)
	}

	svc.SetEngramUninstallScope(model.EngramUninstallScopeGlobal)

	ops, _, err := svc.componentOperations(adapter, model.ComponentEngram)
	if err != nil {
		t.Fatalf("componentOperations() error = %v", err)
	}

	for _, op := range ops {
		if _, _, err := op.apply(op.path); err != nil {
			t.Fatalf("op.apply(%q) error = %v", op.path, err)
		}
	}

	if _, err := os.Stat(projectDataDir); err != nil {
		t.Fatalf("project .engram dir should be preserved in global scope, err = %v", err)
	}

	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		if !os.IsNotExist(err) {
			t.Fatalf("ReadFile(settings) error = %v", err)
		}
		return
	}
	if strings.Contains(string(raw), `"engram"`) {
		t.Fatalf("global engram config should be removed in global scope, got: %s", string(raw))
	}
}

// TestComponentOperationsEngram_CodexRemovesConsolidatedProtocolAssetsWithNoOrphans
// is the task 2.9 regression assertion: the canonical-asset consolidation
// (design.md Decision 3) renamed/removed the SOURCE assets
// (internal/assets/claude/engram-protocol.md, codex/engram-instructions.md,
// codex/engram-compact-prompt.md -> internal/assets/engram/protocol.md), but
// the WRITTEN on-disk paths for Codex (~/.codex/engram-instructions.md,
// ~/.codex/engram-compact-prompt.md) MUST stay byte-identical so the
// uninstaller keeps covering them with no orphaned files left behind.
func TestComponentOperationsEngram_CodexRemovesConsolidatedProtocolAssetsWithNoOrphans(t *testing.T) {
	restore := codex.SetRuntimeVersionCommandForTest("codex-cli 0.144.0", nil)
	t.Cleanup(restore)
	homeDir := t.TempDir()
	workspaceDir := t.TempDir()

	svc, err := NewService(homeDir, workspaceDir, "dev")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	adapter, ok := svc.registry.Get(model.AgentCodex)
	if !ok {
		t.Fatal("codex adapter not found in registry")
	}

	// Actually write the files via the real (post-consolidation) engram
	// injector, instead of hand-crafting fixtures, so this test fails if the
	// renderer ever drifts from the on-disk paths the uninstaller expects.
	if _, err := engram.InjectWithOptions(homeDir, adapter, engram.InjectOptions{}); err != nil {
		t.Fatalf("engram.InjectWithOptions(codex) error = %v", err)
	}

	instructionsPath := filepath.Join(homeDir, ".codex", "engram-instructions.md")
	compactPath := filepath.Join(homeDir, ".codex", "engram-compact-prompt.md")
	for _, path := range []string{instructionsPath, compactPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected engram injection to create %q: %v", path, err)
		}
	}

	svc.SetEngramUninstallScope(model.EngramUninstallScopeGlobal)

	ops, targets, err := svc.componentOperations(adapter, model.ComponentEngram)
	if err != nil {
		t.Fatalf("componentOperations() error = %v", err)
	}

	for _, want := range []string{instructionsPath, compactPath} {
		if !slices.Contains(targets, want) {
			t.Fatalf("componentOperations() targets missing %q; got: %v", want, targets)
		}
	}

	for _, op := range ops {
		if _, _, err := op.apply(op.path); err != nil {
			t.Fatalf("op.apply(%q) error = %v", op.path, err)
		}
	}

	for _, path := range []string{instructionsPath, compactPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %q to be removed by uninstall, err = %v", path, err)
		}
	}

	// No orphaned directory left behind either.
	if entries, err := os.ReadDir(filepath.Join(homeDir, ".codex")); err == nil {
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "engram-") {
				t.Fatalf("orphaned engram asset left behind after uninstall: %s", entry.Name())
			}
		}
	}
}

func TestFullAgentClaudeRemovesSkillRegistryHook(t *testing.T) {
	homeDir := t.TempDir()
	workspaceDir := t.TempDir()

	svc, err := NewService(homeDir, workspaceDir, "dev")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	adapter, ok := svc.registry.Get(model.AgentClaudeCode)
	if !ok {
		t.Fatal("claude adapter not found in registry")
	}
	settingsPath := adapter.SettingsPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	initial := `{
  "hooks": {
    "UserPromptSubmit": [
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "gentle-ai skill-registry refresh --quiet --no-gitignore --cwd \"${CLAUDE_PROJECT_DIR:-$PWD}\" || true"},
          {"type": "command", "command": "echo keep"}
        ]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{"type": "command", "command": "echo pre"}]
      }
    ],
    "SubagentStop": [
      {
        "hooks": [
          {"type": "command", "command": "echo subagent keep"}
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {"type": "command", "command": "echo stop keep"}
        ]
      }
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	built, err := svc.buildPlan([]model.AgentID{adapter.Agent()}, withoutComponent(fullAgentRemovalComponents, model.ComponentSDD))
	ops := built.operations
	if err != nil {
		t.Fatalf("componentOperations() error = %v", err)
	}
	for _, op := range ops {
		if op.typeID == opRewriteFile && op.path == settingsPath {
			if _, _, err := op.apply(op.path); err != nil {
				t.Fatalf("settings rewrite op.apply() error = %v", err)
			}
		}
	}
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "gentle-ai skill-registry refresh") {
		t.Fatalf("managed hook should be removed:\n%s", text)
	}
	if !strings.Contains(text, "echo keep") || !strings.Contains(text, "echo pre") || !strings.Contains(text, "echo subagent keep") || !strings.Contains(text, "echo stop keep") {
		t.Fatalf("unrelated hooks should be preserved:\n%s", text)
	}
}

func TestFullAgentClaudeRemovesReviewAndPreflightHooks(t *testing.T) {
	homeDir := t.TempDir()
	workspaceDir := t.TempDir()

	svc, err := NewService(homeDir, workspaceDir, "dev")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	adapter, ok := svc.registry.Get(model.AgentClaudeCode)
	if !ok {
		t.Fatal("claude adapter not found in registry")
	}
	settingsPath := adapter.SettingsPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	initial := `{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "gentle-ai review stop-hook --agent claude-code", "timeout": 60},
          {"type": "command", "command": "echo keep"}
        ]
      }
    ],
    "SessionStart": [
      {
        "matcher": "startup|resume|clear|compact",
        "hooks": [
          {"type": "command", "command": "gentle-ai review stop-hook --agent claude-code", "timeout": 30},
          {"type": "command", "command": "echo custom session-start"}
        ]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{"type": "command", "command": "echo pre"}]
      },
      {
        "matcher": "Agent",
        "hooks": [{"type": "command", "command": "gentle-ai sdd-preflight-hook --agent claude-code"}]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "AskUserQuestion",
        "hooks": [
          {"type": "command", "command": "gentle-ai sdd-preflight-hook --agent claude-code"},
          {"type": "command", "command": "echo post keep"}
        ]
      }
    ],
    "SessionEnd": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "gentle-ai sdd-preflight-hook --agent claude-code"}]
      }
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	built, err := svc.buildPlan([]model.AgentID{adapter.Agent()}, withoutComponent(fullAgentRemovalComponents, model.ComponentSDD))
	ops := built.operations
	if err != nil {
		t.Fatalf("componentOperations() error = %v", err)
	}
	for _, op := range ops {
		if op.typeID == opRewriteFile && op.path == settingsPath {
			if _, _, err := op.apply(op.path); err != nil {
				t.Fatalf("settings rewrite op.apply() error = %v", err)
			}
		}
	}
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "gentle-ai review stop-hook") || strings.Count(text, "gentle-ai sdd-preflight-hook") != 3 {
		t.Fatalf("review hooks should be removed; unmarked legacy hooks preserved:\n%s", text)
	}
	if !strings.Contains(text, "echo keep") || !strings.Contains(text, "echo pre") || !strings.Contains(text, "echo post keep") || !strings.Contains(text, "echo custom session-start") {
		t.Fatalf("unrelated hooks should be preserved:\n%s", text)
	}
}

func TestFullAgentCodexRemovesSkillRegistryHook(t *testing.T) {
	homeDir := t.TempDir()
	workspaceDir := t.TempDir()

	svc, err := NewService(homeDir, workspaceDir, "dev")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	adapter, ok := svc.registry.Get(model.AgentCodex)
	if !ok {
		t.Fatal("codex adapter not found in registry")
	}
	hooksPath := filepath.Join(adapter.GlobalConfigDir(homeDir), "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatal(err)
	}
	initial := `{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "startup|resume|clear|compact",
        "hooks": [
          {"type": "command", "command": "gentle-ai skill-registry refresh --quiet --no-gitignore --cwd \"$PWD\" || true"},
          {"type": "command", "command": "echo keep"}
        ]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{"type": "command", "command": "echo pre"}]
      }
    ]
  }
}`
	if err := os.WriteFile(hooksPath, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	built, err := svc.buildPlan([]model.AgentID{adapter.Agent()}, withoutComponent(fullAgentRemovalComponents, model.ComponentSDD))
	ops := built.operations
	if err != nil {
		t.Fatalf("componentOperations() error = %v", err)
	}
	for _, op := range ops {
		if op.typeID == opRewriteFile && op.path == hooksPath {
			if _, _, err := op.apply(op.path); err != nil {
				t.Fatalf("Codex hooks rewrite op.apply() error = %v", err)
			}
		}
	}
	raw, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "gentle-ai skill-registry refresh") {
		t.Fatalf("managed hook should be removed:\n%s", text)
	}
	if !strings.Contains(text, "echo keep") || !strings.Contains(text, "echo pre") {
		t.Fatalf("unrelated hooks should be preserved:\n%s", text)
	}
}

func TestRetainedHooksOutsideFullAgentRemoval(t *testing.T) {
	for _, agentID := range []model.AgentID{model.AgentClaudeCode, model.AgentCodex} {
		t.Run(string(agentID), func(t *testing.T) {
			svc, err := NewService(t.TempDir(), t.TempDir(), "dev")
			if err != nil {
				t.Fatal(err)
			}
			adapter, _ := svc.registry.Get(agentID)
			path := adapter.SettingsPath(svc.homeDir)
			if agentID == model.AgentCodex {
				path = filepath.Join(adapter.GlobalConfigDir(svc.homeDir), "hooks.json")
			}
			for _, components := range [][]model.ComponentID{{model.ComponentPersona}} {
				p, err := svc.buildPlan([]model.AgentID{agentID}, components)
				if err != nil {
					t.Fatal(err)
				}
				for _, op := range p.operations {
					if op.path == path && op.typeID == opRewriteFile && agentID == model.AgentCodex {
						t.Fatal("component-only removal must not rewrite retained Codex hooks")
					}
				}
			}
			p, err := svc.buildPlan([]model.AgentID{agentID}, withoutComponent(fullAgentRemovalComponents, model.ComponentSDD))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(p.backupTargets, path) {
				t.Fatalf("missing rollback backup for %s", path)
			}
			found := false
			for _, op := range p.operations {
				if op.path == path && op.typeID == opRewriteFile {
					found = true
					if !slices.Contains(op.agents, agentID) {
						t.Fatalf("missing failure owner %s", agentID)
					}
				}
			}
			if !found {
				t.Fatalf("missing retained hook cleanup for %s", agentID)
			}
		})
	}
}

// TestUpdateStateAfterUninstallReReadsLatestStateAfterLockContention is the
// #1809 preservation proof for updateStateAfterUninstall: the whole
// read-modify-write must run inside the canonical install-state lock, so a
// concurrent writer's RDDMode survives the agent removal. A contended lock
// must fail fast without writing anything.
func TestUpdateStateAfterUninstallReReadsLatestStateAfterLockContention(t *testing.T) {
	home := t.TempDir()
	lockPath, err := statecoord.LockPath(home)
	if err != nil {
		t.Fatalf("install state lock path: %v", err)
	}
	held, err := reviewtransaction.AcquireAuthorityFileLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.InstallState{
		InstalledAgents: []string{string(model.AgentCodex), "claude-code"},
		RDDMode:         string(reviewtransaction.RDDModeOn),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := updateStateAfterUninstall(home, []model.AgentID{model.AgentCodex}); err == nil || !strings.Contains(err.Error(), "acquire install state lock") {
		t.Fatalf("contended uninstall state update error = %v", err)
	}
	contended, err := state.Read(home)
	if err != nil || len(contended.InstalledAgents) != 2 || contended.RDDMode != string(reviewtransaction.RDDModeOn) {
		t.Fatalf("state after contended run = %#v, err = %v", contended, err)
	}
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}

	removed, err := updateStateAfterUninstall(home, []model.AgentID{model.AgentCodex})
	if err != nil {
		t.Fatalf("updateStateAfterUninstall() error = %v", err)
	}
	if len(removed) != 1 || removed[0] != model.AgentCodex {
		t.Fatalf("removed = %v, want [codex]", removed)
	}
	got, err := state.Read(home)
	if err != nil {
		t.Fatalf("re-read state: %v", err)
	}
	if len(got.InstalledAgents) != 1 || got.InstalledAgents[0] != "claude-code" {
		t.Fatalf("InstalledAgents = %v, want [claude-code]", got.InstalledAgents)
	}
	if got.RDDMode != string(reviewtransaction.RDDModeOn) {
		t.Fatalf("concurrent RDDMode clobbered: got %q, want %q", got.RDDMode, reviewtransaction.RDDModeOn)
	}
}

func TestUninstallSkillsRemovesLegacySharedMarkerAfterUpgrade(t *testing.T) {
	for _, tt := range []struct {
		name          string
		dirMarker     bool
		wantMarker    bool
		wantSharedDir bool
	}{
		{name: "generated regular marker is removed with emptied _shared"},
		{name: "non-regular marker is preserved", dirMarker: true, wantMarker: true, wantSharedDir: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			skillDir := claude.NewAdapter().SkillsDir(home)
			shared, err := skills.SharedReferencePaths(skillDir)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range shared {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("managed reference"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			marker := skills.LegacySharedMarkerPath(skillDir)
			if tt.dirMarker {
				if err := os.MkdirAll(marker, 0o755); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(marker, []byte("legacy generated marker"), 0o644); err != nil {
				t.Fatal(err)
			}

			svc, err := NewService(home, t.TempDir(), "dev")
			if err != nil {
				t.Fatal(err)
			}
			svc.snapshotter = stubSnapshotter{}
			result, err := svc.PartialUninstall([]model.AgentID{model.AgentClaudeCode}, []model.ComponentID{model.ComponentSkills})
			if err != nil {
				t.Fatal(err)
			}

			if _, err := os.Lstat(marker); (err == nil) != tt.wantMarker {
				t.Fatalf("legacy marker present = %v, want %v (err %v)", err == nil, tt.wantMarker, err)
			}
			if got := slices.Contains(result.RemovedFiles, marker); got == tt.wantMarker {
				t.Fatalf("RemovedFiles contains marker = %v, want %v: %v", got, !tt.wantMarker, result.RemovedFiles)
			}
			if _, err := os.Stat(filepath.Dir(marker)); (err == nil) != tt.wantSharedDir {
				t.Fatalf("_shared present = %v, want %v (err %v)", err == nil, tt.wantSharedDir, err)
			}
		})
	}
}
