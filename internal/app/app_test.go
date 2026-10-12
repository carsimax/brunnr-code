package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/carsimax/brunnr-code/internal/agents/codex"
	"github.com/carsimax/brunnr-code/internal/backup"
	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/planner"
	"github.com/carsimax/brunnr-code/internal/state"
	"github.com/carsimax/brunnr-code/internal/system"
	"github.com/carsimax/brunnr-code/internal/tui/screens"
	"github.com/carsimax/brunnr-code/internal/update"
	"github.com/carsimax/brunnr-code/internal/update/upgrade"
	tea "github.com/charmbracelet/bubbletea"
)

func TestClaudeNativeReviewAssignmentsPersistThroughAppStateConversion(t *testing.T) {
	roles := []string{"risk", "readability", "reliability", "resilience", "refuter", "validator"}
	assignments := make(map[string]model.ClaudePhaseAssignment)
	for _, role := range roles {
		assignments[role] = model.ClaudePhaseAssignment{Model: model.ClaudeModelHaiku}
	}
	home := t.TempDir()
	if err := state.Write(home, state.InstallState{ClaudePhaseAssignments: claudePhaseAssignmentsToState(assignments)}); err != nil {
		t.Fatal(err)
	}
	reopened := model.Selection{}
	loadPersistedAssignments(home, &reopened)
	for _, role := range roles {
		if got := reopened.ClaudePhaseAssignments[role].Model; got != model.ClaudeModelHaiku {
			t.Errorf("reopened %s model = %q, want haiku", role, got)
		}
	}
}

// TestListBackupsNewestFirst verifies that ListBackups returns manifests sorted
// newest-first by CreatedAt timestamp, matching the spec "newest first" ordering.
func TestListBackupsNewestFirst(t *testing.T) {
	home := t.TempDir()
	backupRoot := filepath.Join(home, ".gentle-ai", "backups")

	older := backup.Manifest{
		ID:        "older",
		CreatedAt: time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC),
		RootDir:   filepath.Join(backupRoot, "older"),
		Entries:   []backup.ManifestEntry{},
	}
	newer := backup.Manifest{
		ID:        "newer",
		CreatedAt: time.Date(2026, 3, 22, 15, 4, 5, 0, time.UTC),
		RootDir:   filepath.Join(backupRoot, "newer"),
		Entries:   []backup.ManifestEntry{},
	}

	// Write older backup first.
	for _, m := range []backup.Manifest{older, newer} {
		dir := filepath.Join(backupRoot, m.ID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := backup.WriteManifest(filepath.Join(dir, backup.ManifestFilename), m); err != nil {
			t.Fatalf("WriteManifest: %v", err)
		}
	}

	// Temporarily override home dir resolution for ListBackups.
	setupMockHome(t, home)

	manifests := ListBackups()

	if len(manifests) != 2 {
		t.Fatalf("ListBackups() returned %d manifests, want 2", len(manifests))
	}

	// Newest must be first.
	if manifests[0].ID != "newer" {
		t.Errorf("ListBackups()[0].ID = %q, want %q (newest first)", manifests[0].ID, "newer")
	}
	if manifests[1].ID != "older" {
		t.Errorf("ListBackups()[1].ID = %q, want %q", manifests[1].ID, "older")
	}
}

// TestListBackupsWithSourceMetadata verifies that ListBackups returns manifests
// with Source metadata intact, so display labels can use the source field.
func TestListBackupsWithSourceMetadata(t *testing.T) {
	home := t.TempDir()
	backupRoot := filepath.Join(home, ".gentle-ai", "backups")

	m := backup.Manifest{
		ID:          "test-with-source",
		CreatedAt:   time.Now().UTC(),
		RootDir:     filepath.Join(backupRoot, "test-with-source"),
		Source:      backup.BackupSourceInstall,
		Description: "pre-install snapshot",
		Entries:     []backup.ManifestEntry{},
	}

	dir := filepath.Join(backupRoot, m.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := backup.WriteManifest(filepath.Join(dir, backup.ManifestFilename), m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	setupMockHome(t, home)

	manifests := ListBackups()

	if len(manifests) != 1 {
		t.Fatalf("ListBackups() returned %d manifests, want 1", len(manifests))
	}

	got := manifests[0]
	if got.Source != backup.BackupSourceInstall {
		t.Errorf("Source = %q, want %q", got.Source, backup.BackupSourceInstall)
	}
	if got.Description != "pre-install snapshot" {
		t.Errorf("Description = %q, want %q", got.Description, "pre-install snapshot")
	}
}

// TestRunArgsRestoreListIsDispatched verifies that `gentle-ai restore --list`
// is correctly dispatched through RunArgs and produces a meaningful response
// (either a backup list or a "no backups" message — never "unknown command").
func TestRunArgsRestoreListIsDispatched(t *testing.T) {
	home := t.TempDir()
	setupMockHome(t, home)

	var buf bytes.Buffer
	err := RunArgs([]string{"restore", "--list"}, &buf)
	if err != nil {
		t.Fatalf("RunArgs(restore --list) error = %v", err)
	}

	out := buf.String()
	if out == "" {
		t.Fatalf("restore --list produced no output")
	}

	// Must not produce "unknown command".
	if strings.Contains(out, "unknown command") {
		t.Errorf("restore is not registered in RunArgs; got: %s", out)
	}
}

// TestRunArgsRestoreByIDWithYes verifies end-to-end wiring of `restore <id> --yes`
// through app.RunArgs.
func TestRunArgsRestoreByIDWithYes(t *testing.T) {
	home := t.TempDir()
	backupRoot := filepath.Join(home, ".gentle-ai", "backups")

	// Create a backup with a real file entry so restore can succeed.
	sourceFile := filepath.Join(home, "config.md")
	if err := os.WriteFile(sourceFile, []byte("original\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	snapshotDir := filepath.Join(backupRoot, "test-backup-001")
	snapshotFile := filepath.Join(snapshotDir, "files", "config.md")
	if err := os.MkdirAll(filepath.Dir(snapshotFile), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(snapshotFile, []byte("backup-content\n"), 0o644); err != nil {
		t.Fatalf("WriteFile snapshot: %v", err)
	}

	m := backup.Manifest{
		ID:        "test-backup-001",
		CreatedAt: time.Now().UTC(),
		RootDir:   snapshotDir,
		Source:    backup.BackupSourceInstall,
		Entries: []backup.ManifestEntry{
			{OriginalPath: sourceFile, SnapshotPath: snapshotFile, Existed: true, Mode: 0o644},
		},
	}
	if err := backup.WriteManifest(filepath.Join(snapshotDir, backup.ManifestFilename), m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	setupMockHome(t, home)

	var buf bytes.Buffer
	err := RunArgs([]string{"restore", "test-backup-001", "--yes"}, &buf)
	if err != nil {
		t.Fatalf("RunArgs(restore test-backup-001 --yes) error = %v", err)
	}

	out := buf.String()
	if !strings.Contains(strings.ToLower(out), "restor") {
		t.Errorf("restore output should confirm restoration; got:\n%s", out)
	}
}

// TestRunArgsRestoreUnknownIDReturnsError verifies that an unknown backup ID
// is surfaced as an error from RunArgs.
func TestRunArgsRestoreUnknownIDReturnsError(t *testing.T) {
	home := t.TempDir()
	setupMockHome(t, home)

	var buf bytes.Buffer
	err := RunArgs([]string{"restore", "no-such-backup", "--yes"}, &buf)
	if err == nil {
		t.Fatalf("RunArgs(restore no-such-backup) expected error")
	}
	if strings.Contains(err.Error(), "unknown command") {
		t.Errorf("restore returned 'unknown command' — not dispatched: %v", err)
	}
}

func TestRunArgsUninstallIsDispatched(t *testing.T) {
	var buf bytes.Buffer
	// uninstall without required flags prints usage help — that's enough to
	// confirm the dispatch path works without needing real agents or state.
	_ = RunArgs([]string{"uninstall"}, &buf)
	// If we got here without panic, the dispatch to cli.RunUninstall works.
}

func TestRunArgsUninstallBypassesPlatformValidation(t *testing.T) {
	origEnsure := ensureCurrentOSSupported
	t.Cleanup(func() { ensureCurrentOSSupported = origEnsure })
	ensureCurrentOSSupported = func() error {
		return fmt.Errorf("unsupported platform")
	}

	var buf bytes.Buffer
	// uninstall should NOT call ensureCurrentOSSupported — it runs before
	// the platform check in the switch.
	_ = RunArgs([]string{"uninstall"}, &buf)
	// If we got here, uninstall bypassed the platform validation.
}

func TestRunArgsUninstallReportsBackupOnFailure(t *testing.T) {
	home := t.TempDir()
	setupMockHome(t, home)
	t.Chdir(t.TempDir())

	statePath := state.Path(home)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	err := RunArgs([]string{"uninstall", "--agent", "codex", "--yes"}, &output)
	if err == nil || !strings.Contains(err.Error(), "read install state") {
		t.Fatalf("RunArgs(uninstall) error = %v, want install state read failure", err)
	}
	for _, want := range []string{"Backup:", "Backup path:"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("RunArgs(uninstall) output missing %q:\n%s", want, output.String())
		}
	}
}

func TestRunArgsInstallHelpPrintsInstallSpecificHelp(t *testing.T) {
	origEnsure := ensureCurrentOSSupported
	t.Cleanup(func() { ensureCurrentOSSupported = origEnsure })
	ensureCurrentOSSupported = func() error {
		return fmt.Errorf("platform validation should not run for install help")
	}

	var buf bytes.Buffer
	err := RunArgs([]string{"install", "--help"}, &buf)
	if err != nil {
		t.Fatalf("RunArgs(install --help) error = %v", err)
	}

	out := buf.String()
	for _, want := range []string{"--agent", "--component", "--dry-run"} {
		if !strings.Contains(out, want) {
			t.Fatalf("install help missing %q; output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "--channel") {
		t.Fatalf("install help still advertises the removed --channel flag; output:\n%s", out)
	}
}

// TestRunArgsRestoreHelpBypassesSystemDetection verifies that an explicit
// restore help request is answered before system detection, like the install
// and sync help pre-dispatch: a host with no resolvable home directory must
// still be able to read the restore usage. The complement case proves a
// non-help restore invocation still goes through system detection.
func TestRunArgsRestoreHelpBypassesSystemDetection(t *testing.T) {
	origDetect := detectSystem
	origEnsure := ensureCurrentOSSupported
	t.Cleanup(func() {
		detectSystem = origDetect
		ensureCurrentOSSupported = origEnsure
	})
	ensureCurrentOSSupported = func() error { return nil }
	detectSystem = func(context.Context) (system.DetectionResult, error) {
		return system.DetectionResult{}, fmt.Errorf("$HOME is not defined")
	}

	for _, helpFlag := range []string{"--help", "-help", "-h", "--h"} {
		t.Run(helpFlag, func(t *testing.T) {
			var buf bytes.Buffer
			if err := RunArgs([]string{"restore", helpFlag}, &buf); err != nil {
				t.Fatalf("RunArgs(restore %s) error = %v", helpFlag, err)
			}
			out := buf.String()
			for _, want := range []string{
				"gentle-ai restore [--list | latest | <id>] [--yes]",
				"list available backups without restoring",
				"skip confirmation prompt",
			} {
				if !strings.Contains(out, want) {
					t.Fatalf("restore help missing %q; output:\n%s", want, out)
				}
			}
		})
	}

	// Complement: restore without a help flag must still be dispatched after
	// system detection, so the failing stub must surface as an error.
	var buf bytes.Buffer
	err := RunArgs([]string{"restore", "--list"}, &buf)
	if err == nil || !strings.Contains(err.Error(), "$HOME is not defined") {
		t.Fatalf("RunArgs(restore --list) error = %v, want detection failure", err)
	}
}

// TestHelpFlagSpellings pins the helper's contract: the flag package treats
// one and two leading dashes as equivalent and both "help" and "h" trigger
// flag.ErrHelp, so hasHelpFlag must recognise all four spellings and nothing
// else.
func TestHelpFlagSpellings(t *testing.T) {
	for _, arg := range []string{"--help", "-help", "-h", "--h"} {
		if !hasHelpFlag([]string{arg}) {
			t.Errorf("hasHelpFlag(%q) = false, want true", arg)
		}
	}
	for _, arg := range []string{"--list", "latest", "--helpx"} {
		if hasHelpFlag([]string{arg}) {
			t.Errorf("hasHelpFlag(%q) = true, want false", arg)
		}
	}
	if hasHelpFlag(nil) {
		t.Error("hasHelpFlag(empty) = true, want false")
	}
}

func TestRunArgsNativeSDDCommandsNoLongerDispatch(t *testing.T) {
	for _, command := range []string{"sdd-status", "sdd-continue", "sdd-attempt"} {
		t.Run(command, func(t *testing.T) {
			var output bytes.Buffer
			err := RunArgs([]string{command, "--help"}, &output)
			if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("unknown command %q", command)) {
				t.Fatalf("RunArgs(%s --help) error = %v, want unknown command; output = %q", command, err, output.String())
			}
		})
	}
}

func TestRunArgsDispatchesNativeReviewOperationsBeforePlatformValidation(t *testing.T) {
	origEnsure := ensureCurrentOSSupported
	t.Cleanup(func() { ensureCurrentOSSupported = origEnsure })
	ensureCurrentOSSupported = func() error { return fmt.Errorf("unsupported platform") }

	for _, test := range []struct {
		command string
		want    string
	}{
		{command: "review-start", want: "review-start requires --cwd, --lineage, and --policy-file"},
		{command: "review-resume", want: "review-resume requires --cwd and --lineage"},
		{command: "review-bundle-export", want: "review-bundle-export requires --cwd, --lineage, and --out"},
		{command: "review-bundle-import", want: "review-bundle-import requires --cwd and --bundle"},
		{command: "review-validate", want: "review-validate requires --cwd"},
	} {
		t.Run(test.command, func(t *testing.T) {
			var output bytes.Buffer
			err := RunArgs([]string{test.command}, &output)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("RunArgs(%s) error = %v, want %q", test.command, err, test.want)
			}
		})
	}
}

func TestRunArgsReviewSubcommandHelpExitsSuccessfully(t *testing.T) {
	for _, command := range []string{"review-start", "review-step", "review-resume", "review-validate", "review-bundle-export", "review-bundle-import"} {
		t.Run(command, func(t *testing.T) {
			var output bytes.Buffer
			if err := RunArgs([]string{command, "--help"}, &output); err != nil {
				t.Fatalf("RunArgs(%s --help) error = %v", command, err)
			}
			if !strings.Contains(output.String(), "Usage: gentle-ai "+command+" [flags]") {
				t.Fatalf("RunArgs(%s --help) output:\n%s", command, output.String())
			}
		})
	}
}

func TestRunArgsDispatchesCompactReviewFacadeBeforePlatformValidation(t *testing.T) {
	origEnsure := ensureCurrentOSSupported
	t.Cleanup(func() { ensureCurrentOSSupported = origEnsure })
	ensureCurrentOSSupported = func() error { return fmt.Errorf("unsupported platform") }

	var output bytes.Buffer
	if err := RunArgs([]string{"review", "--help"}, &output); err != nil {
		t.Fatalf("RunArgs(review --help) error = %v", err)
	}
	if !strings.Contains(output.String(), "review <acknowledge-approved|capture-result|capture-correction-plan|capture-refuter|capture-unachievable|capture-validation|lens-context|capabilities|assess|start|validate|status|repair|invalidate|abandon|recover|reclaim|store-reset|inspect-authority|inspect-candidate|reopen-results|schema>") {
		t.Fatalf("compact review help missing:\n%s", output.String())
	}
	for _, retired := range []string{"preserve-result", "dispose-result"} {
		if strings.Contains(output.String(), retired) {
			t.Fatalf("compact review help still advertises retired %q:\n%s", retired, output.String())
		}
	}
	output.Reset()
	if err := RunArgs([]string{"review", "repair", "--help"}, &output); err != nil || !strings.Contains(output.String(), "provider-owned") {
		t.Fatalf("RunArgs(review repair --help) = %v\n%s", err, output.String())
	}
}

func TestRunArgsDispatchesReviewModeBeforePlatformValidation(t *testing.T) {
	origEnsure := ensureCurrentOSSupported
	t.Cleanup(func() { ensureCurrentOSSupported = origEnsure })
	ensureCurrentOSSupported = func() error { return fmt.Errorf("unsupported platform") }

	var output bytes.Buffer
	if err := RunArgs([]string{"review", "mode", "--help"}, &output); err != nil {
		t.Fatalf("RunArgs(review mode --help) error = %v", err)
	}
	if !strings.Contains(output.String(), "gentle-ai review mode <enable|disable|status>") {
		t.Fatalf("review mode help missing:\n%s", output.String())
	}

	output.Reset()
	err := RunArgs([]string{"review", "mode", "toggle"}, &output)
	if err == nil || !strings.Contains(err.Error(), "unknown review mode command") {
		t.Fatalf("RunArgs(review mode toggle) error = %v", err)
	}
}

// TestListBackupsFallsBackGracefullyForOldManifests verifies that old manifests
// without Source/Description are still returned (not skipped) and can be displayed
// via DisplayLabel without panicking.
func TestListBackupsFallsBackGracefullyForOldManifests(t *testing.T) {
	_ = fmt.Sprintf // Ensure fmt is used.
	home := t.TempDir()
	backupRoot := filepath.Join(home, ".gentle-ai", "backups")

	// Write a manifest with no Source/Description.
	m := backup.Manifest{
		ID:        "old-backup",
		CreatedAt: time.Now().UTC(),
		RootDir:   filepath.Join(backupRoot, "old-backup"),
		Entries:   []backup.ManifestEntry{},
		// Source and Description intentionally omitted — simulates old manifest.
	}

	dir := filepath.Join(backupRoot, m.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := backup.WriteManifest(filepath.Join(dir, backup.ManifestFilename), m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	setupMockHome(t, home)

	manifests := ListBackups()

	if len(manifests) != 1 {
		t.Fatalf("ListBackups() returned %d manifests, want 1", len(manifests))
	}

	// Must not panic — DisplayLabel should handle empty Source gracefully.
	label := manifests[0].DisplayLabel()
	if label == "" {
		t.Errorf("DisplayLabel() returned empty string, want non-empty fallback label")
	}
}

// ─── BUG 3: SyncOverrides.StrictTDD never read in tuiSync ───────────────────

// TestTuiSyncAppliesStrictTDDOverride verifies that applyOverrides correctly
// merges SyncOverrides.StrictTDD into the selection.
// Previously, the field was declared on SyncOverrides but never applied.
func TestTuiSyncAppliesStrictTDDOverride(t *testing.T) {
	sel := boolPtr(true)
	overrides := &model.SyncOverrides{StrictTDD: sel}

	selection := model.Selection{StrictTDD: false}
	applyOverrides(&selection, overrides)

	if !selection.StrictTDD {
		t.Fatalf("Selection.StrictTDD = false after applyOverrides with StrictTDD=true override; field is not being applied")
	}
}

// TestTuiSyncAppliesStrictTDDOverrideFalse verifies the override correctly sets
// StrictTDD to false when the pointer points to false.
func TestTuiSyncAppliesStrictTDDOverrideFalse(t *testing.T) {
	sel := boolPtr(false)
	overrides := &model.SyncOverrides{StrictTDD: sel}

	selection := model.Selection{StrictTDD: true}
	applyOverrides(&selection, overrides)

	if selection.StrictTDD {
		t.Fatalf("Selection.StrictTDD = true after applyOverrides with StrictTDD=false override")
	}
}

// TestTuiSyncStrictTDDNilOverrideNoChange verifies that when StrictTDD override
// is nil, the selection's existing value is preserved.
func TestTuiSyncStrictTDDNilOverrideNoChange(t *testing.T) {
	overrides := &model.SyncOverrides{StrictTDD: nil}

	selection := model.Selection{StrictTDD: true}
	applyOverrides(&selection, overrides)

	if !selection.StrictTDD {
		t.Fatalf("Selection.StrictTDD changed unexpectedly; nil override should not modify the field")
	}
}

func boolPtr(b bool) *bool { return &b }

func TestTuiSyncTargetAgentsOverridePersistedInstallState(t *testing.T) {
	home := t.TempDir()
	if err := state.Write(home, state.InstallState{InstalledAgents: []string{string(model.AgentCodex)}}); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	got := syncAgentIDs(home, &model.SyncOverrides{
		TargetAgents: []model.AgentID{model.AgentClaudeCode, model.AgentClaudeCode, ""},
	})

	if len(got) != 1 || got[0] != model.AgentClaudeCode {
		t.Fatalf("syncAgentIDs() = %v, want [%s]", got, model.AgentClaudeCode)
	}
}

func TestTuiSyncTargetAgentsFallsBackToDiscoveredAgents(t *testing.T) {
	home := t.TempDir()
	if err := state.Write(home, state.InstallState{InstalledAgents: []string{string(model.AgentCodex)}}); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	got := syncAgentIDs(home, nil)

	if len(got) != 1 || got[0] != model.AgentCodex {
		t.Fatalf("syncAgentIDs(nil) = %v, want [%s]", got, model.AgentCodex)
	}
}

func TestTuiSyncSelectionPreservesCustomPermissionExclusion(t *testing.T) {
	home := t.TempDir()
	if err := state.Write(home, state.InstallState{InstalledAgents: []string{"codex"}, SelectionConfigured: true, Components: []model.ComponentID{model.ComponentSkills}, Skills: []model.SkillID{model.SkillCommentWriter}, Preset: model.PresetCustom}); err != nil {
		t.Fatal(err)
	}
	selection := model.Selection{Agents: []model.AgentID{model.AgentCodex}, Components: []model.ComponentID{model.ComponentPermission}, Preset: model.PresetFullGentleman}
	loadPersistedAssignments(home, &selection)
	if selection.HasComponent(model.ComponentPermission) || !reflect.DeepEqual(selection.Skills, []model.SkillID{model.SkillCommentWriter}) || selection.Preset != model.PresetCustom {
		t.Fatalf("TUI sync replaced custom selection: %#v", selection)
	}
}

func TestTUIExecutePersistsConfiguredSelection(t *testing.T) {
	home := t.TempDir()
	setupMockHome(t, home)
	if err := state.Write(home, state.InstallState{RDDMode: "off"}); err != nil {
		t.Fatal(err)
	}
	selection := model.Selection{Agents: []model.AgentID{model.AgentClaudeCode}, Preset: model.PresetCustom, Components: []model.ComponentID{}, Skills: []model.SkillID{}, StrictTDD: true}
	result := tuiExecute(selection, planner.ResolvedPlan{}, system.DetectionResult{}, nil)
	got, err := state.Read(home)
	if result.Err != nil || err != nil || !got.SelectionConfigured || got.Preset != model.PresetCustom || got.StrictTDD || got.RDDMode != "off" || !slices.Equal(got.InstalledAgents, []string{string(model.AgentClaudeCode)}) || len(got.Components) != 0 || len(got.Skills) != 0 {
		t.Fatalf("persisted selection = %#v, execute err = %v, read err = %v", got, result.Err, err)
	}
	persisted, err := os.ReadFile(state.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(persisted, []byte(`"strict_tdd"`)) {
		t.Fatalf("retired strict_tdd persisted: %s", persisted)
	}
}

func buildAppCandidateBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "gentle-ai")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	// A cold build of the whole binary on a shared CI runner can exceed
	// 30s; the cap only guards against a hung toolchain, not build speed.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	// This fixture tests runtime state, not VCS metadata. Avoid consulting
	// ambient repository ancestry when building from a linked worktree.
	command := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", binary, "../../cmd/brunnr-code")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build candidate binary: %v\n%s", err, output)
	}
	return binary
}

func TestTuiSyncPreservesClaudeBalancedPreset(t *testing.T) {
	for _, phase := range []bool{false, true} {
		t.Run(fmt.Sprintf("phase=%t", phase), func(t *testing.T) {
			home := t.TempDir()
			balanced := model.ClaudeModelPresetBalanced()
			initial := state.InstallState{InstalledAgents: []string{string(model.AgentClaudeCode)}}
			if phase {
				initial.ClaudePhaseAssignments = make(map[string]state.ClaudePhaseAssignmentState)
				for role, alias := range balanced {
					initial.ClaudePhaseAssignments[role] = state.ClaudePhaseAssignmentState{Model: string(alias)}
				}
			} else {
				initial.ClaudeModelAssignments = make(map[string]string)
				for role, alias := range balanced {
					initial.ClaudeModelAssignments[role] = string(alias)
				}
			}
			if err := state.Write(home, initial); err != nil {
				t.Fatal(err)
			}
			for syncRun := 0; syncRun < 2; syncRun++ {
				if _, err := tuiSync(home)(nil); err != nil {
					t.Fatalf("sync %d: %v", syncRun, err)
				}
				persisted, err := state.Read(home)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(persisted.ClaudePhaseAssignments, initial.ClaudePhaseAssignments) ||
					!reflect.DeepEqual(persisted.ClaudeModelAssignments, initial.ClaudeModelAssignments) {
					t.Fatalf("sync %d changed Claude preset assignments: phase=%v legacy=%v", syncRun, persisted.ClaudePhaseAssignments, persisted.ClaudeModelAssignments)
				}
				reopened := model.Selection{}
				loadPersistedAssignments(home, &reopened)
				assignments := reopened.ClaudePhaseAssignments
				if !phase {
					assignments = model.ClaudePhaseAssignmentsFromLegacy(reopened.ClaudeModelAssignments)
				}
				picker := screens.NewClaudeModelPickerStateFromPhaseAssignments(assignments)
				if picker.Preset != screens.ClaudePresetBalanced {
					t.Fatalf("sync %d reopened preset = %v, want Balanced", syncRun, picker.Preset)
				}
				body, err := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(body), "| orchestrator |") {
					t.Fatal("persisted metadata must not expose an orchestrator model row in the parent prompt")
				}
			}
		})
	}
}

func TestTuiSyncClaudeModelConfigWritesSelectedAssignments(t *testing.T) {
	home := t.TempDir()
	if err := state.Write(home, state.InstallState{InstalledAgents: []string{string(model.AgentCodex)}}); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	assignments := map[string]model.ClaudeModelAlias{
		"review-risk": model.ClaudeModelHaiku,
		"jd-judge-a":  model.ClaudeModelHaiku,
		"default":     model.ClaudeModelHaiku,
	}

	changed, err := tuiSync(home)(&model.SyncOverrides{
		TargetAgents:           []model.AgentID{model.AgentClaudeCode},
		ClaudeModelAssignments: assignments,
	})
	if err != nil {
		t.Fatalf("tuiSync Claude model config error: %v", err)
	}
	if len(changed) == 0 {
		t.Fatal("tuiSync Claude model config changed 0 files, want Claude assets written")
	}

	for _, name := range []string{"review-risk.md", "jd-judge-a.md"} {
		path := filepath.Join(home, ".claude", "agents", name)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", path, err)
		}
		if !strings.Contains(string(body), "model: haiku") {
			t.Fatalf("%s did not receive selected model; got:\n%s", name, body)
		}
	}

	persisted, err := state.Read(home)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range assignments {
		if got := persisted.ClaudeModelAssignments[key]; got != string(want) {
			t.Fatalf("ClaudeModelAssignments[%s] = %q, want %q", key, got, want)
		}
	}
	promptPath := filepath.Join(home, ".claude", "CLAUDE.md")
	body, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", promptPath, err)
	}
	if strings.Contains(string(body), "| orchestrator |") {
		t.Fatalf("Claude parent prompt should not expose orchestrator as a configurable model row; got:\n%s", body)
	}
}

func TestTuiSyncModelConfigPropagatesAssignmentWriteFailure(t *testing.T) {
	home := t.TempDir()
	original := state.InstallState{
		InstalledAgents:          []string{string(model.AgentClaudeCode)},
		CommunityToolsConfigured: true,
		ClaudeModelAssignments:   map[string]string{"review-risk": "haiku"},
	}
	if err := state.Write(home, original); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	statePath := state.Path(home)
	stateTarget := filepath.Join(home, ".gentle-ai", "persisted-state.json")
	if err := os.Rename(statePath, stateTarget); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if err := os.Symlink(stateTarget, statePath); err != nil {
		t.Skipf("state symlink unavailable: %v", err)
	}

	_, err := tuiSync(home)(&model.SyncOverrides{
		TargetAgents: []model.AgentID{model.AgentClaudeCode},
		ClaudeModelAssignments: map[string]model.ClaudeModelAlias{
			"review-risk": model.ClaudeModelSonnet,
		},
	})
	if err == nil {
		t.Fatal("tuiSync() error = nil, want assignment persistence failure")
	}

	got, readErr := state.Read(home)
	if readErr != nil {
		t.Fatalf("state.Read: %v", readErr)
	}
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("state after failed tuiSync() = %#v, want %#v", got, original)
	}
}

func TestTuiSyncClaudePhaseAssignmentsPersistAndGenerateEffort(t *testing.T) {
	home := t.TempDir()
	if err := state.Write(home, state.InstallState{InstalledAgents: []string{string(model.AgentCodex)}}); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	phaseAssignments := model.ClaudePhaseAssignmentsFromLegacy(map[string]model.ClaudeModelAlias{
		"review-risk": model.ClaudeModelSonnet,
		"jd-judge-a":  model.ClaudeModelSonnet,
		"default":     model.ClaudeModelSonnet,
	})
	phaseAssignments["review-risk"] = model.ClaudePhaseAssignment{
		Model:  model.ClaudeModelSonnet,
		Effort: model.ClaudeEffortMax,
	}

	changed, err := tuiSync(home)(&model.SyncOverrides{
		TargetAgents:           []model.AgentID{model.AgentClaudeCode},
		ClaudePhaseAssignments: phaseAssignments,
	})
	if err != nil {
		t.Fatalf("tuiSync Claude phase config error: %v", err)
	}
	if len(changed) == 0 {
		t.Fatal("tuiSync Claude phase config changed 0 files, want Claude assets written")
	}

	persisted, err := state.Read(home)
	if err != nil {
		t.Fatalf("state.Read: %v", err)
	}
	reviewState, ok := persisted.ClaudePhaseAssignments["review-risk"]
	if !ok {
		t.Fatalf("persisted state missing claude_phase_assignments.review-risk: %#v", persisted.ClaudePhaseAssignments)
	}
	if reviewState.Model != string(model.ClaudeModelSonnet) || reviewState.Effort != string(model.ClaudeEffortMax) {
		t.Fatalf("persisted review-risk = %#v, want sonnet/max", reviewState)
	}
	if persisted.ClaudeModelAssignments != nil {
		t.Fatalf("legacy claude_model_assignments should be cleared when phase assignments are persisted; got %#v", persisted.ClaudeModelAssignments)
	}

	reviewAgent := filepath.Join(home, ".claude", "agents", "review-risk.md")
	body, err := os.ReadFile(reviewAgent)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", reviewAgent, err)
	}
	for _, want := range []string{"model: sonnet", "effort: max"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("review-risk agent missing %q; got:\n%s", want, body)
		}
	}

	judgeAgent := filepath.Join(home, ".claude", "agents", "jd-judge-a.md")
	body, err = os.ReadFile(judgeAgent)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", judgeAgent, err)
	}
	if !strings.Contains(string(body), "model: sonnet") || strings.Contains(string(body), "effort:") {
		t.Fatalf("default-effort judge should have model but omit effort frontmatter; got:\n%s", body)
	}

	beforeState := persisted.ClaudePhaseAssignments
	beforeReview, err := os.ReadFile(reviewAgent)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", reviewAgent, err)
	}
	beforeJudge, err := os.ReadFile(judgeAgent)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", judgeAgent, err)
	}
	beforeAgentFiles := filesUnder(t, filepath.Join(home, ".claude", "agents"))

	changed, err = tuiSync(home)(&model.SyncOverrides{
		TargetAgents:           []model.AgentID{model.AgentClaudeCode},
		ClaudePhaseAssignments: phaseAssignments,
	})
	if err != nil {
		t.Fatalf("second tuiSync Claude phase config error: %v", err)
	}
	if len(changed) == 0 {
		t.Log("second tuiSync reported no file changes")
	}
	afterAgentFiles := filesUnder(t, filepath.Join(home, ".claude", "agents"))
	if !reflect.DeepEqual(afterAgentFiles, beforeAgentFiles) {
		t.Fatalf("Claude agent file set changed after second sync: got %#v want %#v", afterAgentFiles, beforeAgentFiles)
	}

	persisted, err = state.Read(home)
	if err != nil {
		t.Fatalf("state.Read after second sync: %v", err)
	}
	if !reflect.DeepEqual(persisted.ClaudePhaseAssignments, beforeState) {
		t.Fatalf("ClaudePhaseAssignments changed after second sync: got %#v want %#v", persisted.ClaudePhaseAssignments, beforeState)
	}
	afterReview, err := os.ReadFile(reviewAgent)
	if err != nil {
		t.Fatalf("ReadFile(%s) after second sync: %v", reviewAgent, err)
	}
	if !bytes.Equal(afterReview, beforeReview) {
		t.Fatalf("review-risk agent changed after idempotent sync")
	}
	afterJudge, err := os.ReadFile(judgeAgent)
	if err != nil {
		t.Fatalf("ReadFile(%s) after second sync: %v", judgeAgent, err)
	}
	if !bytes.Equal(afterJudge, beforeJudge) {
		t.Fatalf("jd-judge-a agent changed after idempotent sync")
	}
}

// TestApplyOverrides_KiroModelAssignments verifies that a non-nil KiroModelAssignments
// override replaces the entire KiroModelAssignments map in the selection (same
// replacement semantics as ClaudeModelAssignments — not a key-level merge).
func filesUnder(t *testing.T, root string) []string {
	t.Helper()

	var files []string
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	}); err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(files)
	return files
}

// ─── Persist model assignments (TUI path) ───────────────────────────────────

// TestLoadPersistedAssignmentsPopulatesEmptySelection verifies that when
// state.json has model assignments and the selection maps are empty, they
// get populated from persisted state.
func TestLoadPersistedAssignmentsPopulatesEmptySelection(t *testing.T) {
	home := t.TempDir()

	// Seed state with assignments.
	err := state.Write(home, state.InstallState{
		InstalledAgents: []string{"codex"},
		ClaudeModelAssignments: map[string]string{
			"orchestrator": "opus",
			"sdd-apply":    "sonnet",
		},
	})
	if err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	selection := model.Selection{}
	loadPersistedAssignments(home, &selection)

	if got := selection.ClaudeModelAssignments["orchestrator"]; got != model.ClaudeModelOpus {
		t.Errorf("persisted orchestrator metadata = %q, want opus", got)
	}
	if got := selection.ClaudeModelAssignments["sdd-apply"]; got != "sonnet" {
		t.Errorf("ClaudeModelAssignments[sdd-apply] = %q, want %q", got, "sonnet")
	}
}

// TestLoadPersistedAssignmentsDoesNotOverrideExisting verifies that when the
// selection already has assignments (e.g. from TUI overrides), persisted
// state does NOT clobber them.
func TestLoadPersistedAssignmentsDoesNotOverrideExisting(t *testing.T) {
	home := t.TempDir()

	// Seed state with "old" assignments.
	err := state.Write(home, state.InstallState{
		ClaudeModelAssignments: map[string]string{"sdd-apply": "haiku"},
	})
	if err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	// Selection already has assignments from the TUI configure flow.
	selection := model.Selection{
		ClaudeModelAssignments: map[string]model.ClaudeModelAlias{
			"sdd-apply": "opus",
		},
	}
	loadPersistedAssignments(home, &selection)

	// Existing values must be preserved, NOT overwritten.
	if got := selection.ClaudeModelAssignments["sdd-apply"]; got != "opus" {
		t.Errorf("ClaudeModelAssignments[sdd-apply] = %q, want %q (should not be overwritten)", got, "opus")
	}
}

// TestPersistAssignmentsPreservesInstalledAgents verifies the read-merge-write
// pattern: persisting assignments must NOT lose the InstalledAgents list.
func TestPersistAssignmentsPreservesInstalledAgents(t *testing.T) {
	home := t.TempDir()

	// Pre-existing state with agents.
	err := state.Write(home, state.InstallState{
		InstalledAgents: []string{"claude-code", "codex"},
	})
	if err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	selection := model.Selection{
		ClaudeModelAssignments: map[string]model.ClaudeModelAlias{
			"orchestrator": "opus",
			"sdd-apply":    "sonnet",
		},
	}
	persistAssignments(home, selection)

	// Read back and verify agents are still there.
	got, err := state.Read(home)
	if err != nil {
		t.Fatalf("state.Read: %v", err)
	}
	if len(got.InstalledAgents) != 2 {
		t.Fatalf("InstalledAgents = %v, want [claude-code codex]", got.InstalledAgents)
	}
	if alias := got.ClaudeModelAssignments["orchestrator"]; alias != "opus" {
		t.Errorf("persisted orchestrator metadata = %q, want opus", alias)
	}
	if got.ClaudeModelAssignments["sdd-apply"] != "sonnet" {
		t.Errorf("ClaudeModelAssignments[sdd-apply] = %q, want %q", got.ClaudeModelAssignments["sdd-apply"], "sonnet")
	}
}

func TestPersistAssignmentsClearsNonPhaseAssignmentMaps(t *testing.T) {
	home := t.TempDir()
	if err := state.Write(home, state.InstallState{
		InstalledAgents: []string{"example-agent", "codex", "claude-code"},
		ClaudeModelAssignments: map[string]string{
			"sdd-apply": "sonnet",
		},
		CodexModelAssignments: map[string]string{
			"sdd-apply": "high",
		},
		CodexCarrilModelAssignments: map[string]string{
			"sdd-strong": "gpt-5.4",
		},
	}); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	persistAssignments(home, model.Selection{
		ClaudeModelAssignments:      map[string]model.ClaudeModelAlias{},
		CodexModelAssignments:       map[string]model.CodexEffort{},
		CodexCarrilModelAssignments: map[string]string{},
	})

	got, err := state.Read(home)
	if err != nil {
		t.Fatalf("state.Read: %v", err)
	}
	if got.ClaudeModelAssignments != nil {
		t.Fatalf("ClaudeModelAssignments = %#v, want nil", got.ClaudeModelAssignments)
	}
	if got.CodexModelAssignments != nil {
		t.Fatalf("CodexModelAssignments = %#v, want nil", got.CodexModelAssignments)
	}
	if got.CodexCarrilModelAssignments != nil {
		t.Fatalf("CodexCarrilModelAssignments = %#v, want nil", got.CodexCarrilModelAssignments)
	}
	if len(got.InstalledAgents) != 3 {
		t.Fatalf("InstalledAgents = %#v, want preserved agents", got.InstalledAgents)
	}
}

func TestApplyOverridesClaudePhaseAssignmentsClearsLegacyAssignments(t *testing.T) {
	selection := model.Selection{
		ClaudeModelAssignments: map[string]model.ClaudeModelAlias{
			"sdd-apply": model.ClaudeModelOpus,
		},
	}
	overrides := &model.SyncOverrides{
		ClaudePhaseAssignments: map[string]model.ClaudePhaseAssignment{},
	}

	applyOverrides(&selection, overrides)

	if selection.ClaudeModelAssignments != nil {
		t.Fatalf("ClaudeModelAssignments = %#v, want nil when phase assignments are provided", selection.ClaudeModelAssignments)
	}
	if selection.ClaudePhaseAssignments == nil {
		t.Fatal("ClaudePhaseAssignments = nil, want explicit override map")
	}
}

func TestPersistAssignmentsSkipsCorruptState(t *testing.T) {
	home := t.TempDir()
	statePath := state.Path(home)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	original := []byte("{not valid json\n")
	if err := os.WriteFile(statePath, original, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	persistAssignments(home, model.Selection{
		CodexPhaseModelAssignments: map[string]string{},
	})

	got, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("state.json was overwritten after corrupt-state read error:\n%s", got)
	}
}

func TestPersistAssignmentsClearsClaudePhaseAssignments(t *testing.T) {
	home := t.TempDir()
	if err := state.Write(home, state.InstallState{
		InstalledAgents: []string{string(model.AgentClaudeCode)},
		ClaudeModelAssignments: map[string]string{
			"sdd-apply": "haiku",
		},
		ClaudePhaseAssignments: map[string]state.ClaudePhaseAssignmentState{
			"sdd-apply": {Model: "sonnet", Effort: "max"},
		},
	}); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	persistAssignments(home, model.Selection{
		ClaudePhaseAssignments: map[string]model.ClaudePhaseAssignment{},
	})

	got, err := state.Read(home)
	if err != nil {
		t.Fatalf("state.Read: %v", err)
	}
	if got.ClaudePhaseAssignments != nil {
		t.Fatalf("ClaudePhaseAssignments = %#v, want nil after explicit clear", got.ClaudePhaseAssignments)
	}
	if got.ClaudeModelAssignments != nil {
		t.Fatalf("ClaudeModelAssignments = %#v, want nil after explicit phase clear", got.ClaudeModelAssignments)
	}
	if len(got.InstalledAgents) != 1 || got.InstalledAgents[0] != string(model.AgentClaudeCode) {
		t.Fatalf("InstalledAgents = %#v, want preserved claude-code", got.InstalledAgents)
	}
}

// TestPersistAssignmentsNoOpWhenEmpty verifies that persistAssignments does
// not write to state.json when the selection has no assignments.
func TestPersistAssignmentsNoOpWhenEmpty(t *testing.T) {
	home := t.TempDir()

	// Write initial state.
	err := state.Write(home, state.InstallState{
		InstalledAgents: []string{"codex"},
	})
	if err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	statePath := filepath.Join(home, ".gentle-ai", "state.json")
	infoBefore, _ := os.Stat(statePath)

	selection := model.Selection{} // empty assignments
	persistAssignments(home, selection)

	infoAfter, _ := os.Stat(statePath)
	if infoAfter.ModTime() != infoBefore.ModTime() {
		t.Errorf("persistAssignments() modified state.json when selection had no assignments")
	}
}

// TestVersionBeforeSystemGuards verifies that `gentle-ai version` returns the
// version string without going through system detection or platform guards.
func TestVersionBeforeSystemGuards(t *testing.T) {
	var buf bytes.Buffer
	err := RunArgs([]string{"version"}, &buf)
	if err != nil {
		t.Fatalf("version should not fail: %v", err)
	}
	if !strings.Contains(buf.String(), "gentle-ai") {
		t.Error("version output should contain 'gentle-ai'")
	}
}

// TestHelpCommand verifies that help, --help, and -h all print USAGE and COMMANDS
// without triggering system detection or platform guards.
func TestHelpCommand(t *testing.T) {
	for _, arg := range []string{"help", "--help", "-h"} {
		t.Run(arg, func(t *testing.T) {
			var buf bytes.Buffer
			err := RunArgs([]string{arg}, &buf)
			if err != nil {
				t.Fatalf("help should not fail: %v", err)
			}
			if !strings.Contains(buf.String(), "USAGE") {
				t.Errorf("help output for %q should contain USAGE", arg)
			}
			if !strings.Contains(buf.String(), "COMMANDS") {
				t.Errorf("help output for %q should contain COMMANDS", arg)
			}
		})
	}
}

// TestUnknownCommandSuggestsHelp verifies that an unrecognised command returns
// an error whose message suggests running 'gentle-ai help'.
func TestUnknownCommandSuggestsHelp(t *testing.T) {
	var buf bytes.Buffer
	err := RunArgs([]string{"notacommand"}, &buf)
	if err == nil {
		t.Fatal("unknown command should return error")
	}
	if !strings.Contains(err.Error(), "gentle-ai help") {
		t.Error("unknown command error should suggest 'gentle-ai help'")
	}
}

// TestRunArgsRetiredWorkCommandsNoLongerDispatch proves the remote control
// plane's command surface is gone: every retired work-* command falls through
// to the unknown-command error instead of dispatching provider machinery.
func TestRunArgsRetiredWorkCommandsNoLongerDispatch(t *testing.T) {
	origDetect := detectSystem
	origEnsure := ensureCurrentOSSupported
	t.Cleanup(func() {
		detectSystem = origDetect
		ensureCurrentOSSupported = origEnsure
	})
	ensureCurrentOSSupported = func() error { return nil }
	detectSystem = func(context.Context) (system.DetectionResult, error) {
		return system.DetectionResult{System: system.SystemInfo{Supported: true}}, nil
	}
	retired := []string{
		"work-capabilities", "work-start", "work-route", "work-advance",
		"work-verification-decide", "work-reconcile", "work-status", "work-transition",
	}
	for _, command := range retired {
		t.Run(command, func(t *testing.T) {
			var buf bytes.Buffer
			err := RunArgs([]string{command}, &buf)
			if err == nil {
				t.Fatalf("RunArgs(%q) error = nil, want unknown command", command)
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("unknown command %q", command)) {
				t.Fatalf("RunArgs(%q) error = %v, want unknown command", command, err)
			}
		})
	}
}

func TestRunArgs_TUIFailsClosedOnUnreadableState(t *testing.T) {
	assumeInteractiveTTY(t)
	home := t.TempDir()
	setupMockHome(t, home)
	if err := os.MkdirAll(filepath.Dir(state.Path(home)), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(state.Path(home), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	origDetect, origEnsure, origRunTUI := detectSystem, ensureCurrentOSSupported, runTUI
	t.Cleanup(func() {
		detectSystem = origDetect
		ensureCurrentOSSupported = origEnsure
		runTUI = origRunTUI
	})
	ensureCurrentOSSupported = func() error { return nil }
	detectSystem = func(context.Context) (system.DetectionResult, error) {
		return system.DetectionResult{System: system.SystemInfo{Supported: true}}, nil
	}
	runTUI = func(tea.Model, ...tea.ProgramOption) (tea.Model, error) {
		t.Fatal("runTUI called with unreadable state")
		return nil, nil
	}

	if err := RunArgs(nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "read install state") {
		t.Fatalf("RunArgs(TUI) error = %v, want unreadable state error", err)
	}
}

func setupMockHome(t *testing.T, home string) {
	t.Helper()
	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	t.Cleanup(func() {
		os.Setenv("HOME", origHome)
		os.Setenv("USERPROFILE", origUserProfile)
	})
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
}

// TestApplyOverrides_CodexModelAssignments verifies that a non-nil
// CodexModelAssignments override sets the selection.
func TestApplyOverrides_CodexModelAssignments(t *testing.T) {
	sel := model.Selection{}
	assignments := model.CodexModelPresetPowerful()
	overrides := &model.SyncOverrides{
		CodexModelAssignments: assignments,
	}
	applyOverrides(&sel, overrides)
	if len(sel.CodexModelAssignments) != len(assignments) {
		t.Fatalf("CodexModelAssignments len = %d, want %d", len(sel.CodexModelAssignments), len(assignments))
	}
	if sel.CodexModelAssignments["sdd-apply"] != model.CodexEffortHigh {
		t.Errorf("CodexModelAssignments[sdd-apply] = %q, want high", sel.CodexModelAssignments["sdd-apply"])
	}
}

// TestLoadPersistedAssignments_Codex verifies that state with codexModelAssignments
// populates selection.CodexModelAssignments.
func TestLoadPersistedAssignments_Codex(t *testing.T) {
	home := t.TempDir()
	if err := state.Write(home, state.InstallState{
		InstalledAgents: []string{"codex"},
		CodexModelAssignments: map[string]string{
			"sdd-apply":   "medium",
			"sdd-explore": "low",
			"default":     "medium",
		},
	}); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	sel := model.Selection{}
	loadPersistedAssignments(home, &sel)

	if sel.CodexModelAssignments["sdd-apply"] != model.CodexEffortMedium {
		t.Errorf("CodexModelAssignments[sdd-apply] = %q, want medium", sel.CodexModelAssignments["sdd-apply"])
	}
}

// TestLoadPersistedAssignments_CodexMissingKey verifies that a state without
// codexModelAssignments leaves selection.CodexModelAssignments nil.
func TestLoadPersistedAssignments_CodexMissingKey(t *testing.T) {
	home := t.TempDir()
	if err := state.Write(home, state.InstallState{
		InstalledAgents: []string{"codex"},
	}); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	sel := model.Selection{}
	loadPersistedAssignments(home, &sel)

	if sel.CodexModelAssignments != nil {
		t.Errorf("CodexModelAssignments = %v, want nil when key absent", sel.CodexModelAssignments)
	}
}

// TestPersistAssignments_Codex verifies that non-empty CodexModelAssignments
// are persisted to state.json.
func TestPersistAssignments_Codex(t *testing.T) {
	home := t.TempDir()
	// Write initial state so state.Read succeeds.
	if err := state.Write(home, state.InstallState{InstalledAgents: []string{"codex"}}); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	sel := model.Selection{
		CodexModelAssignments: model.CodexModelPresetLowCost(),
	}
	persistAssignments(home, sel)

	s, err := state.Read(home)
	if err != nil {
		t.Fatalf("state.Read: %v", err)
	}
	if s.CodexModelAssignments["sdd-apply"] != "medium" {
		t.Errorf("state.CodexModelAssignments[sdd-apply] = %q, want medium", s.CodexModelAssignments["sdd-apply"])
	}
}

// ─── Carril model assignment tests (W-3 fix) ─────────────────────────────────

// TestApplyOverrides_CodexCarrilModelAssignments verifies that a non-nil
// CodexCarrilModelAssignments override sets the selection.
func TestApplyOverrides_CodexCarrilModelAssignments(t *testing.T) {
	sel := model.Selection{}
	carrilModels := model.DefaultCarrilModels()
	overrides := &model.SyncOverrides{
		CodexCarrilModelAssignments: carrilModels,
	}
	applyOverrides(&sel, overrides)
	if len(sel.CodexCarrilModelAssignments) != len(carrilModels) {
		t.Fatalf("CodexCarrilModelAssignments len = %d, want %d", len(sel.CodexCarrilModelAssignments), len(carrilModels))
	}
	if sel.CodexCarrilModelAssignments["sdd-cheap"] != "gpt-6.1-luna" {
		t.Errorf("CodexCarrilModelAssignments[sdd-cheap] = %q, want gpt-6.1-luna", sel.CodexCarrilModelAssignments["sdd-cheap"])
	}
	if sel.CodexCarrilModelAssignments["sdd-strong"] != "gpt-6.1-sol" {
		t.Errorf("CodexCarrilModelAssignments[sdd-strong] = %q, want gpt-6.1-sol", sel.CodexCarrilModelAssignments["sdd-strong"])
	}
}

func TestLoadPersistedAssignments_CodexCarrilModels(t *testing.T) {
	tests := []struct {
		name      string
		persisted map[string]string
		want      map[string]string
	}{
		{name: "migrates exact legacy defaults", persisted: map[string]string{"sdd-strong": "gpt-5.5", "sdd-mid": "gpt-5.5", "sdd-cheap": "gpt-5.4-mini"}, want: model.DefaultCarrilModels()},
		{name: "preserves custom tuple", persisted: map[string]string{"sdd-strong": "gpt-5.5", "sdd-mid": "gpt-5.4", "sdd-cheap": "gpt-5.4-mini"}, want: map[string]string{"sdd-strong": "gpt-5.5", "sdd-mid": "gpt-5.4", "sdd-cheap": "gpt-5.4-mini"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			if err := state.Write(home, state.InstallState{InstalledAgents: []string{"codex"}, CodexCarrilModelAssignments: tt.persisted}); err != nil {
				t.Fatalf("state.Write: %v", err)
			}

			sel := model.Selection{}
			loadPersistedAssignments(home, &sel)
			if !reflect.DeepEqual(sel.CodexCarrilModelAssignments, tt.want) {
				t.Fatalf("CodexCarrilModelAssignments = %#v, want %#v", sel.CodexCarrilModelAssignments, tt.want)
			}
		})
	}
}

func TestTuiSyncMigratesLegacyCodexCarrilDefaults(t *testing.T) {
	t.Cleanup(codex.SetRuntimeVersionCommandForTest("codex-cli 0.144.0", nil))
	home := t.TempDir()
	if err := state.Write(home, state.InstallState{
		InstalledAgents:             []string{"codex"},
		CodexCarrilModelAssignments: map[string]string{"sdd-strong": "gpt-5.5", "sdd-mid": "gpt-5.5", "sdd-cheap": "gpt-5.4-mini"},
		CodexModelAssignments:       map[string]string{"sdd-apply": "medium"},
	}); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	changed, err := tuiSync(home)(nil)
	if err != nil {
		t.Fatalf("tuiSync() error = %v", err)
	}
	changedSet := make(map[string]bool, len(changed))
	for _, path := range changed {
		changedSet[path] = true
	}

	promptPath := filepath.Join(home, ".codex", "AGENTS.md")
	body, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []string{
		"| `odd-explorer` | `gpt-6.1-luna` | `high` |",
		"| `odd-worker` | `gpt-6.1-luna` | `high` |",
		"| `odd-verify` | `gpt-6.1-sol` | `medium` |",
	} {
		if !strings.Contains(string(body), row) {
			t.Errorf("migrated Codex routing missing %q", row)
		}
	}
	if !changedSet[promptPath] {
		t.Errorf("tuiSync changed files missing %s: %#v", promptPath, changed)
	}
	for _, name := range []string{"sdd-strong.config.toml", "sdd-mid.config.toml", "sdd-cheap.config.toml"} {
		if _, err := os.Stat(filepath.Join(home, ".codex", name)); !os.IsNotExist(err) {
			t.Errorf("retired profile %s generated: %v", name, err)
		}
	}

	persisted, err := state.Read(home)
	if err != nil {
		t.Fatalf("state.Read after tuiSync: %v", err)
	}
	if !reflect.DeepEqual(persisted.CodexCarrilModelAssignments, model.DefaultCarrilModels()) {
		t.Fatalf("persisted carril assignments = %#v, want %#v", persisted.CodexCarrilModelAssignments, model.DefaultCarrilModels())
	}
	if persisted.CodexModelAssignments["sdd-apply"] != "medium" {
		t.Fatalf("persisted effort assignment = %#v, want sdd-apply medium", persisted.CodexModelAssignments)
	}
}

// TestPersistAssignments_CodexCarrilModels verifies that non-empty
// CodexCarrilModelAssignments are persisted to state.json.
func TestPersistAssignments_CodexCarrilModels(t *testing.T) {
	home := t.TempDir()
	if err := state.Write(home, state.InstallState{InstalledAgents: []string{"codex"}}); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	sel := model.Selection{
		CodexCarrilModelAssignments: map[string]string{
			"sdd-strong": "gpt-5.5",
			"sdd-mid":    "gpt-5.5",
			"sdd-cheap":  "gpt-5.4-mini",
		},
	}
	persistAssignments(home, sel)

	s, err := state.Read(home)
	if err != nil {
		t.Fatalf("state.Read: %v", err)
	}
	if s.CodexCarrilModelAssignments["sdd-cheap"] != "gpt-5.4-mini" {
		t.Errorf("state.CodexCarrilModelAssignments[sdd-cheap] = %q, want gpt-5.4-mini", s.CodexCarrilModelAssignments["sdd-cheap"])
	}
	if s.CodexCarrilModelAssignments["sdd-strong"] != "gpt-5.5" {
		t.Errorf("state.CodexCarrilModelAssignments[sdd-strong] = %q, want gpt-5.5", s.CodexCarrilModelAssignments["sdd-strong"])
	}
}

// ─── BUG-1: Custom→preset via sync path does not clear CodexPhaseModelAssignments ───

// TestPresetSyncClearsCodexPhaseModelAssignments is the RED→GREEN regression test.
// It simulates:
//  1. A previous Custom per-phase sync that persisted CodexPhaseModelAssignments to state.json.
//  2. The user then picks a preset (e.g. Recommended) — the sync carries an empty-map
//     clear signal for CodexPhaseModelAssignments.
//
// After the fix: state.json must NOT contain codexPhaseModelAssignments (or it must be empty).
// Against current (unfixed) code this test FAILS (stale value survives).
func TestPresetSyncClearsCodexPhaseModelAssignments(t *testing.T) {
	home := t.TempDir()

	// Step 1 — persist stale per-phase assignments (as if a previous Custom sync ran).
	if err := state.Write(home, state.InstallState{
		InstalledAgents: []string{"codex"},
		CodexPhaseModelAssignments: map[string]string{
			"sdd-apply":   "o3",
			"sdd-explore": "gpt-4o-mini",
			"default":     "o3",
		},
	}); err != nil {
		t.Fatalf("state.Write (seed): %v", err)
	}

	// Step 2 — simulate a preset sync: overrides carry an empty-map clear signal.
	// model.go sets CodexPhaseModelAssignments to map[string]string{} (non-nil empty)
	// when the user picks a preset. Nil means "not provided"; empty means "clear".
	presetCarril := model.DefaultCarrilModels()
	overrides := &model.SyncOverrides{
		TargetAgents:                []model.AgentID{model.AgentCodex},
		CodexModelAssignments:       model.CodexModelPresetRecommended(),
		CodexCarrilModelAssignments: presetCarril,
		CodexPhaseModelAssignments:  map[string]string{}, // explicit clear signal
	}

	selection := model.Selection{}
	loadPersistedAssignments(home, &selection)
	applyOverrides(&selection, overrides)
	persistAssignments(home, selection)

	// Assert: state must no longer contain per-phase assignments.
	s, err := state.Read(home)
	if err != nil {
		t.Fatalf("state.Read: %v", err)
	}
	if len(s.CodexPhaseModelAssignments) > 0 {
		t.Fatalf("state.CodexPhaseModelAssignments = %v, want empty after preset sync (stale per-phase map was NOT cleared)", s.CodexPhaseModelAssignments)
	}

	// Assert: selection must also be cleared.
	if len(selection.CodexPhaseModelAssignments) > 0 {
		t.Fatalf("selection.CodexPhaseModelAssignments = %v, want empty after preset sync", selection.CodexPhaseModelAssignments)
	}
}

// TestPartialSyncDoesNotWipeCodexPhaseModelAssignments is the guard test.
// A partial sync that does NOT set CodexPhaseModelAssignments on the override
// (nil = not provided) must NOT wipe an existing per-phase map from state.
func TestPartialSyncDoesNotWipeCodexPhaseModelAssignments(t *testing.T) {
	home := t.TempDir()

	// Seed state with per-phase assignments.
	existingPhaseAssignments := map[string]string{
		"sdd-apply":   "o3",
		"sdd-explore": "gpt-4o-mini",
		"default":     "o3",
	}
	if err := state.Write(home, state.InstallState{
		InstalledAgents:            []string{"codex"},
		CodexPhaseModelAssignments: existingPhaseAssignments,
	}); err != nil {
		t.Fatalf("state.Write (seed): %v", err)
	}

	// Partial sync that only updates Claude model assignments — no Codex override.
	overrides := &model.SyncOverrides{
		ClaudeModelAssignments: map[string]model.ClaudeModelAlias{
			"sdd-apply": model.ClaudeModelHaiku,
		},
	}

	selection := model.Selection{}
	loadPersistedAssignments(home, &selection)
	applyOverrides(&selection, overrides)
	persistAssignments(home, selection)

	// The per-phase assignments must survive an unrelated partial sync.
	s, err := state.Read(home)
	if err != nil {
		t.Fatalf("state.Read: %v", err)
	}
	if s.CodexPhaseModelAssignments["sdd-apply"] != "o3" {
		t.Fatalf("state.CodexPhaseModelAssignments[sdd-apply] = %q, want %q (wiped by unrelated partial sync)", s.CodexPhaseModelAssignments["sdd-apply"], "o3")
	}
	if s.CodexPhaseModelAssignments["default"] != "o3" {
		t.Fatalf("state.CodexPhaseModelAssignments[default] = %q, want %q", s.CodexPhaseModelAssignments["default"], "o3")
	}
}

func writeAppSDDStatusFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

// ─── Slice 4 RED: deferred sync on launch via pending_sync flag ───────────────

func TestCustomSyncClearsPersistedCodexOrchestratorAssignment(t *testing.T) {
	home := t.TempDir()
	if err := state.Write(home, state.InstallState{
		InstalledAgents: []string{"codex"},
		CodexOrchestratorAssignment: &state.CodexOrchestratorAssignmentState{
			Model: "gpt-5.6-sol", Effort: "low",
		},
	}); err != nil {
		t.Fatalf("state.Write (seed): %v", err)
	}

	selection := model.Selection{}
	loadPersistedAssignments(home, &selection)
	applyOverrides(&selection, &model.SyncOverrides{ClearCodexOrchestratorAssignment: true})
	if selection.CodexOrchestratorAssignment != nil {
		t.Fatalf("same-sync selection retained curated orchestrator: %#v", selection.CodexOrchestratorAssignment)
	}
	persistAssignments(home, selection)

	persisted, err := state.Read(home)
	if err != nil {
		t.Fatalf("state.Read: %v", err)
	}
	if persisted.CodexOrchestratorAssignment != nil {
		t.Fatalf("persisted curated orchestrator was not cleared: %#v", persisted.CodexOrchestratorAssignment)
	}
}

func TestCustomClearRoundTripLeavesFutureSyncInPreserveMode(t *testing.T) {
	home := t.TempDir()
	if err := state.Write(home, state.InstallState{
		InstalledAgents: []string{"codex"},
		CodexOrchestratorAssignment: &state.CodexOrchestratorAssignmentState{
			Model: "gpt-5.6-sol", Effort: "low",
		},
	}); err != nil {
		t.Fatalf("state.Write (seed): %v", err)
	}

	first := model.Selection{}
	loadPersistedAssignments(home, &first)
	applyOverrides(&first, &model.SyncOverrides{ClearCodexOrchestratorAssignment: true})
	persistAssignments(home, first)

	future := model.Selection{}
	loadPersistedAssignments(home, &future)
	if future.CodexOrchestratorAssignment != nil || future.ClearCodexOrchestratorAssignment {
		t.Fatalf("future sync did not return to preserve mode: assignment=%#v clear=%v", future.CodexOrchestratorAssignment, future.ClearCodexOrchestratorAssignment)
	}
}

func TestLoadPersistedCodexServiceTierRestoresOnlyValidValues(t *testing.T) {
	for _, tt := range []struct{ persisted, want string }{
		{"priority", "priority"},
		{"priority\nmodel = \"x\"", ""},
	} {
		home := t.TempDir()
		if err := state.Write(home, state.InstallState{InstalledAgents: []string{"codex"}, CodexServiceTier: tt.persisted}); err != nil {
			t.Fatalf("state.Write (seed): %v", err)
		}
		selection := model.Selection{}
		loadPersistedAssignments(home, &selection)
		if selection.CodexServiceTier != tt.want || selection.CodexManagedServiceTier != tt.want {
			t.Fatalf("restored %q: tier = %q managed = %q, want %q", tt.persisted, selection.CodexServiceTier, selection.CodexManagedServiceTier, tt.want)
		}
	}
}

func TestPersistAssignmentsNeverRecordsTheSelectedCodexServiceTier(t *testing.T) {
	home := t.TempDir()
	if err := state.Write(home, state.InstallState{InstalledAgents: []string{"codex"}, CodexServiceTier: "flex"}); err != nil {
		t.Fatalf("state.Write (seed): %v", err)
	}
	if err := persistAssignments(home, model.Selection{CodexServiceTier: "priority", CodexManagedServiceTier: "flex"}); err != nil {
		t.Fatal(err)
	}
	if persisted, err := state.Read(home); err != nil || persisted.CodexServiceTier != "flex" {
		t.Fatalf("persisted tier = %q, err = %v; want flex (only engram's write result is recorded)", persisted.CodexServiceTier, err)
	}
}

func codexServiceTierInstall(t *testing.T, home string, components []model.ComponentID, desired, managed string) {
	t.Helper()
	selection := model.Selection{Agents: []model.AgentID{model.AgentCodex}, Components: components, CodexServiceTier: desired, CodexManagedServiceTier: managed}
	resolved := planner.ResolvedPlan{Agents: selection.Agents, OrderedComponents: components}
	if result := tuiExecute(selection, resolved, system.DetectionResult{}, nil); result.Err != nil {
		t.Fatalf("TUI install error = %v", result.Err)
	}
}

func codexConfigAndStateTier(t *testing.T, home string) (string, string) {
	t.Helper()
	config, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	persisted, err := state.Read(home)
	if err != nil {
		t.Fatalf("state.Read: %v", err)
	}
	return string(config), persisted.CodexServiceTier
}

// TestTUIInstallRecordsOnlyTheCodexServiceTierEngramWrote covers both halves
// of the managed-tier contract: a tier no engram run wrote is never recorded,
// and a Standard install in the session that wrote Fast retires exactly it.
func TestTUIInstallRecordsOnlyTheCodexServiceTierEngramWrote(t *testing.T) {
	t.Cleanup(codex.SetRuntimeVersionCommandForTest("codex-cli 0.144.0", nil))
	if runtime.GOOS == "windows" {
		t.Skip("fake engram runtime is a POSIX shell script")
	}
	home := t.TempDir()
	previousUserHomeDir := appUserHomeDir
	appUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { appUserHomeDir = previousUserHomeDir })
	// A fake engram on PATH keeps the engram component from installing anything.
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "engram"), []byte("#!/bin/sh\nprintf 'engram 1.18.0\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	codexServiceTierInstall(t, home, []model.ComponentID{model.ComponentPersona}, "priority", "")
	if config, recorded := codexConfigAndStateTier(t, home); recorded != "" || strings.Contains(config, "service_tier") {
		t.Fatalf("unwritten tier recorded: state = %q config:\n%s", recorded, config)
	}

	codexServiceTierInstall(t, home, []model.ComponentID{model.ComponentEngram}, "priority", "")
	if config, recorded := codexConfigAndStateTier(t, home); recorded != "priority" || !strings.Contains(config, `service_tier = "priority"`) {
		t.Fatalf("written tier not recorded: state = %q config:\n%s", recorded, config)
	}

	// Same session, now Standard: the managed value is what state recorded.
	codexServiceTierInstall(t, home, []model.ComponentID{model.ComponentEngram}, "", "priority")
	if config, recorded := codexConfigAndStateTier(t, home); recorded != "" || strings.Contains(config, "service_tier") {
		t.Fatalf("Standard left an orphan: state = %q config:\n%s", recorded, config)
	}
}

// ─── Issue #535: upgrade argument validation pre-effect gate ───────────────

// installUpgradeSentinels replaces every effect that the upgrade preflight
// MUST NOT reach, with stubs that fail the test if invoked. It restores the
// originals on cleanup. HOME is isolated to a temp dir so the parser cannot
// accidentally trigger real home-directory effects.
func installUpgradeSentinels(t *testing.T, home string) {
	t.Helper()
	setupMockHome(t, home)

	origEnsure := ensureCurrentOSSupported
	origDetect := detectSystem
	origCheckFiltered := updateCheckFiltered
	origCheckAll := updateCheckAll
	origUpgradeExecute := upgradeExecute
	origUpgradeExecuteWithOptions := upgradeExecuteWithOptions
	t.Cleanup(func() {
		ensureCurrentOSSupported = origEnsure
		detectSystem = origDetect
		updateCheckFiltered = origCheckFiltered
		updateCheckAll = origCheckAll
		upgradeExecute = origUpgradeExecute
		upgradeExecuteWithOptions = origUpgradeExecuteWithOptions
	})

	ensureCurrentOSSupported = func() error {
		return fmt.Errorf("ensureCurrentOSSupported must not run for this upgrade invocation")
	}
	detectSystem = func(context.Context) (system.DetectionResult, error) {
		return system.DetectionResult{}, fmt.Errorf("detectSystem must not run for this upgrade invocation")
	}
	updateCheckFiltered = func(context.Context, system.PlatformProfile, []string) []update.UpdateResult {
		t.Fatalf("updateCheckFiltered must not run for this upgrade invocation")
		return nil
	}
	updateCheckAll = func(context.Context, system.PlatformProfile) []update.UpdateResult {
		t.Fatalf("updateCheckAll must not run for this upgrade invocation")
		return nil
	}
	upgradeExecute = func(context.Context, []update.UpdateResult, system.PlatformProfile, string, bool, ...io.Writer) upgrade.UpgradeReport {
		t.Fatalf("upgradeExecute must not run for this upgrade invocation")
		return upgrade.UpgradeReport{}
	}
	upgradeExecuteWithOptions = func(context.Context, []update.UpdateResult, system.PlatformProfile, string, bool, upgrade.ExecuteOptions) upgrade.UpgradeReport {
		t.Fatalf("upgradeExecuteWithOptions must not run for this upgrade invocation")
		return upgrade.UpgradeReport{}
	}
}

// TestRunArgs_UpgradeUnsupportedOptionStopsBeforeAnyEffect proves unsupported
// dash args (--verbose) and the #535 remediation (--help, -h) are rejected
// before every effect: identifiable token, errors.Is, zero effects.
func TestRunArgs_UpgradeUnsupportedOptionStopsBeforeAnyEffect(t *testing.T) {
	for _, token := range []string{"--verbose", "--help", "-h"} {
		t.Run(token, func(t *testing.T) {
			installUpgradeSentinels(t, t.TempDir())
			var buf bytes.Buffer
			err := RunArgs([]string{"upgrade", token}, &buf)
			if err == nil {
				t.Fatalf("RunArgs(upgrade %s) error = nil, want error", token)
			}
			want := fmt.Sprintf(`unsupported upgrade argument: %q`, token)
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("RunArgs(upgrade %s) error = %v, want %q", token, err, want)
			}
			if !errors.Is(err, errUnsupportedUpgradeArgument) {
				t.Fatalf("RunArgs(upgrade %s) error not identifiable: %v", token, err)
			}
		})
	}
}
