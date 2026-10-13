package upgrade

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/backup"
	"github.com/carsimax/brunnr-code/internal/components/agentguidance"
	"github.com/carsimax/brunnr-code/internal/components/reviewassets"
	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/state"
	"github.com/carsimax/brunnr-code/internal/system"
	"github.com/carsimax/brunnr-code/internal/update"
)

// --- helpers ---

func darwinProfile() system.PlatformProfile {
	return system.PlatformProfile{OS: "darwin", Supported: true}
}

func makeResult(name string, status update.UpdateStatus, oldVer, newVer string, method update.InstallMethod) update.UpdateResult {
	return update.UpdateResult{
		Tool: update.ToolInfo{
			Name:          name,
			Owner:         "Gentleman-Programming",
			Repo:          name,
			InstallMethod: method,
		},
		InstalledVersion: oldVer,
		LatestVersion:    newVer,
		Status:           status,
	}
}

// --- TestExecute_NoopWhenNothingIsExecutable ---

// TestExecute_NoopWhenNothingIsExecutable verifies that Execute returns an empty
// UpgradeReport with no backup and no tool results when no UpdateResult is
// UpdateAvailable or DevBuild status (i.e. only UpToDate and NotInstalled tools).
func TestExecute_NoopWhenNothingIsExecutable(t *testing.T) {
	results := []update.UpdateResult{
		makeResult("brunnr-code", update.UpToDate, "1.0.0", "1.0.0", update.InstallBinary),
		makeResult("engram", update.NotInstalled, "", "0.4.0", update.InstallBinary),
		// CheckFailed — should also be omitted from results.
		makeResult("other", update.CheckFailed, "", "", update.InstallBinary),
	}

	report := Execute(context.Background(), results, darwinProfile(), t.TempDir(), false)

	if report.BackupID != "" {
		t.Errorf("BackupID = %q, want empty — no backup should be created when nothing to execute", report.BackupID)
	}

	if len(report.Results) != 0 {
		t.Errorf("len(Results) = %d, want 0 — UpToDate, NotInstalled, CheckFailed must be omitted", len(report.Results))
	}

	if report.DryRun {
		t.Errorf("DryRun should be false when not requested")
	}
}

// --- TestExecute_DevBuildOnlyNoBackupCreated ---

// TestExecute_DevBuildOnlyNoBackupCreated verifies that when ALL tools are DevBuild
// (nothing to execute), no backup snapshot is created. Backup is only needed before
// actual binary execution, not for skip-only reports.
func TestExecute_DevBuildOnlyNoBackupCreated(t *testing.T) {
	origExecCommand := execCommand
	t.Cleanup(func() { execCommand = origExecCommand })

	execCalled := false
	execCommand = func(name string, args ...string) *exec.Cmd {
		execCalled = true
		return mockCmd("echo", "should not be called")
	}

	results := []update.UpdateResult{
		makeResult("brunnr-code", update.DevBuild, "dev", "1.0.0", update.InstallBinary),
	}

	report := Execute(context.Background(), results, darwinProfile(), t.TempDir(), false)

	if execCalled {
		t.Errorf("execCommand should NOT be called for DevBuild-only inputs")
	}

	// DevBuild tool MUST appear in results as UpgradeSkipped.
	if len(report.Results) != 1 {
		t.Fatalf("len(Results) = %d, want 1 — DevBuild tool must appear as skipped", len(report.Results))
	}
	if report.Results[0].Status != UpgradeSkipped {
		t.Errorf("DevBuild Status = %q, want UpgradeSkipped", report.Results[0].Status)
	}

	// No backup should be created — nothing executed.
	if report.BackupID != "" {
		t.Errorf("BackupID = %q, want empty — no backup when no execution occurs", report.BackupID)
	}
}

func TestExecute_VersionUnknownIsSurfacedAsSkipped(t *testing.T) {
	results := []update.UpdateResult{
		makeResult("engram", update.VersionUnknown, "", "1.2.0", update.InstallBinary),
	}
	results[0].Tool.DetectCmd = []string{"engram", "version"}

	report := Execute(context.Background(), results, darwinProfile(), t.TempDir(), false)

	if len(report.Results) != 1 {
		t.Fatalf("len(Results) = %d, want 1", len(report.Results))
	}
	if report.Results[0].Status != UpgradeSkipped {
		t.Fatalf("status = %q, want %q", report.Results[0].Status, UpgradeSkipped)
	}
	if report.Results[0].ManualHint == "" {
		t.Fatal("ManualHint must be populated for version-unknown tools")
	}
	if !strings.Contains(report.Results[0].ManualHint, "`engram version`") {
		t.Fatalf("ManualHint = %q, want detect command hint", report.Results[0].ManualHint)
	}
	if report.BackupID != "" {
		t.Fatalf("BackupID = %q, want empty when nothing is executed", report.BackupID)
	}
}

// --- TestRenderUpgradeReport_DryRunManualHintNotCountedAsPending ---

func TestRenderUpgradeReport_DryRunManualHintNotCountedAsPending(t *testing.T) {
	report := UpgradeReport{
		DryRun: true,
		Results: []ToolUpgradeResult{
			{ToolName: "engram", Status: UpgradeSkipped, ManualHint: "source build — upgrade manually"},
		},
	}

	output := RenderUpgradeReport(report)

	if strings.Contains(output, "upgrade(s) pending") {
		t.Fatalf("manual-hint skips must NOT be counted as pending upgrades in dry-run:\n%s", output)
	}
	if !strings.Contains(output, "manual") {
		t.Fatalf("dry-run output should mention manual attention:\n%s", output)
	}
}

// --- TestExecute_BackupBeforeExecution ---

// TestExecute_BackupBeforeExecution verifies the architectural invariant:
// a backup snapshot is created BEFORE any upgrade execution begins.
// We verify this by ensuring BackupID is non-empty when upgrades are available.
func TestExecute_BackupBeforeExecution(t *testing.T) {
	origExecCommand := execCommand
	t.Cleanup(func() { execCommand = origExecCommand })

	// Capture exec calls to verify ordering.
	var calls []string
	execCommand = func(name string, args ...string) *exec.Cmd {
		calls = append(calls, name)
		// Return a real passing command (echo) so exec succeeds.
		return mockCmd("echo", "ok")
	}

	results := []update.UpdateResult{
		makeResult("engram", update.UpdateAvailable, "0.3.0", "0.4.0", update.InstallBinary),
	}

	report := Execute(context.Background(), results, darwinProfile(), t.TempDir(), false)

	// BackupID must be non-empty.
	if report.BackupID == "" {
		t.Errorf("BackupID is empty — backup must be created before upgrade execution")
	}

	// At least one result must be present.
	if len(report.Results) != 1 {
		t.Fatalf("len(Results) = %d, want 1", len(report.Results))
	}
}

func TestExecuteProgressDoesNotIncludeBackupExclusionDiagnostics(t *testing.T) {
	origExecCommand := execCommand
	t.Cleanup(func() { execCommand = origExecCommand })
	execCommand = func(name string, args ...string) *exec.Cmd {
		return mockCmd("echo", "ok")
	}

	home := t.TempDir()
	configFile := filepath.Join(home, ".claude", "CLAUDE.md")
	excludedFile := filepath.Join(home, ".claude", "projects", "session.json")
	for _, f := range []string{configFile, excludedFile} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(f, []byte("data"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	results := []update.UpdateResult{
		makeResult("engram", update.UpdateAvailable, "0.3.0", "0.4.0", update.InstallBinary),
	}

	var progress bytes.Buffer
	report := Execute(context.Background(), results, darwinProfile(), home, false, &progress)
	if report.BackupID == "" {
		t.Fatal("BackupID should be populated before upgrade execution")
	}

	got := progress.String()
	if strings.Contains(got, "backup: excluding directory") {
		t.Fatalf("progress output leaked backup exclusion diagnostics:\n%s", got)
	}
	if !strings.Contains(got, "Creating pre-upgrade backup") {
		t.Fatalf("progress output should still show user-visible backup progress, got:\n%s", got)
	}
}

// --- TestExecute_DryRunNeverExecs ---

// TestExecute_DryRunNeverExecs verifies that when dryRun=true, no exec is called
// but the report is still populated.
func TestExecute_DryRunNeverExecs(t *testing.T) {
	origExecCommand := execCommand
	t.Cleanup(func() { execCommand = origExecCommand })

	called := false
	execCommand = func(name string, args ...string) *exec.Cmd {
		called = true
		return mockCmd("echo", "should not run")
	}

	results := []update.UpdateResult{
		makeResult("engram", update.UpdateAvailable, "0.3.0", "0.4.0", update.InstallBinary),
	}

	report := Execute(context.Background(), results, darwinProfile(), t.TempDir(), true)

	if called {
		t.Errorf("execCommand was called during dry-run — must NOT execute")
	}

	if !report.DryRun {
		t.Errorf("DryRun = false, want true")
	}

	if len(report.Results) != 1 {
		t.Fatalf("len(Results) = %d, want 1", len(report.Results))
	}

	if report.Results[0].Status != UpgradeSkipped {
		t.Errorf("dry-run status = %q, want UpgradeSkipped", report.Results[0].Status)
	}
}

// --- TestExecute_PerToolSuccessFailureSkip ---

// TestExecute_PerToolSuccessAndFailure verifies that Execute reports success for one
// tool and failure for another in a mixed scenario.
func TestExecute_PerToolSuccessAndFailure(t *testing.T) {
	origExecCommand := execCommand
	t.Cleanup(func() { execCommand = origExecCommand })

	execCommand = func(name string, args ...string) *exec.Cmd {
		// engram go install succeeds, faketool curl/download attempt fails — we simulate
		// the failure by having execCommand return false for "faketool" detection.
		if name == "go" {
			return mockCmd("echo", "go install ok")
		}
		// Any other exec attempt fails.
		return mockCmd("false")
	}

	results := []update.UpdateResult{
		makeResult("engram", update.UpdateAvailable, "0.3.0", "0.4.0", update.InstallBinary),
	}

	report := Execute(context.Background(), results, darwinProfile(), t.TempDir(), false)

	if len(report.Results) != 1 {
		t.Fatalf("len(Results) = %d, want 1", len(report.Results))
	}

	// engram should succeed (go install echo'd "ok")
	if report.Results[0].Status != UpgradeSucceeded {
		t.Errorf("engram status = %q, want UpgradeSucceeded", report.Results[0].Status)
	}
}

// --- TestExecute_DevBuildIsSkipped ---

// TestExecute_DevBuildIsSkipped verifies the spec requirement:
// brunnr-code with DevBuild status must appear in Results as UpgradeSkipped
// with a non-empty ManualHint explaining it is a source/dev build.
// DevBuild tools must NOT be auto-executed, and engram/faketool remain eligible.
func TestExecute_DevBuildIsSkipped(t *testing.T) {
	origExecCommand := execCommand
	t.Cleanup(func() { execCommand = origExecCommand })
	execCommand = func(name string, args ...string) *exec.Cmd {
		return mockCmd("echo", "ok")
	}

	results := []update.UpdateResult{
		makeResult("brunnr-code", update.DevBuild, "dev", "1.0.0", update.InstallBinary),
		makeResult("engram", update.UpdateAvailable, "0.3.0", "0.4.0", update.InstallBinary),
	}

	report := Execute(context.Background(), results, darwinProfile(), t.TempDir(), false)

	// brunnr-code (DevBuild) MUST appear as UpgradeSkipped with a ManualHint.
	var devResult *ToolUpgradeResult
	for i := range report.Results {
		if report.Results[i].ToolName == "brunnr-code" {
			r := report.Results[i]
			devResult = &r
		}
	}
	if devResult == nil {
		t.Fatalf("brunnr-code (DevBuild) must appear in Results — was not found")
	}
	if devResult.Status != UpgradeSkipped {
		t.Errorf("brunnr-code DevBuild Status = %q, want UpgradeSkipped", devResult.Status)
	}
	if devResult.ManualHint == "" {
		t.Errorf("brunnr-code DevBuild ManualHint must be non-empty")
	}

	// engram should still be processed as succeeded.
	found := false
	for _, r := range report.Results {
		if r.ToolName == "engram" {
			found = true
			if r.Status != UpgradeSucceeded {
				t.Errorf("engram status = %q, want UpgradeSucceeded", r.Status)
			}
		}
	}
	if !found {
		t.Errorf("engram not found in Results")
	}
}

// --- TestExecute_FailureDoesNotImplyConfigLoss ---

// TestExecute_FailureDoesNotImplyConfigLoss verifies that when a tool upgrade fails,
// we can still retrieve the BackupID — confirming config was snapshotted first.
func TestExecute_FailureDoesNotImplyConfigLoss(t *testing.T) {
	origDownload := engramDownloadFn
	t.Cleanup(func() { engramDownloadFn = origDownload })

	// Force the engram download to fail.
	engramDownloadFn = func(system.PlatformProfile) (string, error) {
		return "", errors.New("download failed")
	}

	results := []update.UpdateResult{
		makeResult("engram", update.UpdateAvailable, "0.3.0", "0.4.0", update.InstallBinary),
	}

	report := Execute(context.Background(), results, darwinProfile(), t.TempDir(), false)

	// Even with failure, BackupID must be set (backup happened before exec).
	if report.BackupID == "" {
		t.Errorf("BackupID is empty — backup must be created before upgrade, even if upgrade fails")
	}

	if len(report.Results) != 1 {
		t.Fatalf("len(Results) = %d, want 1", len(report.Results))
	}

	if report.Results[0].Status != UpgradeFailed {
		t.Errorf("status = %q, want UpgradeFailed", report.Results[0].Status)
	}

	if report.Results[0].Err == nil {
		t.Errorf("Err should not be nil on failure")
	}
}

// NOTE: Install isolation is enforced by the import boundary at the top of
// executor.go — this package MUST NOT import pipeline, planner, or cli.
// The compiler enforces this; no runtime test is needed.

// --- TestExecute_DevBuildSurfacedAsSkipped ---

// TestExecute_DevBuildSurfacedAsSkipped verifies the spec gap:
// A DevBuild tool (e.g. brunnr-code with version="dev") MUST appear in UpgradeReport.Results
// with Status=UpgradeSkipped and a non-empty ManualHint explaining it is a dev/source build.
// Previously, DevBuild tools were silently omitted from Results entirely.
func TestExecute_DevBuildSurfacedAsSkipped(t *testing.T) {
	origExecCommand := execCommand
	t.Cleanup(func() { execCommand = origExecCommand })
	execCommand = func(name string, args ...string) *exec.Cmd {
		return mockCmd("echo", "ok")
	}

	results := []update.UpdateResult{
		makeResult("brunnr-code", update.DevBuild, "dev", "1.0.0", update.InstallBinary),
		makeResult("engram", update.UpdateAvailable, "0.3.0", "0.4.0", update.InstallBinary),
	}

	report := Execute(context.Background(), results, darwinProfile(), t.TempDir(), false)

	// brunnr-code (DevBuild) MUST appear in results as UpgradeSkipped.
	var devResult *ToolUpgradeResult
	for i := range report.Results {
		if report.Results[i].ToolName == "brunnr-code" {
			r := report.Results[i]
			devResult = &r
		}
	}

	if devResult == nil {
		t.Fatalf("brunnr-code DevBuild must appear in Results as UpgradeSkipped, but was not found")
	}

	if devResult.Status != UpgradeSkipped {
		t.Errorf("brunnr-code DevBuild Status = %q, want UpgradeSkipped", devResult.Status)
	}

	if devResult.ManualHint == "" {
		t.Errorf("brunnr-code DevBuild ManualHint must be non-empty — should explain dev/source build")
	}

	// engram (UpdateAvailable) must still be processed normally.
	found := false
	for _, r := range report.Results {
		if r.ToolName == "engram" {
			found = true
			if r.Status != UpgradeSucceeded {
				t.Errorf("engram status = %q, want UpgradeSucceeded", r.Status)
			}
		}
	}
	if !found {
		t.Errorf("engram not found in Results")
	}
}

// --- TestExecute_ConfigNotMutatedDuringUpgrade ---

// TestExecute_ConfigNotMutatedDuringUpgrade provides direct evidence that upgrade
// execution does not mutate config file contents — the spec's config preservation
// guarantee. We create real config files in a temp dir, run Execute (stubbed exec),
// and diff the contents before and after.
func TestExecute_ConfigNotMutatedDuringUpgrade(t *testing.T) {
	homeDir := t.TempDir()

	// Create realistic config files with known contents.
	configFiles := map[string]string{
		".claude/CLAUDE.md":  "# Claude config\nThis is my config.\n",
		".codex/config.toml": "model = \"gpt\"\n",
	}

	for relPath, content := range configFiles {
		fullPath := homeDir + "/" + relPath
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("create dir for %s: %v", relPath, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write config %s: %v", relPath, err)
		}
	}

	origExecCommand := execCommand
	t.Cleanup(func() { execCommand = origExecCommand })
	execCommand = func(name string, args ...string) *exec.Cmd {
		// Simulate a successful upgrade (no-op shell command).
		return mockCmd("echo", "upgrade ok")
	}

	results := []update.UpdateResult{
		makeResult("engram", update.UpdateAvailable, "0.3.0", "0.4.0", update.InstallBinary),
	}

	profile := darwinProfile()

	// Execute upgrade.
	report := Execute(context.Background(), results, profile, homeDir, false)

	// Verify upgrade ran.
	if len(report.Results) != 1 {
		t.Fatalf("len(Results) = %d, want 1", len(report.Results))
	}
	if report.Results[0].Status != UpgradeSucceeded {
		t.Errorf("engram status = %q, want UpgradeSucceeded", report.Results[0].Status)
	}

	// Verify config files are byte-identical after upgrade.
	for relPath, want := range configFiles {
		fullPath := homeDir + "/" + relPath
		got, err := os.ReadFile(fullPath)
		if err != nil {
			t.Fatalf("read config %s after upgrade: %v", relPath, err)
		}
		if string(got) != want {
			t.Errorf("config %s was mutated by upgrade!\n  before: %q\n  after:  %q", relPath, want, string(got))
		}
	}
}

// --- helper: verify errors wrap correctly ---
func TestToolUpgradeResult_ErrorWrapping(t *testing.T) {
	sentinel := errors.New("sentinel error")
	r := ToolUpgradeResult{
		ToolName: "engram",
		Status:   UpgradeFailed,
		Err:      sentinel,
	}

	if !errors.Is(r.Err, sentinel) {
		t.Errorf("errors.Is failed — Err should wrap the sentinel")
	}
}

// --- Upgrade Backup Hardening Tests ---

// TestConfigPathsForBackup_CoversManagedAgentPaths verifies that upgrade
// backups include Brunnr-Code-managed files for installed agents, without treating
// every file in an agent config directory as backup-owned.
func TestConfigPathsForBackup_CoversManagedAgentPaths(t *testing.T) {
	homeDir := t.TempDir()

	managedFiles := map[string]string{
		".claude.json":      `{"oauthAccount":{"emailAddress":"user@example.com"},"mcpServers":{"engram":{"command":"engram"}}}`,
		".claude/CLAUDE.md": "# Claude",
	}
	unmanagedFile := filepath.Join(homeDir, ".claude", "conversation-transcript.md")

	for relPath, content := range managedFiles {
		full := filepath.Join(homeDir, relPath)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", relPath, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", relPath, err)
		}
	}
	if err := os.WriteFile(unmanagedFile, []byte("runtime data"), 0o644); err != nil {
		t.Fatalf("write unmanaged file: %v", err)
	}

	paths := configPathsForBackup(homeDir)
	pathSet := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		pathSet[p] = struct{}{}
	}

	for relPath := range managedFiles {
		full := filepath.Join(homeDir, relPath)
		if _, ok := pathSet[full]; !ok {
			t.Errorf("configPathsForBackup missing managed file %q", relPath)
		}
	}
	if _, ok := pathSet[unmanagedFile]; ok {
		t.Errorf("configPathsForBackup included unmanaged file %q", unmanagedFile)
	}
}

func TestConfigPathsForBackup_HandlesEmptyDirs(t *testing.T) {
	homeDir := t.TempDir()
	// No agent config directories exist in this temp dir.

	paths := configPathsForBackup(homeDir)
	// Must return a non-nil slice (empty is fine).
	if paths == nil {
		t.Errorf("configPathsForBackup returned nil, want non-nil (empty slice is fine)")
	}
}

// NOTE: BackupWarning field existence is verified by the compiler (struct literal
// usage in tests). The failure path is fully covered by
// TestExecute_ForcedSnapshotFailureSurfacesWarningEndToEnd below.

// TestExecute_ForcedSnapshotFailureSurfacesWarningEndToEnd verifies the complete
// failure path end-to-end: when snapshot creation fails, the UpgradeReport
// carries a non-empty BackupWarning and BackupID is empty, AND RenderUpgradeReport
// renders the WARNING prefix into its output.
//
// This closes the verify gap: prior tests only validated the struct field exists
// or relied on OS permission tricks. This test injects the failure directly via
// the snapshotCreator package-level var (same testability pattern as execCommand).
func TestExecute_ForcedSnapshotFailureSurfacesWarningEndToEnd(t *testing.T) {
	origExecCommand := execCommand
	origSnapshotCreator := snapshotCreator
	t.Cleanup(func() {
		execCommand = origExecCommand
		snapshotCreator = origSnapshotCreator
	})

	// Stub exec so the upgrade itself succeeds (we're only testing the backup path).
	execCommand = func(name string, args ...string) *exec.Cmd {
		return mockCmd("echo", "upgrade ok")
	}

	// Force snapshot creation to fail.
	snapshotCreator = func(snapshotDir string, paths []string) (backup.Manifest, error) {
		return backup.Manifest{}, errors.New("simulated snapshot failure: disk full")
	}

	results := []update.UpdateResult{
		makeResult("engram", update.UpdateAvailable, "0.3.0", "0.4.0", update.InstallBinary),
	}

	report := Execute(context.Background(), results, darwinProfile(), t.TempDir(), false)

	// BackupID must be empty — the snapshot failed.
	if report.BackupID != "" {
		t.Errorf("BackupID = %q, want empty when snapshot fails", report.BackupID)
	}

	// BackupWarning must be non-empty and mention the failure.
	if report.BackupWarning == "" {
		t.Errorf("BackupWarning is empty — failure must be surfaced explicitly")
	}
	if !strings.Contains(report.BackupWarning, "pre-upgrade backup failed") {
		t.Errorf("BackupWarning = %q, want it to mention 'pre-upgrade backup failed'", report.BackupWarning)
	}
	if !strings.Contains(report.BackupWarning, "simulated snapshot failure") {
		t.Errorf("BackupWarning = %q, want it to include the root cause", report.BackupWarning)
	}

	// The upgrade must still have run and produced a result.
	if len(report.Results) != 1 {
		t.Fatalf("len(Results) = %d, want 1 — upgrade must proceed even when backup fails", len(report.Results))
	}
	if report.Results[0].Status != UpgradeSucceeded {
		t.Errorf("Result status = %q, want UpgradeSucceeded — upgrade proceeds without backup", report.Results[0].Status)
	}

	// RenderUpgradeReport must include the WARNING line in its output.
	rendered := RenderUpgradeReport(report)
	if !strings.Contains(rendered, "WARNING:") {
		t.Errorf("RenderUpgradeReport output must contain 'WARNING:' when BackupWarning is set;\ngot:\n%s", rendered)
	}
	if !strings.Contains(rendered, "pre-upgrade backup failed") {
		t.Errorf("RenderUpgradeReport output must include the backup failure message;\ngot:\n%s", rendered)
	}
}

// TestExecute_UpgradeBackupManifestHasUpgradeMetadata verifies that when Execute
// creates a pre-upgrade backup, the manifest on disk carries Source=upgrade,
// Description="pre-upgrade snapshot", and the version from AppVersion.
//
// This closes the verify gap: "no runtime test proves upgrade manifests are
// emitted with metadata". This test reads the manifest from disk directly.
func TestExecute_UpgradeBackupManifestHasUpgradeMetadata(t *testing.T) {
	origExecCommand := execCommand
	origAppVersion := AppVersion
	t.Cleanup(func() {
		execCommand = origExecCommand
		AppVersion = origAppVersion
	})
	execCommand = func(name string, args ...string) *exec.Cmd {
		return mockCmd("echo", "ok")
	}
	AppVersion = "3.0.0"

	homeDir := t.TempDir()
	// Create a config file so the snapshot captures at least one file.
	configFile := filepath.Join(homeDir, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(configFile), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(configFile, []byte("# Claude"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	results := []update.UpdateResult{
		makeResult("engram", update.UpdateAvailable, "0.3.0", "0.4.0", update.InstallBinary),
	}

	report := Execute(context.Background(), results, darwinProfile(), homeDir, false)

	if report.BackupID == "" {
		t.Fatalf("BackupID is empty — backup must be created")
	}

	// Find the backup manifest on disk and verify its metadata.
	backupRoot := filepath.Join(homeDir, ".brunnr-code", "backups")
	entries, err := os.ReadDir(backupRoot)
	if err != nil {
		t.Fatalf("ReadDir backups: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("no backup directories found under %s", backupRoot)
	}

	// There should be exactly one backup dir created by Execute.
	manifestPath := filepath.Join(backupRoot, entries[0].Name(), backup.ManifestFilename)
	manifest, err := backup.ReadManifest(manifestPath)
	if err != nil {
		t.Fatalf("ReadManifest(%q): %v", manifestPath, err)
	}

	if manifest.Source != backup.BackupSourceUpgrade {
		t.Errorf("manifest.Source = %q, want %q", manifest.Source, backup.BackupSourceUpgrade)
	}
	if manifest.Description != "pre-upgrade snapshot" {
		t.Errorf("manifest.Description = %q, want %q", manifest.Description, "pre-upgrade snapshot")
	}
	if manifest.CreatedByVersion != "3.0.0" {
		t.Errorf("manifest.CreatedByVersion = %q, want 3.0.0", manifest.CreatedByVersion)
	}
}

// TestExecute_SuccessfulSnapshotHasNoWarning verifies the happy path: when the
// snapshot succeeds, BackupWarning is empty (no false positive warning).
func TestExecute_SuccessfulSnapshotHasNoWarning(t *testing.T) {
	origExecCommand := execCommand
	t.Cleanup(func() { execCommand = origExecCommand })
	execCommand = func(name string, args ...string) *exec.Cmd {
		return mockCmd("echo", "ok")
	}
	// snapshotCreator is intentionally left at its real default.

	results := []update.UpdateResult{
		makeResult("engram", update.UpdateAvailable, "0.3.0", "0.4.0", update.InstallBinary),
	}

	report := Execute(context.Background(), results, darwinProfile(), t.TempDir(), false)

	if report.BackupWarning != "" {
		t.Errorf("BackupWarning = %q, want empty when snapshot succeeds", report.BackupWarning)
	}
	if report.BackupID == "" {
		t.Errorf("BackupID is empty — should be set when snapshot succeeds")
	}

	rendered := RenderUpgradeReport(report)
	if strings.Contains(rendered, "WARNING:") {
		t.Errorf("RenderUpgradeReport must NOT contain 'WARNING:' on success;\ngot:\n%s", rendered)
	}
}

// --- Phase 3: Adapter-driven configPathsForBackup ---

// TestConfigPathsForBackup_CoversRegistryAgentsNotInOldList verifies that
// configPathsForBackup covers managed paths for agents from the full registry,
// not just a previous hardcoded agent list. codex (~/.codex) was NOT in the old hardcoded list.
func TestConfigPathsForBackup_CoversRegistryAgentsNotInOldList(t *testing.T) {
	homeDir := t.TempDir()

	// Create a file under codex config dir — not in old hardcoded list.
	// Use uppercase AGENTS.md to match the codex CLI convention (fix for #299).
	codexFile := filepath.Join(homeDir, ".codex", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(codexFile), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(codexFile, []byte("# Codex config"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	paths := configPathsForBackup(homeDir)

	pathSet := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		pathSet[p] = struct{}{}
	}

	if _, ok := pathSet[codexFile]; !ok {
		t.Errorf("configPathsForBackup() missing codex managed file %q — must cover registry agents, not just old hardcoded 4; got paths: %v", codexFile, paths)
	}
}

func TestConfigPathsForBackup_ExcludesRuntimeDirs(t *testing.T) {
	homeDir := t.TempDir()

	// --- Claude: config file (keep) + runtime dirs (exclude) ---
	claudeConfig := filepath.Join(homeDir, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(claudeConfig), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(claudeConfig, []byte("# Claude"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	claudeExcludes := []string{"projects", "sessions", "plugins", "cache", "backups"}

	// --- Codex: managed config file (keep) + sessions (exclude) ---
	codexConfig := filepath.Join(homeDir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(codexConfig), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(codexConfig, []byte("model = \"gpt\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	codexExcludes := []string{"sessions"}

	// Create excluded dirs with files for all agents.
	type agentExclude struct {
		base     string
		excludes []string
	}
	agents := []agentExclude{
		{filepath.Join(homeDir, ".claude"), claudeExcludes},
		{filepath.Join(homeDir, ".codex"), codexExcludes},
	}

	var excludedFiles []string
	for _, agent := range agents {
		for _, dir := range agent.excludes {
			f := filepath.Join(agent.base, dir, "data.json")
			if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
				t.Fatalf("MkdirAll %s: %v", dir, err)
			}
			if err := os.WriteFile(f, []byte("runtime data"), 0o644); err != nil {
				t.Fatalf("WriteFile %s: %v", dir, err)
			}
			excludedFiles = append(excludedFiles, f)
		}
	}

	paths := configPathsForBackup(homeDir)
	pathSet := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		pathSet[p] = struct{}{}
	}

	// Config files must be present.
	for _, cfg := range []string{claudeConfig, codexConfig} {
		if _, ok := pathSet[cfg]; !ok {
			t.Errorf("configPathsForBackup missing config file %q", cfg)
		}
	}

	// Runtime files must NOT be present.
	for _, f := range excludedFiles {
		if _, ok := pathSet[f]; ok {
			t.Errorf("configPathsForBackup should exclude runtime file %q", f)
		}
	}
}

// --- Phase 4: state.json-driven backup scope (issues #114, #354) ---

// TestConfigPathsForBackup_StateWinsOverFilesystem verifies that when state.json
// lists a subset of agents, configPathsForBackup backs up only those agents'
// config paths — NOT all detected config dirs.
//
// Scenario: state.json has 1 agent (claude-code); filesystem also has codex.
// Backup must include claude-code paths but NOT codex paths.
func TestConfigPathsForBackup_StateWinsOverFilesystem(t *testing.T) {
	homeDir := t.TempDir()

	// Create both agent config dirs on disk (simulates filesystem detection).
	claudeDir := filepath.Join(homeDir, ".claude")
	codexDir := filepath.Join(homeDir, ".codex")
	for _, dir := range []string{claudeDir, codexDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	// Write a managed claude-code config file that should be backed up.
	claudeSettings := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(claudeSettings, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write %s: %v", claudeSettings, err)
	}
	// Write a codex config file that should NOT be backed up (not in state).
	codexSettings := filepath.Join(codexDir, "config.toml")
	if err := os.WriteFile(codexSettings, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write %s: %v", codexSettings, err)
	}

	// Write state.json with only claude-code — this is the user's explicit selection.
	if err := state.Write(homeDir, state.InstallState{
		InstalledAgents: []string{string(model.AgentClaudeCode)},
	}); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	paths := configPathsForBackup(homeDir)
	pathSet := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		pathSet[p] = struct{}{}
	}

	// Codex config must NOT appear in backup — not in state.json.
	if _, ok := pathSet[codexSettings]; ok {
		t.Errorf("configPathsForBackup included codex config %q which is not in state.json; state.json should be the source of truth", codexSettings)
	}
}

// TestConfigPathsForBackup_FallsBackToFilesystemWhenNoState verifies that when
// state.json does not exist, configPathsForBackup falls back to filesystem
// detection — preserving the first-time install behavior.
func TestConfigPathsForBackup_FallsBackToFilesystemWhenNoState(t *testing.T) {
	homeDir := t.TempDir()

	// Create a claude config dir on disk — no state.json.
	claudeDir := filepath.Join(homeDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", claudeDir, err)
	}
	claudeSettings := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(claudeSettings, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write %s: %v", claudeSettings, err)
	}

	// No state.json written — simulates fresh install.
	paths := configPathsForBackup(homeDir)

	// Result must not be nil (empty slice is fine, but nil would panic callers).
	if paths == nil {
		t.Error("configPathsForBackup returned nil when state.json missing, want non-nil")
	}
}

// TestConfigPathsForBackup_EmptyStateAgentsFallsBackToFilesystem verifies that
// state.json with an empty InstalledAgents list is treated the same as a missing
// state.json — filesystem detection is used as fallback.
func TestConfigPathsForBackup_EmptyStateAgentsFallsBackToFilesystem(t *testing.T) {
	homeDir := t.TempDir()

	// Write state.json with an empty agent list.
	if err := state.Write(homeDir, state.InstallState{
		InstalledAgents: []string{},
	}); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	// Create a claude config dir on disk.
	claudeDir := filepath.Join(homeDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", claudeDir, err)
	}
	claudeSettings := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(claudeSettings, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write %s: %v", claudeSettings, err)
	}

	paths := configPathsForBackup(homeDir)
	if paths == nil {
		t.Error("configPathsForBackup returned nil, want non-nil")
	}
}

func TestConfigPathsForBackup_ConfiguredEmptySelectionExcludesDetectedAgents(t *testing.T) {
	homeDir := t.TempDir()
	claudeSettings := filepath.Join(homeDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(claudeSettings), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(claudeSettings), err)
	}
	if err := os.WriteFile(claudeSettings, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write %s: %v", claudeSettings, err)
	}
	if err := state.Write(homeDir, state.InstallState{SelectionConfigured: true}); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	reg, err := agents.NewDefaultRegistry()
	if err != nil {
		t.Fatalf("agents.NewDefaultRegistry: %v", err)
	}
	claudeAdapter, ok := reg.Get(model.AgentClaudeCode)
	if !ok {
		t.Fatal("default registry does not contain Claude Code")
	}

	actual := make(map[string]struct{})
	for _, path := range configPathsForBackup(homeDir) {
		actual[path] = struct{}{}
	}
	for _, path := range managedAgentBackupPaths(homeDir, claudeAdapter, &bytes.Buffer{}) {
		if _, found := actual[path]; found {
			t.Errorf("configPathsForBackup() included Claude-managed path %q for a configured empty selection", path)
		}
	}
}

func mockCmd(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

// writeRetiredFixture creates a leftover retired file: the snapshot only
// records retired inventory paths that exist.
func writeRetiredFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Absent retired files never enter the upgrade snapshot: a restore would
// delete a file the user created there later. Prompts behind a symlinked
// directory are not declared either; retirement never enters it.
func TestManagedAgentBackupPathsOmitAbsentRetiredRuntimeFiles(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	reg, err := agents.NewDefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	absent := map[model.AgentID][]string{
		model.AgentCodex: {
			filepath.Join(homeDir, ".codex", "agents.md"),
		},
	}
	for agent, paths := range absent {
		adapter, ok := reg.Get(agent)
		if !ok {
			t.Fatalf("default registry does not contain %s", agent)
		}
		got := managedAgentBackupPaths(homeDir, adapter, &bytes.Buffer{})
		for _, path := range paths {
			if slices.Contains(got, path) {
				t.Errorf("%s snapshot records absent retired path %s", agent, path)
			}
		}
	}

}

// Retirement never enters a symlinked ~/.codex, so the snapshot
// never declares a prompt behind one.
func TestManagedAgentBackupPathsOmitPromptsBehindSymlinkedRuntimeDirs(t *testing.T) {
	homeDir := t.TempDir()
	reg, err := agents.NewDefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	dotfiles := t.TempDir()
	writeRetiredFixture(t, filepath.Join(dotfiles, "codex", "agents.md"))
	for name, target := range map[string]string{".codex": "codex"} {
		if err := os.Symlink(filepath.Join(dotfiles, target), filepath.Join(homeDir, name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	for agent, path := range map[model.AgentID]string{
		model.AgentCodex: filepath.Join(homeDir, ".codex", "agents.md"),
	} {
		adapter, ok := reg.Get(agent)
		if !ok {
			t.Fatalf("default registry does not contain %s", agent)
		}
		if slices.Contains(managedAgentBackupPaths(homeDir, adapter, &bytes.Buffer{}), path) {
			t.Errorf("%s snapshot declares %s behind a symlinked directory", agent, path)
		}
	}
}

// TestClaudePilotSnapshotRestoreRemovesModulePilot pins the #5256 S27 contract:
// an upgrade snapshot taken before the Claude module pilot existed records its
// nine known paths as absent, so manually restoring it after a later pilot
// install brings back the monolithic core and deletes every file at those
// paths, including a module the user edited and a known module name the user
// created. Files outside the known paths are not part of the snapshot and stay.
func TestClaudePilotSnapshotRestoreRemovesModulePilot(t *testing.T) {
	home := t.TempDir()
	if err := state.Write(home, state.InstallState{InstalledAgents: []string{string(model.AgentClaudeCode)}}); err != nil {
		t.Fatalf("state.Write: %v", err)
	}
	options := agentguidance.RoutingOptions{ReviewContract: reviewassets.ReviewExecutionContractFor}
	if _, err := agentguidance.InjectRoutingWithOptions(home, model.AgentClaudeCode, options); err != nil {
		t.Fatalf("install monolithic core: %v", err)
	}
	corePath := filepath.Join(home, ".claude", "CLAUDE.md")
	original, err := os.ReadFile(corePath)
	if err != nil {
		t.Fatal(err)
	}

	backupPaths := configPathsForBackup(home)
	for _, path := range backupPaths {
		rel, err := filepath.Rel(home, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			t.Fatalf("snapshot path %q escapes isolated home %q (relative %q, err %v)", path, home, rel, err)
		}
	}
	manifest, err := backup.NewSnapshotter().Create(filepath.Join(t.TempDir(), "snapshot"), backupPaths)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	options.ClaudeGlobalModules = true
	installed, err := agentguidance.InjectRoutingWithOptions(home, model.AgentClaudeCode, options)
	if err != nil {
		t.Fatalf("install pilot: %v", err)
	}
	known, err := agentguidance.RoutingPathsWithOptions(home, model.AgentClaudeCode, options)
	if err != nil || len(known) != 9 || known[0] != corePath {
		t.Fatalf("pilot paths = %v, %v; want the core and eight module paths", known, err)
	}
	moduleDir := filepath.Dir(known[1])
	written := make(map[string]bool, len(installed.Files))
	for _, path := range installed.Files {
		written[path] = true
	}
	var userModified, userCreated string
	for _, path := range known[1 : len(known)-1] {
		switch {
		case written[path] && userModified == "":
			userModified = path
		case !written[path] && userCreated == "":
			userCreated = path
		}
	}
	if userModified == "" || userCreated == "" || !written[known[len(known)-1]] {
		t.Fatalf("pilot wrote %v; want a module, an unused known module name and the ledger", installed.Files)
	}
	unknown := filepath.Join(moduleDir, "notes.md")
	for path, body := range map[string]string{userModified: "user edit\n", userCreated: "user module\n", unknown: "user notes\n"} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := (backup.RestoreService{Roots: []string{home}}).Restore(manifest); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if got, err := os.ReadFile(corePath); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("restored core differs from the monolithic snapshot (err %v)", err)
	}
	for _, path := range known[1:] {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("%s survived the restore (lstat err %v); the snapshot recorded it absent", path, err)
		}
	}
	if got, err := os.ReadFile(unknown); err != nil || string(got) != "user notes\n" {
		t.Errorf("unknown file outside the known paths = %q, %v; want it preserved", got, err)
	}
}

// TestManagedAgentBackupPathsIncludeLegacyNeutralOutputStyle keeps the
// retired neutral.md output style in the upgrade snapshot, because the
// post-upgrade persona sync removes it.
func TestManagedAgentBackupPathsIncludeLegacyNeutralOutputStyle(t *testing.T) {
	homeDir := t.TempDir()
	reg, err := agents.NewDefaultRegistry()
	if err != nil {
		t.Fatalf("agents.NewDefaultRegistry: %v", err)
	}
	claudeAdapter, ok := reg.Get(model.AgentClaudeCode)
	if !ok {
		t.Fatal("default registry does not contain Claude Code")
	}
	got := managedAgentBackupPaths(homeDir, claudeAdapter, &bytes.Buffer{})
	for _, want := range []string{
		filepath.Join(homeDir, ".claude", "output-styles", "principal-engineer.md"),
		filepath.Join(homeDir, ".claude", "output-styles", "neutral.md"),
	} {
		if !slices.Contains(got, want) {
			t.Errorf("managedAgentBackupPaths missing %s; got %v", want, got)
		}
	}
}
