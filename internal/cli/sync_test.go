package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/agents/codex"
	"github.com/carsimax/brunnr-code/internal/backup"
	"github.com/carsimax/brunnr-code/internal/components/agentguidance"
	"github.com/carsimax/brunnr-code/internal/components/engram"
	"github.com/carsimax/brunnr-code/internal/components/filemerge"
	"github.com/carsimax/brunnr-code/internal/components/reviewassets"
	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/pipeline"
	"github.com/carsimax/brunnr-code/internal/planner"
	"github.com/carsimax/brunnr-code/internal/state"
	"github.com/carsimax/brunnr-code/internal/verify"
)

// markerMigrationFailureStep fails only once the migration's settings write is visible.
type markerMigrationFailureStep struct {
	path          string
	before        []byte
	observedWrite *bool
	cause         error
}

func (s markerMigrationFailureStep) ID() string { return "test:after-marker-migration" }
func (s markerMigrationFailureStep) Run() error {
	updated, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	if bytes.Equal(updated, s.before) || bytes.Contains(updated, []byte(`"__managed_by"`)) {
		return errors.New("marker migration did not write cleaned settings")
	}
	*s.observedWrite = true
	return s.cause
}

// ─── Phase 1: ParseSyncFlags ───────────────────────────────────────────────

func TestSyncFlagsRetiredOptionsRejectedAndHelpOmitted(t *testing.T) {
	for _, name := range []string{"profile", "profile-phase"} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseSyncFlags([]string{"--" + name, "value"})
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("retired flag %s accepted: %v", name, err)
			}
		})
	}
	var help strings.Builder
	PrintSyncHelp(&help)
	if strings.Contains(help.String(), "--profile") {
		t.Fatalf("sync help advertises retired flags: %s", help.String())
	}
}

func TestParseSyncFlagsDefaults(t *testing.T) {
	flags, err := ParseSyncFlags([]string{})
	if err != nil {
		t.Fatalf("ParseSyncFlags() error = %v", err)
	}

	if len(flags.Agents) != 0 {
		t.Errorf("Agents = %v, want empty", flags.Agents)
	}
	if flags.DryRun {
		t.Errorf("DryRun = true, want false")
	}
}

func TestParseSyncFlagsAgentsCSV(t *testing.T) {
	flags, err := ParseSyncFlags([]string{"--agents", "claude-code,codex"})
	if err != nil {
		t.Fatalf("ParseSyncFlags() error = %v", err)
	}

	want := []string{"claude-code", "codex"}
	if !reflect.DeepEqual(flags.Agents, want) {
		t.Errorf("Agents = %v, want %v", flags.Agents, want)
	}
}

func TestParseSyncFlagsAgentsRepeated(t *testing.T) {
	flags, err := ParseSyncFlags([]string{"--agent", "claude-code", "--agent", "codex"})
	if err != nil {
		t.Fatalf("ParseSyncFlags() error = %v", err)
	}

	want := []string{"claude-code", "codex"}
	if !reflect.DeepEqual(flags.Agents, want) {
		t.Errorf("Agents = %v, want %v", flags.Agents, want)
	}
}

// TestRunSyncRejectsUnsupportedAgent closes install/sync surface audit
// finding 3: `brunnr-code sync --agent cluade` (a typo) previously printed
// "All managed assets are already up to date. No files changed." — the user
// believed they synced, but asAgentIDs silently converted the typo into an
// AgentID nothing ever matches, so DiscoverAgents-equivalent resolution
// produced a no-op instead of an error.
func TestRunSyncRejectsUnsupportedAgent(t *testing.T) {
	home := t.TempDir()
	original := osUserHomeDir
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { osUserHomeDir = original })

	_, err := RunSync([]string{"--agent", "cluade"})
	if err == nil {
		t.Fatal("RunSync() error = nil, want an error rejecting the unsupported agent")
	}
	if !strings.Contains(err.Error(), "cluade") {
		t.Fatalf("error = %q, want it to name the offending value %q", err.Error(), "cluade")
	}
}

func TestParseSyncFlagsDryRun(t *testing.T) {
	flags, err := ParseSyncFlags([]string{"--dry-run"})
	if err != nil {
		t.Fatalf("ParseSyncFlags() error = %v", err)
	}
	if !flags.DryRun {
		t.Errorf("DryRun = false, want true")
	}
}

func TestParseSyncFlagsUnknownFlagReturnsError(t *testing.T) {
	_, err := ParseSyncFlags([]string{"--unknown-flag"})
	if err == nil {
		t.Fatalf("ParseSyncFlags() expected error for unknown flag")
	}
}

// TestParseSyncFlagsMistypedFlagNamesTheSupportedFlags closes install/sync
// surface audit finding 4: a mistyped flag like `-bogus` produced only
// "flag provided but not defined: -bogus" with the
// FlagSet's own usage output suppressed (fs.SetOutput(ioDiscard{})) and no
// pointer to how to discover the real flags. The fix captures the FlagSet's
// own canonical usage text (derived from the registered flags themselves,
// not a hand-written list) instead of discarding it, and names
// `brunnr-code sync --help`.
func TestParseSyncFlagsMistypedFlagNamesTheSupportedFlags(t *testing.T) {
	_, err := ParseSyncFlags([]string{"-bogus", "single"})
	if err == nil {
		t.Fatal("ParseSyncFlags() error = nil, want an error for the undefined -bogus flag")
	}
	msg := err.Error()
	if !strings.Contains(msg, "flag provided but not defined: -bogus") {
		t.Fatalf("error = %q, want it to preserve the original flag package error", msg)
	}
	if !strings.Contains(msg, "brunnr-code sync --help") {
		t.Fatalf("error = %q, want it to point at `brunnr-code sync --help`", msg)
	}
	if strings.Contains(msg, "-profile-phase") {
		t.Fatalf("usage advertises retired flags: %q", msg)
	}
	if !strings.Contains(msg, "-strict-tdd") {
		t.Fatalf("usage omitted strict-tdd: %q", msg)
	}
}

// TestParseSyncFlagsHelpFlagRendersUsage proves `brunnr-code sync --help` now
// actually surfaces the supported flags instead of the bare
// "flag: help requested" text it produced before (the FlagSet's usage output
// was being discarded via ioDiscard{}).
func TestParseSyncFlagsHelpFlagRendersUsage(t *testing.T) {
	_, err := ParseSyncFlags([]string{"--help"})
	if err == nil {
		t.Fatal("ParseSyncFlags() error = nil, want flag.ErrHelp wrapped with usage text")
	}
	if strings.Contains(err.Error(), "-profile-phase") {
		t.Fatalf("help advertises retired flags: %q", err)
	}
	if !strings.Contains(err.Error(), "-strict-tdd") {
		t.Fatalf("help omitted strict-tdd: %q", err)
	}
}

// TestParseSyncFlagsPositionalArgumentNamesTheAgentFlag closes install/sync
// surface audit finding 5: `brunnr-code sync claude` (a positional agent name
// instead of a flag) produced only `unexpected sync argument "claude"` with
// no pointer to the correct --agent form.
func TestParseSyncFlagsPositionalArgumentNamesTheAgentFlag(t *testing.T) {
	_, err := ParseSyncFlags([]string{"claude"})
	if err == nil {
		t.Fatal("ParseSyncFlags() error = nil, want an error for the positional argument")
	}
	msg := err.Error()
	if !strings.Contains(msg, `unexpected sync argument "claude"`) {
		t.Fatalf("error = %q, want it to preserve the original message", msg)
	}
	if !strings.Contains(msg, "--agent claude") {
		t.Fatalf("error = %q, want it to name the --agent claude form", msg)
	}
}

// ─── Phase 1: BuildSyncSelection ──────────────────────────────────────────

func TestBuildSyncSelectionIsTheSetup(t *testing.T) {
	agents := []model.AgentID{model.AgentClaudeCode}

	sel := BuildSyncSelection(agents)

	if !reflect.DeepEqual(sel.Agents, agents) {
		t.Errorf("Agents = %v, want %v", sel.Agents, agents)
	}
	// Persona content between <!-- brunnr-code:persona --> markers is
	// harness-managed, so sync propagates it like every other component.
	want := []model.ComponentID{model.ComponentEngram, model.ComponentSkills, model.ComponentPermission, model.ComponentPersona}
	if !slices.Equal(sel.Components, want) {
		t.Errorf("Components = %v, want %v", sel.Components, want)
	}
	if !slices.Equal(sel.Skills, setupSkillIDs) {
		t.Errorf("Skills = %v, want %v", sel.Skills, setupSkillIDs)
	}
	if sel.Preset != model.PresetFull || sel.StrictTDD {
		t.Errorf("Preset = %q, StrictTDD = %v, want full and false", sel.Preset, sel.StrictTDD)
	}
}

// ─── Phase 2: DiscoverAgents ───────────────────────────────────────────────

func TestDiscoverAgentsReturnsAgentsWithConfigDirPresent(t *testing.T) {
	home := t.TempDir()

	// Create the GlobalConfigDir for claude-code: ~/.claude/
	claudeConfigDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	discovered := DiscoverAgents(home)

	found := false
	for _, id := range discovered {
		if id == model.AgentClaudeCode {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("DiscoverAgents() expected claude-code when ~/.claude/ exists, got %v", discovered)
	}
}

func TestDiscoverAgentsReturnsEmptyWhenNoConfigDirsPresent(t *testing.T) {
	home := t.TempDir()
	// Empty home dir — no agent config dirs exist.

	discovered := DiscoverAgents(home)

	if len(discovered) != 0 {
		t.Errorf("DiscoverAgents() expected empty, got %v", discovered)
	}
}

func TestDiscoverAgentsDoesNotReturnAgentsWithMissingConfigDir(t *testing.T) {
	home := t.TempDir()

	// Only codex dir
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	discovered := DiscoverAgents(home)

	// claude-code should NOT be returned since ~/.claude/ doesn't exist
	for _, id := range discovered {
		if id == model.AgentClaudeCode {
			t.Errorf("DiscoverAgents() should not return claude-code when ~/.claude/ is absent, got %v", discovered)
		}
	}

	// codex SHOULD be returned
	if !slices.Contains(discovered, model.AgentCodex) {
		t.Errorf("DiscoverAgents() expected codex when ~/.codex/ exists, got %v", discovered)
	}
}

func TestDiscoverAgentsMultiplePresent(t *testing.T) {
	home := t.TempDir()

	// Create both Claude and Codex config dirs
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	discovered := DiscoverAgents(home)

	if len(discovered) < 2 {
		t.Errorf("DiscoverAgents() expected at least 2 agents when both config dirs exist, got %v", discovered)
	}
}

// TestDiscoverAgentsDelegatesCanonicalDiscovery proves that DiscoverAgents
// derives results from canonical adapter-driven discovery rather than a stale
// hardcoded list. After the Phase 2 rewire, the wrapper must:
//   - return agents whose adapter.GlobalConfigDir exists on disk
//   - not return agents whose config dir is absent
//   - produce the same set as agents.DiscoverInstalled for the same homeDir
func TestDiscoverAgentsDelegatesCanonicalDiscovery(t *testing.T) {
	home := t.TempDir()

	// Create only the codex config dir — a less-common agent that would be
	// absent from a minimal stale hardcoded list if someone forgot to update it.
	// This verifies the wrapper consults the registry, not a frozen snapshot.
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	discovered := DiscoverAgents(home)

	// codex MUST be discovered because its config dir exists.
	found := false
	for _, id := range discovered {
		if id == model.AgentCodex {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("DiscoverAgents() did not return codex even though ~/.codex/ exists; got %v — wrapper must delegate to canonical registry discovery", discovered)
	}

	// No other agents should appear — their dirs don't exist.
	for _, id := range discovered {
		if id != model.AgentCodex {
			t.Errorf("DiscoverAgents() returned unexpected agent %q — no other config dirs were created", id)
		}
	}
}

// ─── Phase 3: componentSyncStep ───────────────────────────────────────────

func TestComponentSyncStepSkipsEngramBinaryInstall(t *testing.T) {
	home := t.TempDir()
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	t.Cleanup(func() {
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
	})

	// Simulate engram NOT on PATH — install logic should NOT be triggered.
	cmdLookPath = func(name string) (string, error) {
		return "", os.ErrNotExist
	}

	var commandsCalled []string
	runCommand = func(name string, args ...string) error {
		commandsCalled = append(commandsCalled, name+" "+strings.Join(args, " "))
		return nil
	}

	step := componentSyncStep{
		id:        "sync:engram",
		component: model.ComponentEngram,
		homeDir:   home,
		agents:    []model.AgentID{model.AgentCodex},
		selection: model.Selection{},
	}

	if err := step.Run(); err != nil {
		t.Fatalf("componentSyncStep.Run() error = %v", err)
	}

	// No binary install or engram setup commands should have been recorded.
	for _, cmd := range commandsCalled {
		if strings.Contains(cmd, "go install") {
			t.Errorf("componentSyncStep must not run binary install, got command: %s", cmd)
		}
		if strings.Contains(cmd, "engram setup") {
			t.Errorf("componentSyncStep must not run engram setup, got command: %s", cmd)
		}
	}
}

// TestComponentSyncStepPreservesSlimEngramProtocol pins bug #1824: install
// threads the detected engram binary version into engram.InjectOptions.Version
// (internal/cli/run.go), so a verified Claude Code install renders the SLIM
// engram-protocol CLAUDE.md section. The sync path built InjectOptions WITHOUT
// Version, so every `brunnr-code sync` silently re-inflated the slim section
// back to the full (~6.7 KB) one. Sync must detect the version identically
// (resolveEngramVersion) and keep the installed slim section byte-identical.
func TestComponentSyncStepPreservesSlimEngramProtocol(t *testing.T) {
	home := t.TempDir()

	const installedEngramVersion = "engram 1.18.0" // above the v1.4.0 slim floor
	restoreVerify := verifyEngramVersion
	t.Cleanup(func() { verifyEngramVersion = restoreVerify })
	verifyEngramVersion = func() (string, error) { return installedEngramVersion, nil }

	adapter, err := agents.NewAdapter(model.AgentClaudeCode)
	if err != nil {
		t.Fatalf("NewAdapter(claude-code) error = %v", err)
	}

	// Install path: Version is threaded, so the SLIM section is written.
	if _, err := engram.InjectWithOptions(home, adapter, engram.InjectOptions{Version: installedEngramVersion}); err != nil {
		t.Fatalf("install-path InjectWithOptions error = %v", err)
	}
	claudeMD := filepath.Join(home, ".claude", "CLAUDE.md")
	registryPath := filepath.Join(home, ".claude.json")
	installed, err := os.ReadFile(claudeMD)
	if err != nil {
		t.Fatalf("ReadFile(CLAUDE.md) after install error = %v", err)
	}
	if strings.Contains(string(installed), "needs_review") || !strings.Contains(string(installed), "SessionStart hook") {
		t.Fatalf("precondition failed: install did not write the SLIM section:\n%s", installed)
	}
	installedRegistry, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("ReadFile(user registry) after install error = %v", err)
	}

	// Sync over the installed state must preserve the slim section.
	var changed []string
	step := componentSyncStep{
		id:           "sync:engram",
		component:    model.ComponentEngram,
		homeDir:      home,
		agents:       []model.AgentID{model.AgentClaudeCode},
		changedFiles: &changed,
	}
	if err := step.Run(); err != nil {
		t.Fatalf("componentSyncStep.Run() error = %v", err)
	}
	afterSync, err := os.ReadFile(claudeMD)
	if err != nil {
		t.Fatalf("ReadFile(CLAUDE.md) after sync error = %v", err)
	}
	if strings.Contains(string(afterSync), "needs_review") {
		t.Fatalf("sync re-inflated the SLIM engram-protocol section back to FULL (bug #1824):\n%s", afterSync)
	}
	if !bytes.Equal(installed, afterSync) {
		t.Fatalf("sync must leave the installed CLAUDE.md byte-identical\ninstalled:\n%s\nafter sync:\n%s", installed, afterSync)
	}
	afterSyncRegistry, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("ReadFile(user registry) after sync error = %v", err)
	}
	if !bytes.Equal(installedRegistry, afterSyncRegistry) {
		t.Fatal("sync must leave Claude's user registry byte-identical")
	}

	// Idempotency guard: a second sync must not report the section changed.
	changed = nil
	if err := step.Run(); err != nil {
		t.Fatalf("second componentSyncStep.Run() error = %v", err)
	}
	afterSecondSync, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("ReadFile(user registry) after second sync error = %v", err)
	}
	if !bytes.Equal(installedRegistry, afterSecondSync) {
		t.Fatal("repeated sync must leave Claude's user registry byte-identical")
	}
	for _, f := range changed {
		if f == claudeMD {
			t.Errorf("second sync reported CLAUDE.md as changed; want no-op on the engram-protocol section")
		}
	}
	afterSecond, err := os.ReadFile(claudeMD)
	if err != nil {
		t.Fatalf("ReadFile(CLAUDE.md) after second sync error = %v", err)
	}
	if !bytes.Equal(installed, afterSecond) {
		t.Fatalf("second sync must remain byte-identical to the installed CLAUDE.md")
	}
}

func TestComponentSyncStepCodexRuntimeGate(t *testing.T) {
	tests := []struct {
		name       string
		version    string
		commandErr error
		wantErr    bool
	}{
		{name: "missing CLI writes shared config", commandErr: exec.ErrNotFound},
		{name: "old runtime is rejected", version: "codex-cli 0.143.9", wantErr: true},
		{name: "minimum runtime writes shared config", version: "codex-cli 0.144.0"},
		{name: "new runtime writes shared config", version: "codex-cli 0.145.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restore := codex.SetRuntimeVersionCommandForTest(tt.version, tt.commandErr)
			t.Cleanup(restore)
			home := t.TempDir()
			codexDir := filepath.Join(home, ".codex")
			if err := os.MkdirAll(codexDir, 0o755); err != nil {
				t.Fatal(err)
			}

			step := componentSyncStep{component: model.ComponentEngram, homeDir: home, agents: []model.AgentID{model.AgentCodex}}
			err := step.Run()
			if (err != nil) != tt.wantErr {
				t.Fatalf("componentSyncStep.Run() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				config, readErr := os.ReadFile(filepath.Join(codexDir, "config.toml"))
				if readErr != nil || !strings.Contains(string(config), "[mcp_servers.engram]") {
					t.Fatalf("shared Codex config was not written: got=%q error=%v", config, readErr)
				}
			}
		})
	}
}

func TestSyncBackupTargetsIncludeClaudeEngramLegacyMigrationSource(t *testing.T) {
	home := t.TempDir()
	selection := model.Selection{Agents: []model.AgentID{model.AgentClaudeCode}, Components: []model.ComponentID{model.ComponentEngram}}
	targets, err := syncBackupTargetsScoped(home, "", ScopeGlobal, selection, resolveAdapters(selection.Agents))
	if err != nil {
		t.Fatalf("syncBackupTargets() error = %v", err)
	}
	want := filepath.Join(home, ".claude", "mcp", "engram.json")
	if !containsPath(targets, want) {
		t.Fatalf("sync backup targets missing legacy migration source %q: %v", want, targets)
	}
}

func TestSyncBackupTargetsIncludeCodexEngramInstructionFiles(t *testing.T) {
	home := t.TempDir()
	selection := model.Selection{Agents: []model.AgentID{model.AgentCodex}, Components: []model.ComponentID{model.ComponentEngram}}
	targets, err := syncBackupTargetsScoped(home, "", ScopeGlobal, selection, resolveAdapters(selection.Agents))
	if err != nil {
		t.Fatalf("syncBackupTargets() error = %v", err)
	}
	for _, name := range []string{"engram-instructions.md", "engram-compact-prompt.md"} {
		want := filepath.Join(home, ".codex", name)
		if !containsPath(targets, want) {
			t.Fatalf("sync backup targets missing Codex Engram instruction %q: %v", want, targets)
		}
	}
}

type failingSyncStep struct{}

func (failingSyncStep) ID() string { return "sync:test:fail-after-engram" }
func (failingSyncStep) Run() error { return errors.New("forced failure after Engram migration") }

func TestRunSyncRollbackRestoresClaudeEngramMigrationSource(t *testing.T) {
	home := t.TempDir()
	registryPath := filepath.Join(home, ".claude.json")
	legacyPath := filepath.Join(home, ".claude", "mcp", "engram.json")
	registryBefore := []byte(`{"oauthAccount":{"emailAddress":"user@example.com"},"mcpServers":{"codegraph":{"command":"codegraph"}}}`)
	legacyBefore := []byte("{\n  \"command\": \"/usr/local/bin/engram\",\n  \"args\": [\"mcp\", \"--tools=agent\"]\n}\n")
	if err := os.WriteFile(registryPath, registryBefore, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, legacyBefore, 0o604); err != nil {
		t.Fatal(err)
	}
	registryInfo, _ := os.Stat(registryPath)
	legacyInfo, _ := os.Stat(legacyPath)
	promptPath := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.WriteFile(promptPath, []byte("original prompt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	restoreBackupHome := backup.UserHomeDirFn
	backup.UserHomeDirFn = func() (string, error) { return home, nil }
	t.Cleanup(func() { backup.UserHomeDirFn = restoreBackupHome })

	selection := model.Selection{
		Agents:     []model.AgentID{model.AgentClaudeCode},
		Components: []model.ComponentID{model.ComponentEngram},
	}
	for attempt := 1; attempt <= 2; attempt++ {
		rt, err := newSyncRuntimeWithScope(home, selection, ScopeGlobal)
		if err != nil {
			t.Fatal(err)
		}
		plan := rt.stagePlan()
		plan.Apply = append(plan.Apply, failingSyncStep{})
		result := pipeline.NewOrchestrator(pipeline.DefaultRollbackPolicy()).Execute(plan)
		if result.Err == nil {
			t.Fatalf("sync transaction attempt %d error = nil; want forced post-migration failure", attempt)
		}
		if len(result.Apply.Steps) < 3 || result.Apply.Steps[1].StepID != "sync:component:engram" || result.Apply.Steps[1].Status != pipeline.StepStatusSucceeded {
			t.Fatalf("attempt %d Engram migration did not complete before failure: error=%v steps=%#v", attempt, result.Err, result.Apply.Steps)
		}
		if !result.Rollback.Success {
			t.Fatalf("sync rollback attempt %d failed: error=%v steps=%#v", attempt, result.Rollback.Err, result.Rollback.Steps)
		}
		if rt.state.rollbackSnapshotDir != "" {
			t.Fatalf("sync rollback attempt %d left transaction snapshot %q", attempt, rt.state.rollbackSnapshotDir)
		}
		for _, file := range []struct {
			path string
			data []byte
			mode os.FileMode
		}{{registryPath, registryBefore, registryInfo.Mode().Perm()}, {legacyPath, legacyBefore, legacyInfo.Mode().Perm()}} {
			got, err := os.ReadFile(file.path)
			if err != nil || !bytes.Equal(got, file.data) {
				t.Fatalf("rollback attempt %d did not restore %q bytes: got=%q error=%v", attempt, file.path, got, err)
			}
			if runtime.GOOS != "windows" {
				info, err := os.Stat(file.path)
				if err != nil || info.Mode().Perm() != file.mode {
					t.Fatalf("rollback attempt %d did not restore %q mode: got=%v error=%v want=%v", attempt, file.path, info, err, file.mode)
				}
			}
		}
	}
	backups, err := os.ReadDir(filepath.Join(home, ".brunnr-code", "backups"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("persistent backup count = %d, want 1 after duplicate transaction", len(backups))
	}
}

func TestRunSyncRollbackRestoresCodexEngramInstructionFiles(t *testing.T) {
	t.Cleanup(codex.SetRuntimeVersionCommandForTest("", exec.ErrNotFound))

	for _, tc := range []struct {
		name   string
		exists bool
	}{
		{name: "restores existing instruction contents", exists: true},
		{name: "removes newly created instruction files", exists: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			codexDir := filepath.Join(home, ".codex")
			if err := os.MkdirAll(codexDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte("custom = true\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			setSyncTestHome(t, home)

			instructionFiles := map[string][]byte{
				"engram-instructions.md":   []byte("original instructions\n"),
				"engram-compact-prompt.md": []byte("original compact prompt\n"),
			}
			if tc.exists {
				for name, before := range instructionFiles {
					if err := os.WriteFile(filepath.Join(codexDir, name), before, 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}

			selection := model.Selection{
				Agents:     []model.AgentID{model.AgentCodex},
				Components: []model.ComponentID{model.ComponentEngram},
			}
			rt, err := newSyncRuntimeWithScope(home, selection, ScopeGlobal)
			if err != nil {
				t.Fatal(err)
			}
			plan := rt.stagePlan()
			backupStep, ok := plan.Prepare[0].(prepareBackupStep)
			if !ok {
				t.Fatalf("prepare step = %T, want prepareBackupStep", plan.Prepare[0])
			}
			backupTargets := backupStep.targets[:0]
			for _, target := range backupStep.targets {
				rel, err := filepath.Rel(home, target)
				if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					backupTargets = append(backupTargets, target)
				}
			}
			backupStep.targets = backupTargets
			plan.Prepare[0] = backupStep
			plan.Apply = append(plan.Apply[:2], failingSyncStep{})
			result := pipeline.NewOrchestrator(pipeline.DefaultRollbackPolicy()).Execute(plan)
			if result.Err == nil || !result.Rollback.Success {
				t.Fatalf("sync rollback success=%t error=%v rollback error=%v", result.Rollback.Success, result.Err, result.Rollback.Err)
			}

			for name, before := range instructionFiles {
				path := filepath.Join(codexDir, name)
				if tc.exists {
					got, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(got, before) {
						t.Fatalf("rollback did not restore %q: got=%q error=%v", path, got, err)
					}
					continue
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("rollback did not remove newly created %q: stat error=%v", path, err)
				}
			}
		})
	}
}

func TestRestoreSyncFilesNeverWidensZeroMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exact zero-mode restoration is a POSIX permission contract")
	}
	tests := []struct {
		name  string
		setup func(t *testing.T) (string, syncFileSnapshot)
	}{
		{
			name: "regular file",
			setup: func(t *testing.T) (string, syncFileSnapshot) {
				path := filepath.Join(t.TempDir(), "managed.json")
				mustWriteFile(t, path, []byte("changed"))
				return path, syncFileSnapshot{exists: true, data: []byte("original"), mode: 0}
			},
		},
		{
			name: "symlink chain target",
			setup: func(t *testing.T) (string, syncFileSnapshot) {
				root := t.TempDir()
				targetPath := filepath.Join(root, "versions", "managed.json")
				innerPath := filepath.Join(root, "current.json")
				path := filepath.Join(root, "config", "managed.json")
				mustWriteFile(t, targetPath, []byte("changed"))
				if err := os.Symlink(targetPath, innerPath); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(innerPath, path); err != nil {
					t.Fatal(err)
				}
				return path, syncFileSnapshot{exists: true, data: []byte("original"), mode: 0, symlink: true, linkTarget: innerPath, targetPath: targetPath, targetExists: true}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, snapshot := tt.setup(t)
			originalWriter := writeSyncFileAtomic
			t.Cleanup(func() { writeSyncFileAtomic = originalWriter })
			writeSyncFileAtomic = func(path string, content []byte, mode os.FileMode) (filemerge.WriteResult, error) {
				if mode != 0o600 {
					t.Fatalf("atomic restore mode = %o, want restrictive 0600", mode)
				}
				result, err := originalWriter(path, content, mode)
				if err == nil {
					info, statErr := os.Stat(path)
					if statErr != nil {
						t.Fatal(statErr)
					}
					if got := info.Mode().Perm(); got != 0o600 {
						t.Fatalf("mode immediately after atomic replace = %o, want 0600", got)
					}
				}
				return result, err
			}

			if err := restoreSyncFiles(map[string]syncFileSnapshot{path: snapshot}); err != nil {
				t.Fatalf("restoreSyncFiles() error = %v", err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != 0 {
				t.Fatalf("restored mode = %o, want exact 000", got)
			}
		})
	}
}

func TestCodeGraphGuidanceSyncStepRepairsCodexConfigOnlyGuidance(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	agentsPath := filepath.Join(home, ".codex", "AGENTS.md")
	mustWriteFile(t, configPath, []byte(strings.Join([]string{
		`[mcp_servers.codegraph]`,
		`command = "codegraph"`,
		`args = ["serve", "--mcp"]`,
	}, "\n")))

	restoreLookPath := cmdLookPath
	t.Cleanup(func() { cmdLookPath = restoreLookPath })
	cmdLookPath = func(name string) (string, error) {
		if name != "codegraph" {
			return "", os.ErrNotExist
		}
		return "/bin/codegraph", nil
	}

	var changed []string
	step := codeGraphGuidanceSyncStep{
		id:           "sync:codegraph-guidance",
		homeDir:      home,
		changedFiles: &changed,
	}
	if err := step.Run(); err != nil {
		t.Fatalf("codeGraphGuidanceSyncStep.Run() error = %v", err)
	}

	body, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", agentsPath, err)
	}
	text := string(body)
	for _, want := range []string{"<!-- brunnr-code:codegraph-guidance -->", "immediately run `brunnr-code codegraph init --cwd <project-root>`"} {
		if !strings.Contains(text, want) {
			t.Fatalf("Codex AGENTS.md missing managed CodeGraph guidance %q:\n%s", want, text)
		}
	}
	if !reflect.DeepEqual(changed, []string{agentsPath}) {
		t.Fatalf("changed files = %#v, want %#v", changed, []string{agentsPath})
	}
}

func hasStepID(steps []pipeline.Step, id string) bool {
	for _, step := range steps {
		if step.ID() == id {
			return true
		}
	}
	return false
}

func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

// ─── Phase 4: RunSync integration tests ───────────────────────────────────

func TestRunSyncDoesNotInvokeEngramSetup(t *testing.T) {
	home := t.TempDir()
	restoreHome := osUserHomeDir
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	t.Cleanup(func() {
		osUserHomeDir = restoreHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
	})

	osUserHomeDir = func() (string, error) { return home, nil }
	cmdLookPath = func(name string) (string, error) { return "/usr/local/bin/" + name, nil }

	var commandsCalled []string
	runCommand = func(name string, args ...string) error {
		commandsCalled = append(commandsCalled, name+" "+strings.Join(args, " "))
		return nil
	}

	_, err := RunSync([]string{"--agents", "claude-code"})
	if err != nil {
		t.Fatalf("RunSync() error = %v", err)
	}

	for _, cmd := range commandsCalled {
		if strings.Contains(cmd, "engram setup") {
			t.Errorf("RunSync must NOT invoke engram setup, got command: %s", cmd)
		}
	}
}

func TestRunSyncDoesNotInstallBinaries(t *testing.T) {
	home := t.TempDir()
	restoreHome := osUserHomeDir
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	t.Cleanup(func() {
		osUserHomeDir = restoreHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
	})

	osUserHomeDir = func() (string, error) { return home, nil }
	// Simulate all binaries as missing.
	cmdLookPath = func(name string) (string, error) {
		return "", os.ErrNotExist
	}

	var commandsCalled []string
	runCommand = func(name string, args ...string) error {
		commandsCalled = append(commandsCalled, name+" "+strings.Join(args, " "))
		return nil
	}

	_, err := RunSync([]string{"--agents", "claude-code"})
	if err != nil {
		t.Fatalf("RunSync() error = %v", err)
	}

	// No binary installation commands.
	for _, cmd := range commandsCalled {
		if strings.Contains(cmd, "go install") ||
			strings.Contains(cmd, "git clone") || strings.Contains(cmd, "npm install") {
			t.Errorf("RunSync must NOT install binaries, got command: %s", cmd)
		}
	}
}

func TestRunSyncPreservesUnmanagedAdjacentFiles(t *testing.T) {
	home := t.TempDir()

	// Create user-owned config file adjacent to managed overlay.
	userConfigDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(userConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	userConfigPath := filepath.Join(userConfigDir, "my-custom-config.json")
	const userContent = `{"my": "custom"}`
	if err := os.WriteFile(userConfigPath, []byte(userContent), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

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

	_, err := RunSync([]string{"--agents", "claude-code"})
	if err != nil {
		t.Fatalf("RunSync() error = %v", err)
	}

	// User's custom file must be byte-for-byte unchanged.
	after, err := os.ReadFile(userConfigPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(after) != userContent {
		t.Errorf("user config modified by sync: got %q, want %q", string(after), userContent)
	}
}

func TestRunSyncDryRunDoesNotWriteFiles(t *testing.T) {
	home := t.TempDir()
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

	result, err := RunSync([]string{"--agents", "claude-code", "--dry-run"})
	if err != nil {
		t.Fatalf("RunSync() error = %v", err)
	}

	if !result.DryRun {
		t.Fatalf("DryRun = false, want true")
	}

	if len(result.Execution.Apply.Steps) != 0 || len(result.Execution.Prepare.Steps) != 0 {
		t.Fatalf("execution should be empty in dry-run")
	}

	// No AGENTS.md should have been created.
	agentsMDPath := filepath.Join(home, ".claude", "CLAUDE.md")
	if _, err := os.Stat(agentsMDPath); err == nil {
		t.Errorf("dry-run should NOT create files, but %q was created", agentsMDPath)
	}
}

func TestRunSyncIsIdempotent(t *testing.T) {
	home := t.TempDir()
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

	args := []string{"--agents", "claude-code"}

	// Run 1
	result1, err := RunSync(args)
	if err != nil {
		t.Fatalf("RunSync() run 1 error = %v", err)
	}
	if !result1.Verify.Ready {
		t.Fatalf("run 1: Verify.Ready = false")
	}

	claudeMDPath := filepath.Join(home, ".claude", "CLAUDE.md")
	contentAfterRun1, err := os.ReadFile(claudeMDPath)
	if err != nil {
		t.Fatalf("ReadFile() run 1 error = %v", err)
	}

	// Run 2
	result2, err := RunSync(args)
	if err != nil {
		t.Fatalf("RunSync() run 2 error = %v", err)
	}
	if !result2.Verify.Ready {
		t.Fatalf("run 2: Verify.Ready = false")
	}

	contentAfterRun2, err := os.ReadFile(claudeMDPath)
	if err != nil {
		t.Fatalf("ReadFile() run 2 error = %v", err)
	}

	if string(contentAfterRun1) != string(contentAfterRun2) {
		t.Errorf("CLAUDE.md changed between sync run 1 and run 2 (idempotency violation):\n--- run1 ---\n%s\n--- run2 ---\n%s",
			contentAfterRun1, contentAfterRun2)
	}
}

// ─── Gap 1: No-op / No managed assets ─────────────────────────────────────

// TestRunSyncNoOpWhenNoAgentsDiscovered verifies the spec scenario:
// "No managed assets to sync — system completes without modifying unrelated
// files and reports that no managed sync actions were needed."
func TestRunSyncNoOpWhenNoAgentsDiscovered(t *testing.T) {
	home := t.TempDir()
	restoreHome := osUserHomeDir
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	t.Cleanup(func() {
		osUserHomeDir = restoreHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
	})

	// Empty home — no agent config dirs exist, so DiscoverAgents returns nil.
	osUserHomeDir = func() (string, error) { return home, nil }
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }

	// No --agents flag and no config dirs — auto-discovery yields nothing.
	result, err := RunSync([]string{})
	if err != nil {
		t.Fatalf("RunSync() no-op error = %v", err)
	}

	// No agents discovered.
	if len(result.Agents) != 0 {
		t.Errorf("expected no agents discovered, got %v", result.Agents)
	}

	// Must be marked as no-op.
	if !result.NoOp {
		t.Errorf("SyncResult.NoOp = false, want true when no agents are discovered")
	}

	// Must produce a human-readable message saying no managed sync actions were needed.
	report := RenderSyncReport(result)
	if !containsAny(report, "no managed", "no sync", "nothing to sync", "0 actions") {
		t.Errorf("RenderSyncReport() should indicate no managed actions; got:\n%s", report)
	}
}

// ─── Gap 2: Report managed actions executed ────────────────────────────────

func TestRenderSyncReportIncludesManagedActions(t *testing.T) {
	home := t.TempDir()
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
	cmdLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }

	result, err := RunSync([]string{"--agents", "claude-code"})
	if err != nil {
		t.Fatalf("RunSync() error = %v", err)
	}

	report := RenderSyncReport(result)

	// Must mention the sync was executed (not just verification).
	if !containsAny(report, "synced", "sync", "managed", "component", "agent") {
		t.Errorf("RenderSyncReport() should mention managed actions; got:\n%s", report)
	}

	// Must list the agents involved.
	if !containsAny(report, "claude-code") {
		t.Errorf("RenderSyncReport() should list agents; got:\n%s", report)
	}
}

// ─── Gap 3: Unmanaged-lookalike-file exclusion ─────────────────────────────

// ─── Verify Gaps ──────────────────────────────────────────────────────────

// TestRunSyncNoOpWhenAssetsAlreadyCurrent verifies the spec scenario:
// "No managed assets to sync — when all managed assets are already current
// (second sync on an already-synced home), the command reports no-op."
//
// This is distinct from TestRunSyncNoOpWhenNoAgentsDiscovered: agents ARE
// present, but all inject calls write nothing new (WriteFileAtomic is no-op).
func TestRunSyncNoOpWhenAssetsAlreadyCurrent(t *testing.T) {
	home := t.TempDir()
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
	cmdLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }

	args := []string{"--agents", "claude-code"}

	// First sync — writes files, changes > 0.
	result1, err := RunSync(args)
	if err != nil {
		t.Fatalf("RunSync() first run error = %v", err)
	}
	if result1.NoOp {
		t.Fatalf("first sync should NOT be no-op; files were written for the first time")
	}
	if result1.FilesChanged == 0 {
		t.Fatalf("first sync: FilesChanged = 0, expected > 0 (files were written)")
	}

	// Second sync — all assets already current, WriteFileAtomic is a no-op.
	result2, err := RunSync(args)
	if err != nil {
		t.Fatalf("RunSync() second run error = %v", err)
	}

	// Must detect true no-op: agents are present but nothing changed.
	if !result2.NoOp {
		t.Errorf("second sync: SyncResult.NoOp = false, want true (all assets already current)")
	}
	if result2.FilesChanged != 0 {
		t.Errorf("second sync: FilesChanged = %d, want 0 (no files changed)", result2.FilesChanged)
	}

	report := RenderSyncReport(result2)
	if !containsAny(report, "no managed", "no sync", "nothing to sync", "0 actions", "already current", "up to date") {
		t.Errorf("RenderSyncReport() should indicate no changes on second run; got:\n%s", report)
	}
}

// TestSyncActionsExecutedReflectsChangedFiles verifies that "Sync actions
// executed" in the report reflects actual file changes, not step count.
//
// On a fresh home, files are written so the count must be > 0.
// On a second sync, nothing changes so the count must be 0.
func TestSyncActionsExecutedReflectsChangedFiles(t *testing.T) {
	home := t.TempDir()
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
	cmdLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }

	args := []string{"--agents", "claude-code"}

	// First sync: files are new, so FilesChanged > 0.
	result1, err := RunSync(args)
	if err != nil {
		t.Fatalf("RunSync() first run error = %v", err)
	}
	if result1.FilesChanged == 0 {
		t.Errorf("first sync: FilesChanged = 0, want > 0")
	}
	report1 := RenderSyncReport(result1)
	// The report must state how many files were actually changed.
	if !containsAny(report1, "files changed", "file changed", "sync actions executed") {
		t.Errorf("first sync report should state changed-file count; got:\n%s", report1)
	}

	// Second sync: nothing new — FilesChanged must be 0.
	result2, err := RunSync(args)
	if err != nil {
		t.Fatalf("RunSync() second run error = %v", err)
	}
	if result2.FilesChanged != 0 {
		t.Errorf("second sync: FilesChanged = %d, want 0 (idempotent)", result2.FilesChanged)
	}
}

func containsAny(s string, subs ...string) bool {
	lower := strings.ToLower(s)
	for _, sub := range subs {
		if strings.Contains(lower, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

// ─── T21: RunSyncWithSelection ─────────────────────────────────────────────

// TestRunSyncWithSelection_NoAgentsIsNoOp verifies that providing an empty
// agent list returns a no-op result without error.
func TestRunSyncWithSelection_NoAgentsIsNoOp(t *testing.T) {
	home := t.TempDir()

	sel := model.Selection{
		Agents:     nil,
		Components: []model.ComponentID{model.ComponentEngram},
	}

	result, err := RunSyncWithSelection(home, sel)
	if err != nil {
		t.Fatalf("RunSyncWithSelection() with no agents: error = %v", err)
	}
	if !result.NoOp {
		t.Errorf("RunSyncWithSelection() with no agents: NoOp = false, want true")
	}
}

// TestRunSyncWithSelection_WritesExpectedFiles verifies that the function
// creates managed asset files for the provided agents and components.
func TestRunSyncWithSelection_WritesExpectedFiles(t *testing.T) {
	home := t.TempDir()
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	t.Cleanup(func() {
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
	})

	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = func(name string) (string, error) { return "/usr/local/bin/" + name, nil }

	sel := model.Selection{
		Agents:     []model.AgentID{model.AgentCodex},
		Components: []model.ComponentID{model.ComponentEngram, model.ComponentSkills},
	}

	result, err := RunSyncWithSelection(home, sel)
	if err != nil {
		t.Fatalf("RunSyncWithSelection() error = %v", err)
	}

	if !result.Verify.Ready {
		t.Fatalf("Verify.Ready = false, report = %#v", result.Verify)
	}

	// Routing guidance belongs to the managed Codex prompt. No retired
	// commands are recreated.
	promptPath := filepath.Join(home, ".codex", "AGENTS.md")
	if !containsPath(result.ChangedFiles, promptPath) {
		t.Fatalf("ChangedFiles = %v, want managed Codex prompt", result.ChangedFiles)
	}
	prompt, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("read synced Codex prompt: %v", err)
	}
	if !strings.Contains(string(prompt), "Organic Driven Development") || !strings.Contains(string(prompt), "Receipt-driven development") {
		t.Fatal("synced Codex orchestrator lost ODD or RDD routing")
	}
	if strings.Contains(string(prompt), "--agent "+string(model.AgentClaudeCode)) {
		t.Fatal("synced Codex orchestrator declares Claude Code identity")
	}
}

// TestRunSyncWithSelection_FilesChangedOnFreshHome verifies that syncing a
// fresh home dir results in FilesChanged > 0.
func TestRunSyncWithSelection_FilesChangedOnFreshHome(t *testing.T) {
	home := t.TempDir()
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	t.Cleanup(func() {
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
	})

	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = func(name string) (string, error) { return "/usr/local/bin/" + name, nil }

	sel := model.Selection{
		Agents:     []model.AgentID{model.AgentCodex},
		Components: []model.ComponentID{model.ComponentEngram, model.ComponentSkills},
	}

	result, err := RunSyncWithSelection(home, sel)
	if err != nil {
		t.Fatalf("RunSyncWithSelection() error = %v", err)
	}

	if result.FilesChanged == 0 {
		t.Errorf("RunSyncWithSelection() on fresh home: FilesChanged = 0, want > 0")
	}
}

// TestRunSyncWithSelection_SelectionAgentsForwarded verifies that the agents in
// the selection are reflected in the result.
func TestRunSyncWithSelection_SelectionAgentsForwarded(t *testing.T) {
	home := t.TempDir()
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	t.Cleanup(func() {
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
	})

	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = func(name string) (string, error) { return "/usr/local/bin/" + name, nil }

	sel := model.Selection{
		Agents:     []model.AgentID{model.AgentCodex},
		Components: []model.ComponentID{model.ComponentEngram, model.ComponentSkills},
	}

	result, err := RunSyncWithSelection(home, sel)
	if err != nil {
		t.Fatalf("RunSyncWithSelection() error = %v", err)
	}

	if len(result.Agents) == 0 {
		t.Errorf("RunSyncWithSelection() result.Agents is empty, want agents forwarded")
	}

	if !slices.Contains(result.Agents, model.AgentCodex) {
		t.Errorf("RunSyncWithSelection() result.Agents should contain codex; got %v", result.Agents)
	}
}

// ─── State-aware DiscoverAgents ────────────────────────────────────────────

// TestDiscoverAgentsUsesStateFileWhenPresent verifies that DiscoverAgents
// returns only the agents recorded in state.json when the file exists and is
// non-empty, ignoring any agent config dirs that happen to be on disk.
//
// This covers issue #107: a user who installed only one agent should not have
// another injected just because its config dir exists.
func TestDiscoverAgentsUsesStateFileWhenPresent(t *testing.T) {
	home := t.TempDir()

	// Write state recording only codex — even though we also create the
	// claude-code config dir to simulate it being installed on disk.
	if err := state.Write(home, state.InstallState{InstalledAgents: []string{"codex"}}); err != nil {
		t.Fatalf("state.Write() error = %v", err)
	}

	// Create the claude-code config dir — FS-discovery would pick this up.
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	discovered := DiscoverAgents(home)

	// Must return exactly the persisted selection: only codex.
	want := []model.AgentID{model.AgentCodex}
	if !reflect.DeepEqual(discovered, want) {
		t.Errorf("DiscoverAgents() with state = %v, want %v", discovered, want)
	}
}

// TestDiscoverAgentsFallsBackToFSDiscoveryWhenStateMissing verifies that
// DiscoverAgents falls back to filesystem discovery when state.json is absent.
// This is the backward-compat path for users who installed before state
// persistence was added.
func TestDiscoverAgentsFallsBackToFSDiscoveryWhenStateMissing(t *testing.T) {
	home := t.TempDir()
	// No state.Write — state.json does not exist.

	// Create the claude-code config dir.
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	discovered := DiscoverAgents(home)

	// FS discovery must return claude-code since ~/.claude/ exists.
	found := false
	for _, id := range discovered {
		if id == model.AgentClaudeCode {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("DiscoverAgents() fallback did not return claude-code; got %v", discovered)
	}
}

// TestDiscoverAgentsFallsBackToFSDiscoveryWhenStateEmpty verifies that
// DiscoverAgents falls back to filesystem discovery when state.json exists but
// contains an empty agent list — treating it the same as absent.
func TestDiscoverAgentsFallsBackToFSDiscoveryWhenStateEmpty(t *testing.T) {
	home := t.TempDir()

	// Write state with zero agents.
	if err := state.Write(home, state.InstallState{InstalledAgents: []string{}}); err != nil {
		t.Fatalf("state.Write() error = %v", err)
	}

	// Create the claude-code config dir.
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	discovered := DiscoverAgents(home)

	// FS discovery must pick up claude-code from disk.
	found := false
	for _, id := range discovered {
		if id == model.AgentClaudeCode {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("DiscoverAgents() empty-state fallback did not return claude-code; got %v", discovered)
	}
}

// TestDiscoverAgentsStateMultipleAgents verifies that multiple agents persisted
// in state.json are all returned, in order.
func TestDiscoverAgentsStateMultipleAgents(t *testing.T) {
	home := t.TempDir()

	agents := []string{"claude-code", "codex"}
	if err := state.Write(home, state.InstallState{InstalledAgents: agents}); err != nil {
		t.Fatalf("state.Write() error = %v", err)
	}

	discovered := DiscoverAgents(home)

	want := []model.AgentID{
		model.AgentClaudeCode,
		model.AgentCodex,
	}
	if !reflect.DeepEqual(discovered, want) {
		t.Errorf("DiscoverAgents() multi-state = %v, want %v", discovered, want)
	}
}

// ─── Task 5: --strict-tdd flag ───────────────────────────────────────────────

// TestParseSyncFlagsStrictTDD verifies the retired flag cannot change policy.
func TestParseSyncFlagsStrictTDD(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantError bool
	}{
		{name: "absent", args: []string{}},
		{name: "explicit true", args: []string{"--strict-tdd"}, wantError: true},
		{name: "explicit false", args: []string{"--strict-tdd=false"}, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseSyncFlags(tt.args)
			if tt.wantError != (err != nil) || (tt.wantError && !strings.Contains(err.Error(), "applicable test-first")) {
				t.Fatalf("ParseSyncFlags() error = %v, want retired flag guidance = %v", err, tt.wantError)
			}
		})
	}
}

// ─── Phase 5: Profile CLI flags ───────────────────────────────────────────────

// Retired profile flags must not be accepted by the public sync parser.
func TestParseSyncFlagsProfileSingleModel(t *testing.T) {
	_, err := ParseSyncFlags([]string{"--profile", "cheap:anthropic/claude-haiku-3-5-20241022"})
	if err == nil || !strings.Contains(err.Error(), "profile") {
		t.Fatalf("retired --profile accepted: %v", err)
	}
}

func TestParseSyncFlagsProfileMultiple(t *testing.T) {
	_, err := ParseSyncFlags([]string{"--profile", "cheap:anthropic/claude-haiku-3-5-20241022", "--profile", "premium:anthropic/claude-opus-4-5"})
	if err == nil || !strings.Contains(err.Error(), "profile") {
		t.Fatalf("retired repeated --profile accepted: %v", err)
	}
}

func TestParseSyncFlagsProfilePhaseAssignment(t *testing.T) {
	_, err := ParseSyncFlags([]string{"--profile-phase", "cheap:odd-worker:anthropic/claude-sonnet-4-20250514"})
	if err == nil || !strings.Contains(err.Error(), "profile-phase") {
		t.Fatalf("retired --profile-phase accepted: %v", err)
	}
}

func TestParseSyncFlagsProfilePhaseJDAssignments(t *testing.T) {
	_, err := ParseSyncFlags([]string{"--profile-phase", "review:jd-judge-a:anthropic/claude-opus-4-5"})
	if err == nil || !strings.Contains(err.Error(), "profile-phase") {
		t.Fatalf("retired JD profile assignment accepted: %v", err)
	}
}

// TestParseSyncFlagsProfileInvalidFormatReturnsError verifies that --profile
// with a missing colon separator returns an error.
func TestParseSyncFlagsProfileInvalidFormatReturnsError(t *testing.T) {
	_, err := ParseSyncFlags([]string{"--profile", "invalid"})
	if err == nil {
		t.Fatalf("expected error for --profile 'invalid' (missing colon), got nil")
	}
}

// TestParseSyncFlagsProfileEmptyNameReturnsError verifies that --profile with
// an empty name (:model) returns an error.
func TestParseSyncFlagsProfileEmptyNameReturnsError(t *testing.T) {
	_, err := ParseSyncFlags([]string{"--profile", ":anthropic/claude-haiku-3-5-20241022"})
	if err == nil {
		t.Fatalf("expected error for --profile ':model' (empty name), got nil")
	}
}

// TestParseSyncFlagsProfileReservedNameReturnsError verifies that --profile
// with the reserved name "default" returns an error.
func TestParseSyncFlagsProfileReservedNameReturnsError(t *testing.T) {
	_, err := ParseSyncFlags([]string{"--profile", "default:anthropic/claude-haiku-3-5-20241022"})
	if err == nil {
		t.Fatalf("expected error for --profile 'default:model' (reserved name), got nil")
	}
}

// TestParseSyncFlagsProfilePhaseUnknownPhaseReturnsError verifies that
// --profile-phase with an unknown phase name returns an error.
func TestParseSyncFlagsProfilePhaseUnknownPhaseReturnsError(t *testing.T) {
	_, err := ParseSyncFlags([]string{
		"--profile", "cheap:anthropic/claude-haiku-3-5-20241022",
		"--profile-phase", "cheap:unknown-phase:anthropic/claude-haiku-3-5-20241022",
	})
	if err == nil {
		t.Fatalf("expected error for --profile-phase with unknown phase 'unknown-phase', got nil")
	}
}

// ─── Persist model assignments across sync runs ─────────────────────────────

// TestRunSyncLoadsPersistedModelAssignments verifies that when state.json
// contains model assignments and no CLI flags override them, RunSync populates
// the selection with the persisted assignments rather than falling back to the
// "balanced" preset defaults.
func TestRunSyncLoadsPersistedModelAssignments(t *testing.T) {
	home := t.TempDir()
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

	// Pre-seed state.json with model assignments from a previous install.
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	err := state.Write(home, state.InstallState{
		InstalledAgents: []string{"claude-code"},
		ClaudeModelAssignments: map[string]string{
			"orchestrator": "opus",
			"review-risk":  "sonnet",
		},
	})
	if err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	// Run sync WITHOUT --claude-model or --model flags — assignments should
	// come from persisted state.
	result, err := RunSync([]string{"--agents", "claude-code"})
	if err != nil {
		t.Fatalf("RunSync() error = %v", err)
	}

	// Claude assignments must be loaded, excluding the main orchestrator model
	// because Claude Code controls the session model itself.
	if _, exists := result.Selection.ClaudeModelAssignments["orchestrator"]; exists {
		t.Errorf("ClaudeModelAssignments should not load persisted orchestrator model: %v", result.Selection.ClaudeModelAssignments)
	}
	if got := result.Selection.ClaudeModelAssignments["review-risk"]; got != "sonnet" {
		t.Errorf("ClaudeModelAssignments[review-risk] = %q, want %q", got, "sonnet")
	}
}

func TestRunSyncWithNoPersistedAssignmentsDoesNotPanic(t *testing.T) {
	home := t.TempDir()
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

	// State with agents but NO model assignments (pre-feature state files).
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	err := state.Write(home, state.InstallState{
		InstalledAgents: []string{"claude-code"},
	})
	if err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	result, err := RunSync([]string{"--agents", "claude-code"})
	if err != nil {
		t.Fatalf("RunSync() error = %v", err)
	}

	// Should work fine — empty maps, no panic.
	if len(result.Selection.ClaudeModelAssignments) != 0 {
		t.Errorf("expected empty ClaudeModelAssignments, got %v", result.Selection.ClaudeModelAssignments)
	}
}

// ─── Phase 2: Persona-in-sync regression tests ─────────────────────────────

func setSyncTestHome(t *testing.T, home string) {
	t.Helper()
	rOSHome := osUserHomeDir
	rBackup := backup.UserHomeDirFn
	rRun := runCommand
	rLook := cmdLookPath
	t.Cleanup(func() {
		osUserHomeDir = rOSHome
		backup.UserHomeDirFn = rBackup
		runCommand = rRun
		cmdLookPath = rLook
	})
	osUserHomeDir = func() (string, error) { return home, nil }
	backup.UserHomeDirFn = func() (string, error) { return home, nil }
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = func(name string) (string, error) { return "/usr/local/bin/" + name, nil }
}

func TestSyncBackupManifestIncludesCodexHooksJSON(t *testing.T) {
	home := t.TempDir()
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	mustWriteFile(t, hooksPath, []byte("{\"hooks\":{\"SessionStart\":[]}}\n"))
	selection := model.Selection{
		Agents: []model.AgentID{model.AgentCodex},
	}
	targets, err := syncBackupTargetsScoped(home, "", ScopeGlobal, selection, resolveAdapters(selection.Agents))
	if err != nil {
		t.Fatalf("syncBackupTargets() error = %v", err)
	}
	if !containsPath(targets, hooksPath) {
		t.Fatalf("sync backup targets missing %q\ntargets=%v", hooksPath, targets)
	}

	manifest, err := backup.NewSnapshotter().Create(filepath.Join(home, "snapshot"), targets)
	if err != nil {
		t.Fatalf("Snapshotter.Create() error = %v", err)
	}
	for _, entry := range manifest.Entries {
		if entry.OriginalPath == hooksPath {
			if !entry.Existed {
				t.Fatalf("hooks.json manifest entry marked absent: %#v", entry)
			}
			return
		}
	}
	t.Fatalf("snapshot manifest omitted %q: %#v", hooksPath, manifest.Entries)
}

func TestSyncCodexConvergesWithHooksJSON(t *testing.T) {
	home := t.TempDir()
	selection := BuildSyncSelection([]model.AgentID{model.AgentCodex, model.AgentClaudeCode})
	run := func() (int, []string) {
		rt, err := newSyncRuntimeWithScope(home, selection, ScopeGlobal)
		if err != nil {
			t.Fatalf("newSyncRuntimeWithScope() error = %v", err)
		}
		plan := rt.stagePlan()
		before, err := snapshotSyncFiles(rt.managedPaths)
		if err != nil {
			t.Fatalf("snapshotSyncFiles() error = %v", err)
		}
		execution := pipeline.NewOrchestrator(pipeline.DefaultRollbackPolicy()).Execute(plan)
		if execution.Err != nil {
			t.Fatalf("sync stage plan error = %v", execution.Err)
		}
		changed, err := changedSyncFiles(rt.changedFiles, before)
		if err != nil {
			t.Fatalf("changedSyncFiles() error = %v", err)
		}
		return len(changed), changed
	}
	firstFiles, _ := run()
	if firstFiles == 0 {
		t.Fatal("first sync reported no managed changes")
	}
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	firstHooks := readTextFile(t, hooksPath)
	if !strings.Contains(firstHooks, "skill") {
		t.Fatalf("Codex hooks lack skill registry: %s", firstHooks)
	}
	agentPath := filepath.Join(home, ".claude", "agents", "jd-judge-a.md")
	firstAgent := readTextFile(t, agentPath)
	if firstAgent == "" {
		t.Fatal("retained review agent is empty")
	}
	routingPath := filepath.Join(home, ".codex", "AGENTS.md")
	firstRouting := readTextFile(t, routingPath)
	if !strings.Contains(firstRouting, "Organic Driven Development") {
		t.Fatal("ODD routing guidance missing")
	}

	secondFiles, changed := run()
	if secondFiles != 0 || len(changed) != 0 {
		t.Fatalf("second sync changed %d files: %v; want no changes", secondFiles, changed)
	}
	if got := readTextFile(t, hooksPath); got != firstHooks {
		t.Fatalf("hooks.json changed between converged syncs")
	}
	if got := readTextFile(t, agentPath); got != firstAgent {
		t.Fatal("review agent changed between converged syncs")
	}
	if got := readTextFile(t, routingPath); got != firstRouting {
		t.Fatal("ODD routing changed between converged syncs")
	}
}

func TestSyncPersonaPathsDeclareManagedClaudeOutputStyle(t *testing.T) {
	home := t.TempDir()
	reg, _ := agents.NewDefaultRegistry()
	a, _ := reg.Get(model.AgentClaudeCode)

	paths := syncPersonaPathsWithWorkspaceScoped(home, "", ScopeGlobal, model.Selection{}, []agents.Adapter{a})
	for _, want := range []string{
		filepath.Join(home, ".claude", "output-styles", "principal-engineer.md"),
		filepath.Join(home, ".claude", "settings.json"),
	} {
		if !containsPath(paths, want) {
			t.Fatalf("syncPersonaPaths missing %q; got %v", want, paths)
		}
	}
}

// TestPersonaBackupTargetsIncludeLegacyNeutralOutputStyle keeps the retired
// neutral.md output style restorable: persona install and sync remove it, so
// both must snapshot it first.
func TestPersonaBackupTargetsIncludeLegacyNeutralOutputStyle(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(home, ".claude", "output-styles", "neutral.md")
	selection := model.Selection{
		Agents:     []model.AgentID{model.AgentClaudeCode},
		Components: []model.ComponentID{model.ComponentPersona},
	}
	install, err := backupTargets(home, "", ScopeGlobal, selection, planner.ResolvedPlan{
		Agents:            selection.Agents,
		OrderedComponents: selection.Components,
	})
	if err != nil {
		t.Fatalf("backupTargets() error = %v", err)
	}
	sync, err := syncBackupTargetsScoped(home, "", ScopeGlobal, selection, resolveAdapters(selection.Agents))
	if err != nil {
		t.Fatalf("syncBackupTargetsScoped() error = %v", err)
	}
	if !containsPath(install, legacy) || !containsPath(sync, legacy) {
		t.Fatalf("legacy output style %q missing from backup targets\ninstall = %v\nsync = %v", legacy, install, sync)
	}
}

// TestRunSyncRegeneratesPersonaBlockBetweenMarkers verifies the core fix:
// when an old persona block lives between markers, sync replaces it with the
// embedded asset for the current version.
func TestRunSyncRegeneratesPersonaBlockBetweenMarkers(t *testing.T) {
	home := t.TempDir()
	setSyncTestHome(t, home)

	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Write a stale managed persona block — what an older version of brunnr-code
	// would have emitted. The sync must replace this with the v1.26 directive.
	stalePersona := "# pre-existing notes by user\n\n" +
		"<!-- brunnr-code:persona -->\n" +
		"## Skills (Auto-load based on context)\n\nstale 2-row table here.\n" +
		"<!-- /brunnr-code:persona -->\n"
	claudeMD := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.WriteFile(claudeMD, []byte(stalePersona), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := state.Write(home, state.InstallState{
		InstalledAgents: []string{"claude-code"},
	}); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	if _, err := RunSync([]string{"--agents", "claude-code"}); err != nil {
		t.Fatalf("RunSync() error = %v", err)
	}

	body, err := os.ReadFile(claudeMD)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, "# pre-existing notes by user") {
		t.Errorf("CLAUDE.md content outside markers was not preserved; got:\n%s", got)
	}
	if strings.Contains(got, "Skills (Auto-load based on context)") {
		t.Errorf("CLAUDE.md still contains the stale Auto-load table; got:\n%s", got)
	}
	if !strings.Contains(got, "Contextual Skill Loading (MANDATORY)") {
		t.Errorf("CLAUDE.md missing the new Contextual Skill Loading directive; got:\n%s", got)
	}
}

// ─── TUI path: RunSyncWithSelection persona resolution from state ───────────

func TestRunSyncWithSelection_UnreadablePersistedStateFailsClosed(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, home string)
	}{
		{
			name: "malformed JSON",
			setup: func(t *testing.T, home string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(state.Path(home)), 0o755); err != nil {
					t.Fatalf("MkdirAll state: %v", err)
				}
				if err := os.WriteFile(state.Path(home), []byte("{malformed"), 0o644); err != nil {
					t.Fatalf("WriteFile state: %v", err)
				}
			},
		},
		{
			name: "state path is unreadable",
			setup: func(t *testing.T, home string) {
				t.Helper()
				if err := os.MkdirAll(state.Path(home), 0o755); err != nil {
					t.Fatalf("Mkdir state path: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			setSyncTestHome(t, home)

			if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			originalPersona := "<!-- brunnr-code:persona -->\nexisting valid persona\n<!-- /brunnr-code:persona -->\n"
			personaPath := filepath.Join(home, ".claude", "CLAUDE.md")
			if err := os.WriteFile(personaPath, []byte(originalPersona), 0o644); err != nil {
				t.Fatalf("WriteFile persona: %v", err)
			}
			tt.setup(t, home)

			_, err := RunSyncWithSelection(home, model.Selection{
				Agents:     []model.AgentID{model.AgentClaudeCode},
				Components: []model.ComponentID{model.ComponentPersona},
			})
			if err == nil {
				t.Fatal("RunSyncWithSelection() error = nil, want persisted state read error")
			}
			if !strings.Contains(err.Error(), "read persisted installation state") {
				t.Fatalf("RunSyncWithSelection() error = %q, want state read context", err)
			}
			gotPersona, readErr := os.ReadFile(personaPath)
			if readErr != nil {
				t.Fatalf("ReadFile persona: %v", readErr)
			}
			if string(gotPersona) != originalPersona {
				t.Fatalf("persona config changed after rejected sync: got %q, want %q", gotPersona, originalPersona)
			}
		})
	}
}

// ─── Changed file path reporting ────────────────────────────────────────────

// TestRenderSyncReportIncludesChangedFilePaths verifies that RenderSyncReport
// lists individual file paths when ChangedFiles is populated.
func TestRenderSyncReportIncludesChangedFilePaths(t *testing.T) {
	result := SyncResult{
		NoOp:         false,
		Agents:       []model.AgentID{model.AgentCodex},
		FilesChanged: 3,
		ChangedFiles: []string{
			"~/.codex/AGENTS.md",
			"~/.codex/skills/judgment-day/SKILL.md",
			"~/.codex/hooks.json",
		},
		Selection: model.Selection{
			Components: []model.ComponentID{model.ComponentSkills},
		},
		Verify: verify.Report{Ready: true},
	}

	report := RenderSyncReport(result)

	for _, path := range result.ChangedFiles {
		if !strings.Contains(report, path) {
			t.Errorf("RenderSyncReport() should include changed file path %q; got:\n%s", path, report)
		}
	}

	if !strings.Contains(report, "3 files changed") {
		t.Errorf("RenderSyncReport() should mention file count; got:\n%s", report)
	}
}

// TestRenderSyncReportNoOpOmitsChangedFilePaths verifies that RenderSyncReport
// does not list individual file path bullets in the no-op case.
func TestRenderSyncReportNoOpOmitsChangedFilePaths(t *testing.T) {
	result := SyncResult{
		NoOp:         true,
		Agents:       []model.AgentID{model.AgentCodex},
		FilesChanged: 0,
		ChangedFiles: nil,
	}

	report := RenderSyncReport(result)

	// The no-op path says "No files changed." but must not render bullet paths.
	if strings.Contains(report, "  - ") {
		t.Errorf("RenderSyncReport() should not render file path bullets on no-op; got:\n%s", report)
	}

	if strings.Contains(report, "Sync actions executed") {
		t.Errorf("RenderSyncReport() should not mention 'Sync actions executed' on no-op; got:\n%s", report)
	}
}

// ─── Deduplication ──────────────────────────────────────────────────────────

func TestDedupPathsRemovesDuplicates(t *testing.T) {
	input := []string{
		"/home/user/.codex/AGENTS.md",
		"/home/user/.codex/settings.json",
		"/home/user/.codex/AGENTS.md", // duplicate
		"/home/user/.codex/mcp.json",
		"/home/user/.codex/settings.json", // duplicate
	}
	got := dedupPaths(input)
	want := []string{
		"/home/user/.codex/AGENTS.md",
		"/home/user/.codex/settings.json",
		"/home/user/.codex/mcp.json",
	}
	if len(got) != len(want) {
		t.Fatalf("dedupPaths: got %d paths, want %d", len(got), len(want))
	}
	for i, p := range got {
		if p != want[i] {
			t.Errorf("dedupPaths[%d] = %q, want %q", i, p, want[i])
		}
	}
}

func TestDedupPathsNilOnEmpty(t *testing.T) {
	got := dedupPaths(nil)
	if got != nil {
		t.Errorf("dedupPaths(nil) = %v, want nil", got)
	}
	got = dedupPaths([]string{})
	if got != nil {
		t.Errorf("dedupPaths([]) = %v, want nil", got)
	}
}

// ─── Dry-run persona resolution ───────────────────────────────────────────────

func TestDedupPathsFiltersEmptyStrings(t *testing.T) {
	input := []string{
		"/home/user/.codex/AGENTS.md",
		"",
		"/home/user/.codex/settings.json",
		"   ",
		"",
	}
	got := dedupPaths(input)
	want := []string{
		"/home/user/.codex/AGENTS.md",
		"/home/user/.codex/settings.json",
	}
	if len(got) != len(want) {
		t.Fatalf("dedupPaths: got %d paths, want %d", len(got), len(want))
	}
	for i, p := range got {
		if p != want[i] {
			t.Errorf("dedupPaths[%d] = %q, want %q", i, p, want[i])
		}
	}
}

func TestCodexODDAssignmentSyncPropagatesSavedSelections(t *testing.T) {
	restoreHome, restoreCommand, restoreLookPath := osUserHomeDir, runCommand, cmdLookPath
	t.Cleanup(func() { osUserHomeDir, runCommand, cmdLookPath = restoreHome, restoreCommand, restoreLookPath })
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = func(name string) (string, error) { return "/usr/local/bin/" + name, nil }
	home := setupCodexSyncHomeWithPhaseModels(t,
		map[string]string{"odd-explorer": "gpt-cheap", "odd-worker": "gpt-mid", "odd-verify": "gpt-strong"},
		map[string]string{"odd-explorer": "high", "odd-worker": "xhigh", "odd-verify": "medium"},
		map[string]string{"odd-worker": "gpt-explicit"})
	osUserHomeDir = func() (string, error) { return home, nil }
	selection := model.Selection{
		Agents:                      []model.AgentID{model.AgentCodex},
		CodexPhaseModelAssignments:  map[string]string{"odd-worker": "gpt-explicit"},
		CodexModelAssignments:       map[string]model.CodexEffort{"odd-explorer": model.CodexEffortHigh, "odd-worker": model.CodexEffortXHigh, "odd-verify": model.CodexEffortMedium},
		CodexCarrilModelAssignments: map[string]string{"odd-explorer": "gpt-cheap", "odd-worker": "gpt-mid", "odd-verify": "gpt-strong"},
	}
	runInstallInjectionSteps(t, newTestInstallRuntime(t, home, selection))
	promptPath := filepath.Join(home, ".codex", "AGENTS.md")
	original := readTextFile(t, promptPath)
	const personal = "\n# User-owned note\nKeep this intact.\n"
	if err := os.WriteFile(promptPath, []byte(original+personal), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		_, syncErr := RunSync([]string{"--agents", "codex"})
		body, err := os.ReadFile(filepath.Join(home, ".codex", "AGENTS.md"))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			"<!-- brunnr-code:agent-routing -->", personal,
			"| `odd-explorer` | `gpt-cheap` | `high` |",
			"| `odd-worker` | `gpt-explicit` | `xhigh` |",
			"| `odd-verify` | `gpt-strong` | `medium` |",
		} {
			if !strings.Contains(string(body), want) {
				t.Errorf("sync %d missing %q", i, want)
			}
		}
		if syncErr != nil {
			t.Fatalf("sync %d failed verification after routing projection: %v", i, syncErr)
		}
	}
}

// ─── WU-3 RED: RunSync restores CodexCarrilModelAssignments ──────────────────

// setupCodexSyncHome creates a temp home with a state.json containing the codex
// agent and the provided carril model map, returning the home directory.
func setupCodexSyncHome(t *testing.T, carrilModels map[string]string, effortAssignments map[string]string) string {
	return setupCodexSyncHomeWithPhaseModels(t, carrilModels, effortAssignments, nil)
}

func setupCodexSyncHomeWithPhaseModels(t *testing.T, carrilModels map[string]string, effortAssignments map[string]string, phaseModels map[string]string) string {
	t.Helper()
	t.Cleanup(codex.SetRuntimeVersionCommandForTest("codex-cli 0.144.0", nil))
	home := t.TempDir()
	s := state.InstallState{
		InstalledAgents:             []string{"codex"},
		CodexModelAssignments:       effortAssignments,
		CodexCarrilModelAssignments: carrilModels,
		CodexPhaseModelAssignments:  phaseModels,
	}
	if err := state.Write(home, s); err != nil {
		t.Fatalf("state.Write() error = %v", err)
	}
	return home
}

func TestRunSync_RestoresCodexCarrilAssignments(t *testing.T) {
	restoreHome := osUserHomeDir
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	t.Cleanup(func() {
		osUserHomeDir = restoreHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
	})

	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = func(name string) (string, error) { return "/usr/local/bin/" + name, nil }

	tests := []struct {
		name      string
		persisted map[string]string
		want      map[string]string
		runs      int
	}{
		{name: "preserves custom tuple repeatedly", persisted: map[string]string{"odd-verify": "gpt-5.5", "odd-worker": "gpt-5.4", "odd-explorer": "gpt-5.4-mini"}, want: map[string]string{"odd-verify": "gpt-5.5", "odd-worker": "gpt-5.4", "odd-explorer": "gpt-5.4-mini"}, runs: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(codex.SetRuntimeVersionCommandForTest("codex-cli 0.144.0", nil))
			home := t.TempDir()
			if err := state.Write(home, state.InstallState{InstalledAgents: []string{"codex"}, CodexCarrilModelAssignments: tt.persisted}); err != nil {
				t.Fatalf("state.Write: %v", err)
			}
			osUserHomeDir = func() (string, error) { return home, nil }

			var firstPrompt []byte
			for run := 0; run < tt.runs; run++ {
				result, err := RunSync([]string{"--agents", "codex"})
				if err != nil {
					t.Fatalf("RunSync() run %d error = %v", run+1, err)
				}
				if !reflect.DeepEqual(result.Selection.CodexCarrilModelAssignments, tt.want) {
					t.Fatalf("run %d carril assignments = %#v, want %#v", run+1, result.Selection.CodexCarrilModelAssignments, tt.want)
				}
				prompt, err := os.ReadFile(filepath.Join(home, ".codex", "AGENTS.md"))
				if err != nil {
					t.Fatal(err)
				}
				for _, row := range []string{
					"| `odd-explorer` | `" + tt.want["odd-explorer"] + "` |",
					"| `odd-worker` | `" + tt.want["odd-worker"] + "` |",
					"| `odd-verify` | `" + tt.want["odd-verify"] + "` |",
				} {
					if !strings.Contains(string(prompt), row) {
						t.Errorf("run %d missing %q", run+1, row)
					}
				}
				if run == 0 {
					if result.NoOp || result.FilesChanged == 0 {
						t.Fatalf("first sync metadata = NoOp %v, FilesChanged %d; want managed changes", result.NoOp, result.FilesChanged)
					}
					firstPrompt = prompt
				} else {
					if !bytes.Equal(prompt, firstPrompt) {
						t.Fatal("Codex routing changed between identical syncs")
					}
					if !result.NoOp || result.FilesChanged != 0 || len(result.ChangedFiles) != 0 {
						t.Fatalf("second sync metadata = NoOp %v, FilesChanged %d, ChangedFiles %#v; want no changes", result.NoOp, result.FilesChanged, result.ChangedFiles)
					}
				}
			}

		})
	}
}

func TestRunSyncPreservesCompletePersistedState(t *testing.T) {
	t.Cleanup(codex.SetRuntimeVersionCommandForTest("codex-cli 0.144.0", nil))
	home := t.TempDir()
	lastUpdate := time.Date(2026, time.July, 11, 12, 0, 0, 0, time.UTC)
	before := state.InstallState{
		InstalledAgents: []string{"codex", "claude-code"},
		ClaudeModelAssignments: map[string]string{
			"jd-fix-agent": "haiku",
		},
		ClaudePhaseAssignments: map[string]state.ClaudePhaseAssignmentState{
			"odd-verify": {Model: "opus", Effort: "high"},
		},
		CodexModelAssignments: map[string]string{
			"odd-worker": "medium",
		},
		CodexOrchestratorAssignment: &state.CodexOrchestratorAssignmentState{Model: "gpt-5.6-sol", Effort: "low"},
		CodexServiceTier:            "priority",
		CodexCarrilModelAssignments: map[string]string{"odd-verify": "gpt-5.5", "odd-worker": "gpt-5.5", "odd-explorer": "gpt-5.4-mini"},
		CodexPhaseModelAssignments:  map[string]string{"odd-worker": "gpt-5.6-terra"},
		LastUpdateCheck:             &lastUpdate,
	}
	writer, err := managedAssetDigest()
	if err != nil {
		t.Fatal(err)
	}
	before.ManagedAssetDigest = writer
	if err := state.Write(home, before); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	restoreHome := osUserHomeDir
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	t.Cleanup(func() {
		osUserHomeDir = restoreHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
	})
	osUserHomeDir = func() (string, error) { return home, nil }
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = func(name string) (string, error) { return "/usr/local/bin/" + name, nil }

	if _, err := RunSync([]string{"--agents", "codex"}); err != nil {
		t.Fatalf("RunSync() error = %v", err)
	}
	after, err := state.Read(home)
	if err != nil {
		t.Fatalf("state.Read after sync: %v", err)
	}
	// #2685: sync deliberately stamps the binary version that performed it, so
	// doctor can surface managed assets older than the running binary. It is
	// the one field sync is ALLOWED to write here; everything else must
	// survive byte-identical, which is this guard's whole point.
	if after.InstalledBinaryVersion != AppVersion {
		t.Fatalf("sync did not stamp the binary version: %q, want %q", after.InstalledBinaryVersion, AppVersion)
	}
	expected := before
	expected.InstalledBinaryVersion = AppVersion
	expected.LastSyncedAt = after.LastSyncedAt
	if !reflect.DeepEqual(after, expected) {
		t.Fatalf("CLI sync changed persisted state beyond the version and last-synced stamps:\nafter:  %#v\nbefore: %#v", after, expected)
	}
	if after.LastSyncedAt == nil {
		t.Fatal("sync did not stamp LastSyncedAt")
	}
	if delta := time.Since(*after.LastSyncedAt); delta < 0 || delta > 30*time.Second {
		t.Errorf("LastSyncedAt = %v, expected within 30s of now", after.LastSyncedAt)
	}
}

// TestRunSync_RestoresCodexEffortAssignments verifies that RunSync reads
// persisted CodexModelAssignments into ODD routing.
func TestRunSync_RestoresCodexEffortAssignments(t *testing.T) {
	efforts := map[string]string{
		"jd-judge-a": "xhigh", "jd-judge-b": "xhigh", "default": "xhigh",
		"jd-fix-agent": "high",
		"odd-explorer": "low", "odd-worker": "xhigh", "odd-verify": "high",
	}
	home := setupCodexSyncHome(t, nil, efforts)

	restoreHome := osUserHomeDir
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	t.Cleanup(func() {
		osUserHomeDir = restoreHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
	})

	osUserHomeDir = func() (string, error) { return home, nil }
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = func(name string) (string, error) { return "/usr/local/bin/" + name, nil }

	_, err := RunSync([]string{"--agents", "codex"})
	if err != nil {
		t.Fatalf("RunSync() error = %v", err)
	}

	content := readTextFile(t, filepath.Join(home, ".codex", "AGENTS.md"))
	for _, row := range []string{
		"| `odd-explorer` | `gpt-6.1-luna` | `low` |",
		"| `odd-worker` | `gpt-6.1-luna` | `xhigh` |",
		"| `odd-verify` | `gpt-6.1-sol` | `high` |",
	} {
		if !strings.Contains(content, row) {
			t.Errorf("persisted ODD effort missing %q", row)
		}
	}
}

// TestRunSync_DefaultKeepsSkillHookAndODDRouting verifies that an ordinary
// sync with persisted role assignments keeps the skill hook and ODD routing.
func TestRunSync_DefaultKeepsSkillHookAndODDRouting(t *testing.T) {
	efforts := map[string]string{
		"jd-judge-a": "xhigh", "jd-judge-b": "xhigh", "default": "xhigh",
		"jd-fix-agent": "high",
	}
	phaseModels := map[string]string{
		"default":    "gpt-5.4-mini",
		"odd-worker": "o3",
	}
	home := setupCodexSyncHomeWithPhaseModels(t, nil, efforts, phaseModels)

	restoreHome := osUserHomeDir
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	t.Cleanup(func() {
		osUserHomeDir = restoreHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
	})

	osUserHomeDir = func() (string, error) { return home, nil }
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = func(name string) (string, error) { return "/usr/local/bin/" + name, nil }

	_, err := RunSync([]string{"--agents", "codex"})
	if err != nil {
		t.Fatalf("RunSync() error = %v", err)
	}
	if got := readTextFile(t, filepath.Join(home, ".codex", "hooks.json")); !strings.Contains(got, "skill") {
		t.Fatal("retained Codex skill hook missing")
	}

	agentsMD := filepath.Join(home, ".codex", "AGENTS.md")
	content, readErr := os.ReadFile(agentsMD)
	if readErr != nil {
		t.Fatalf("ReadFile(%q) error = %v", agentsMD, readErr)
	}
	text := string(content)
	if !strings.Contains(text, "Organic Driven Development") {
		t.Fatal("AGENTS.md missing ODD routing")
	}
}

// ─── Organic routing guidance is refreshed for every configured agent ──────
//
// Sync must reach the same unconditional guarantee install does: a persisted
// selection still routes work whatever components it holds (issue #1794).

func runSyncInjectionSteps(t *testing.T, home string, selection model.Selection) []string {
	t.Helper()

	rt, err := newSyncRuntimeWithScope(home, selection, ScopeGlobal)
	if err != nil {
		t.Fatalf("newSyncRuntimeWithScope() error = %v", err)
	}
	for _, step := range rt.stagePlan().Apply {
		if err := step.Run(); err != nil {
			t.Fatalf("Run(%s) error = %v", step.ID(), err)
		}
	}
	return rt.changedFiles
}

// runSyncComponentSteps executes only the component steps of a sync plan.
func runSyncComponentSteps(t *testing.T, home string, selection model.Selection) {
	t.Helper()

	rt, err := newSyncRuntimeWithScope(home, selection, ScopeGlobal)
	if err != nil {
		t.Fatalf("newSyncRuntimeWithScope() error = %v", err)
	}
	for _, step := range rt.stagePlan().Apply {
		if _, isComponent := step.(componentSyncStep); !isComponent {
			continue
		}
		if err := step.Run(); err != nil {
			t.Fatalf("Run(%s) error = %v", step.ID(), err)
		}
	}
}

func TestSyncRemoteAuthorizationUpdatesExistingInstallation(t *testing.T) {
	home := t.TempDir()
	path := systemPromptFileFor(t, home, model.AgentClaudeCode)
	const personal = "Personal instruction: ask before deployment.\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(personal), 0o644); err != nil {
		t.Fatal(err)
	}
	selection := model.Selection{Agents: []model.AgentID{model.AgentClaudeCode}}
	runSyncInjectionSteps(t, home, selection)
	first := readTextFile(t, path)
	if !strings.Contains(first, personal) || !strings.Contains(first, "<!-- brunnr-code:remote-authorization -->") {
		t.Fatal("component-independent sync lost personal text or omitted remote authorization")
	}
	runSyncInjectionSteps(t, home, selection)
	if got := readTextFile(t, path); got != first {
		t.Fatal("repeat sync changed the primary instruction carrier")
	}
}

func TestSyncDeliversRoutingGuidanceWithPersonaOnly(t *testing.T) {
	home := t.TempDir()

	runSyncInjectionSteps(t, home, model.Selection{
		Agents:     []model.AgentID{model.AgentClaudeCode},
		Components: []model.ComponentID{model.ComponentPersona},
	})

	prompt := readTextFile(t, systemPromptFileFor(t, home, model.AgentClaudeCode))
	if !strings.Contains(prompt, routingOpenMarker) || !strings.Contains(prompt, routingCloseMarker) {
		t.Fatalf("persona-only sync left the agent unrouted:\n%s", prompt)
	}
}

func TestSyncRoutingGuidanceIsIndependentOfComponentSelection(t *testing.T) {
	for _, tc := range []struct {
		name      string
		selection model.Selection
	}{
		{"no components", model.Selection{Agents: []model.AgentID{model.AgentClaudeCode}}},
		{"persona selected", model.Selection{Agents: []model.AgentID{model.AgentClaudeCode}, Components: []model.ComponentID{model.ComponentPersona}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			runSyncInjectionSteps(t, home, tc.selection)
			prompt := readTextFile(t, systemPromptFileFor(t, home, model.AgentClaudeCode))
			if !strings.Contains(prompt, routingOpenMarker) || !strings.Contains(prompt, routingCloseMarker) || !strings.Contains(prompt, "Implementation Routing") {
				t.Fatalf("sync lost routing guidance for %s:\n%s", tc.name, prompt)
			}
		})
	}
}

func TestSyncStripsLegacyTriggerRulesSection(t *testing.T) {
	home := t.TempDir()

	promptPath := systemPromptFileFor(t, home, model.AgentClaudeCode)
	if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(promptPath), err)
	}
	seeded := filemerge.InjectMarkdownSection("# My own notes\n", "trigger-rules", "Retired WorkRun ceremony\n")
	if err := os.WriteFile(promptPath, []byte(seeded), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", promptPath, err)
	}

	runSyncInjectionSteps(t, home, model.Selection{Agents: []model.AgentID{model.AgentClaudeCode}})

	prompt := readTextFile(t, promptPath)
	if strings.Contains(prompt, legacyTriggerRulesOpenMarker) {
		t.Fatalf("legacy trigger-rules section survived the sync:\n%s", prompt)
	}
	if !strings.Contains(prompt, "# My own notes") {
		t.Fatalf("stripping the legacy section destroyed unmanaged user content:\n%s", prompt)
	}
}

func TestSyncRoutingGuidanceSecondRunReportsNoChange(t *testing.T) {
	home := t.TempDir()
	selection := model.Selection{Agents: []model.AgentID{model.AgentClaudeCode, model.AgentCodex}}
	runInstallInjectionSteps(t, newTestInstallRuntime(t, home, selection))
	before := map[string]string{
		"Claude prompt": readTextFile(t, systemPromptFileFor(t, home, model.AgentClaudeCode)),
		"Codex prompt":  readTextFile(t, systemPromptFileFor(t, home, model.AgentCodex)),
	}
	runSyncInjectionSteps(t, home, selection)
	if changed := runSyncInjectionSteps(t, home, selection); len(changed) != 0 {
		t.Fatalf("second identical sync rewrote %v; routing delivery is not idempotent", changed)
	}
	for name, want := range before {
		var path string
		switch name {
		case "Claude prompt":
			path = systemPromptFileFor(t, home, model.AgentClaudeCode)
		case "Codex prompt":
			path = systemPromptFileFor(t, home, model.AgentCodex)
		}
		if got := readTextFile(t, path); got != want {
			t.Fatalf("sync changed %s bytes", name)
		}
	}
}

// ─── Routing guidance is part of sync's rollback contract ──────────────────
//
// The routing guidance step runs for every synced agent regardless of the
// persisted components, so its target has to be snapshotted even when no
// component contributes the same file.

func TestSyncBackupTargetsIncludeRoutingGuidancePathsWithoutAnyComponent(t *testing.T) {
	home := t.TempDir()
	agent := model.AgentClaudeCode
	selection := model.Selection{Agents: []model.AgentID{agent}}

	targets, err := syncBackupTargetsScoped(home, "", ScopeGlobal, selection, resolveAdapters(selection.Agents))
	if err != nil {
		t.Fatalf("syncBackupTargets() error = %v", err)
	}

	routing, err := agentguidance.RoutingPathsWithOptions(home, agent, agentguidance.RoutingOptions{})
	if err != nil {
		t.Fatalf("RoutingPaths(%q) error = %v", agent, err)
	}
	if len(routing) == 0 {
		t.Fatalf("RoutingPaths(%q) returned no path; the test proves nothing", agent)
	}
	for _, path := range routing {
		if !containsPath(targets, path) {
			t.Fatalf("syncBackupTargets missing routing guidance path %q\ntargets = %v", path, targets)
		}
	}
}

func TestSyncBackupTargetsContainNoDuplicatePaths(t *testing.T) {
	home := t.TempDir()
	selection := model.Selection{
		Agents:     []model.AgentID{model.AgentClaudeCode, model.AgentCodex},
		Components: []model.ComponentID{model.ComponentEngram, model.ComponentPersona},
	}

	targets, err := syncBackupTargetsScoped(home, "", ScopeGlobal, selection, resolveAdapters(selection.Agents))
	if err != nil {
		t.Fatalf("syncBackupTargets() error = %v", err)
	}

	assertNoDuplicatePaths(t, "syncBackupTargets", targets)
}

// TestRenderSyncReportIncludesManagedActions verifies that the sync output
// reports the managed actions that were executed, not just verification results.
func TestNativeReviewSyncPipelineRollbackRestoresLedgerAndAgent(t *testing.T) {
	home := t.TempDir()
	adapter, err := agents.NewAdapter(model.AgentClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reviewassets.InstallNativeAgents(home, adapter, reviewassets.InstallOptions{CodeGraphGuidanceMarkdown: "original guidance"}); err != nil {
		t.Fatal(err)
	}
	dir := adapter.SubAgentsDir(home)
	ledger := filepath.Join(dir, reviewassets.OwnershipLedgerFilename)
	path := filepath.Join(dir, "jd-judge-a.md")
	ledgerBefore, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	fileBefore, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	selection := model.Selection{Agents: []model.AgentID{model.AgentClaudeCode}}
	runtime, err := newSyncRuntimeWithScope(home, selection, ScopeGlobal)
	if err != nil {
		t.Fatal(err)
	}
	plan := runtime.stagePlan()
	if !containsPath(runtime.managedPaths, ledger) || !containsPath(runtime.managedPaths, path) {
		t.Fatalf("pipeline snapshot missing ledger/agent: %v", runtime.managedPaths)
	}
	plan.Apply = append(plan.Apply, failingSyncStep{})
	result := pipeline.NewOrchestrator(pipeline.DefaultRollbackPolicy()).Execute(plan)
	if result.Err == nil || !result.Rollback.Success {
		t.Fatalf("pipeline rollback result = %+v", result)
	}
	if !containsPath(runtime.changedFiles, ledger) || !containsPath(runtime.changedFiles, path) {
		t.Fatalf("native agent and ledger were not updated before rollback: %v", runtime.changedFiles)
	}
	ledgerAfter, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	fileAfter, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ledgerBefore, ledgerAfter) || !bytes.Equal(fileBefore, fileAfter) {
		t.Fatalf("pipeline did not restore ledger and agent: ledger restored %t, file restored %t", bytes.Equal(ledgerBefore, ledgerAfter), bytes.Equal(fileBefore, fileAfter))
	}
}

func TestNativeReviewSyncPreservesUnknownAndSnapshotsLedger(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	adapter, err := agents.NewAdapter(model.AgentClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	selection := model.Selection{Agents: []model.AgentID{model.AgentClaudeCode}}
	path := filepath.Join(adapter.SubAgentsDir(home), "jd-judge-a.md")
	ledger := filepath.Join(adapter.SubAgentsDir(home), reviewassets.OwnershipLedgerFilename)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("custom bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	targets, err := syncBackupTargetsScoped(home, workspace, ScopeGlobal, selection, []agents.Adapter{adapter})
	if err != nil {
		t.Fatal(err)
	}
	if !containsPath(targets, ledger) || !containsPath(targets, path) {
		t.Fatalf("snapshot targets missing ledger/file: %v", targets)
	}
	before, err := snapshotSyncFiles([]string{ledger, path})
	if err != nil {
		t.Fatal(err)
	}
	state := &runtimeState{}
	changed := []string{}
	step := nativeReviewAgentStep{id: "sync-test", agent: model.AgentClaudeCode, homeDir: home, workspaceDir: workspace, scope: ScopeGlobal, selection: selection, changedFiles: &changed, state: state}
	if err := step.Run(); err != nil {
		t.Fatal(err)
	}
	if !containsPath(changed, ledger) || containsPath(changed, path) {
		t.Fatalf("changed paths = %v", changed)
	}
	if len(state.nativeReviewActions) != 1 || !strings.Contains(RenderSyncReport(SyncResult{NoOp: true, Agents: selection.Agents, ManualActions: state.nativeReviewActions}), nativeReviewPreservedAction(path)) {
		t.Fatalf("actions = %v", state.nativeReviewActions)
	}
	if err := restoreSyncFiles(before); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(ledger); !os.IsNotExist(err) {
		t.Fatalf("ledger after rollback error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "custom bytes" {
		t.Fatalf("user file after rollback = %q, %v", data, err)
	}

	// Opt in for only the warned fixture file by moving it outside the agent
	// directory. Verify the saved bytes before exercising the documented command.
	saved := filepath.Join(t.TempDir(), "saved-agent.md")
	if err := os.Rename(path, saved); err != nil {
		t.Fatal(err)
	}
	if savedBytes, err := os.ReadFile(saved); err != nil || !bytes.Equal(savedBytes, data) {
		t.Fatalf("saved backup = %q, %v", savedBytes, err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	originalHome, originalBackupHome := osUserHomeDir, backup.UserHomeDirFn
	originalCommand, originalLookPath := runCommand, cmdLookPath
	osUserHomeDir = func() (string, error) { return home, nil }
	backup.UserHomeDirFn = func() (string, error) { return home, nil }
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	t.Cleanup(func() {
		osUserHomeDir, backup.UserHomeDirFn = originalHome, originalBackupHome
		runCommand, cmdLookPath = originalCommand, originalLookPath
	})
	for i := 0; i < 2; i++ {
		result, err := RunSync([]string{"--agent", "claude-code", "--scope", "global"})
		if err != nil {
			t.Fatalf("recovery sync %d: %v", i, err)
		}
		if strings.Contains(RenderSyncReport(result), nativeReviewPreservedAction(path)) {
			t.Fatalf("recovery sync %d still preserves warned file: %v", i, result.ManualActions)
		}
		generated, err := os.ReadFile(path)
		if err != nil || len(generated) == 0 || bytes.Equal(generated, data) {
			t.Fatalf("recovered agent = %q, %v", generated, err)
		}
		if _, err := os.Stat(ledger); err != nil {
			t.Fatalf("recovered ledger: %v", err)
		}
	}
	if savedBytes, err := os.ReadFile(saved); err != nil || !bytes.Equal(savedBytes, data) {
		t.Fatalf("recovery changed backup = %q, %v", savedBytes, err)
	}
}

// TestRunSyncSilentlyIgnoresUnsupportedPersistedAgents verifies that a
// state.json recording an agent this build no longer supports does not break
// discovery or sync: the stale ID is dropped and the supported agents sync.
func TestRunSyncSilentlyIgnoresUnsupportedPersistedAgents(t *testing.T) {
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(workspace) error = %v", err)
	}
	t.Chdir(workspace)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(home) error = %v", err)
	}
	if err := state.Write(home, state.InstallState{
		InstalledAgents:     []string{"opencode", "claude-code"},
		SelectionConfigured: true,
	}); err != nil {
		t.Fatalf("state.Write() error = %v", err)
	}

	restoreHome := osUserHomeDir
	restoreBackupHome := backup.UserHomeDirFn
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	osUserHomeDir = func() (string, error) { return home, nil }
	backup.UserHomeDirFn = func() (string, error) { return home, nil }
	cmdLookPath = func(name string) (string, error) { return "/usr/local/bin/" + name, nil }
	runCommand = func(string, ...string) error { return nil }
	t.Cleanup(func() {
		osUserHomeDir = restoreHome
		backup.UserHomeDirFn = restoreBackupHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
	})

	want := []model.AgentID{model.AgentClaudeCode}
	if got := DiscoverAgents(home); !reflect.DeepEqual(got, want) {
		t.Fatalf("DiscoverAgents() = %v, want %v", got, want)
	}

	result, err := RunSync(nil)
	if err != nil {
		t.Fatalf("RunSync() error = %v, want stale agent ignored", err)
	}
	if !reflect.DeepEqual(result.Agents, want) {
		t.Fatalf("RunSync() agents = %v, want %v", result.Agents, want)
	}
}

// setupSkillIDs is the fixed skill set of the single setup.
var setupSkillIDs = []model.SkillID{
	model.SkillJudgmentDay,
	model.SkillGoTesting,
	model.SkillCreator,
	model.SkillImprover,
	model.SkillSkillRegistry,
	model.SkillChainedPR,
	model.SkillCognitiveDoc,
	model.SkillWorkUnitCommits,
}

// TestParseSyncFlagsRejectsRemovedSetupFlags pins that sync has one setup:
// the skill and permission flags no longer exist.
func TestParseSyncFlagsRejectsRemovedSetupFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--include-permissions"},
		{"--skill", "go-testing"},
		{"--skills", "go-testing"},
	} {
		t.Run(args[0], func(t *testing.T) {
			_, err := ParseSyncFlags(args)
			if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
				t.Fatalf("ParseSyncFlags(%v) error = %v, want unknown flag error", args, err)
			}
		})
	}
}

func TestRunSyncWithoutStateUsesSetup(t *testing.T) {
	home := t.TempDir()
	original := osUserHomeDir
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { osUserHomeDir = original })

	result, err := RunSync([]string{"--dry-run", "--agent", "claude-code"})
	if err != nil {
		t.Fatalf("RunSync() error = %v", err)
	}
	if !result.Selection.HasComponent(model.ComponentPermission) {
		t.Fatalf("components = %v, want permission included", result.Selection.Components)
	}
	if got := selectedSkillIDs(result.Selection); !slices.Equal(got, setupSkillIDs) {
		t.Fatalf("skills = %v, want %v", got, setupSkillIDs)
	}
}

// TestRunSyncNormalizesLegacyStateToSetup pins that a state.json written with
// another preset or a narrower selection decodes and syncs the full setup.
func TestRunSyncNormalizesLegacyStateToSetup(t *testing.T) {
	home := t.TempDir()
	legacy := `{"installed_agents":["claude-code"],"selection_configured":true,"components":["engram"],"skills":["comment-writer"],"preset":"minimal"}`
	if err := os.MkdirAll(filepath.Dir(state.Path(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.Path(home), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	original := osUserHomeDir
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { osUserHomeDir = original })

	result, err := RunSync([]string{"--dry-run"})
	if err != nil {
		t.Fatalf("RunSync() error = %v", err)
	}
	want := []model.ComponentID{model.ComponentEngram, model.ComponentSkills, model.ComponentPermission, model.ComponentPersona}
	if !slices.Equal(result.Selection.Components, want) {
		t.Fatalf("components = %v, want %v", result.Selection.Components, want)
	}
	if got := selectedSkillIDs(result.Selection); !slices.Equal(got, setupSkillIDs) {
		t.Fatalf("skills = %v, want %v", got, setupSkillIDs)
	}
	if result.Selection.Preset != model.PresetFull {
		t.Fatalf("preset = %q, want %q", result.Selection.Preset, model.PresetFull)
	}
}
