package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/carsimax/brunnr-code/internal/agents/codex"
	"github.com/carsimax/brunnr-code/internal/backup"
	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/pipeline"
	"github.com/carsimax/brunnr-code/internal/system"
)

// missingBinaryLookPath simulates all installable binaries (engram, gga) as
// missing. Go availability is no longer required for engram installation
// (pre-built binaries are downloaded directly from GitHub Releases).
func missingBinaryLookPath(name string) (string, error) {
	return "", exec.ErrNotFound
}

func assertFileContains(t *testing.T, path string, want string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if !strings.Contains(string(body), want) {
		t.Fatalf("file %q missing %q; got:\n%s", path, want, string(body))
	}
}

func assertFileNotContains(t *testing.T, path string, unwanted string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if strings.Contains(string(body), unwanted) {
		t.Fatalf("file %q still contains %q; got:\n%s", path, unwanted, string(body))
	}
}

// seedLegacyPiMCPAdapter writes the pi-mcp-adapter entries older releases
// provisioned, so install tests can prove they are retired.
func seedLegacyPiMCPAdapter(t *testing.T, agentDir string) {
	t.Helper()
	files := map[string]string{
		filepath.Join(agentDir, "settings.json"):       `{"packages":["npm:other@1.0.0","npm:pi-mcp-adapter"]}`,
		filepath.Join(agentDir, "npm", "package.json"): `{"dependencies":{"left-pad":"^1.0.0","pi-mcp-adapter":"^2.6.0"}}`,
	}
	for path, body := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
}

func stringSliceContains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

const engramInitCommandForTest = "npm exec --yes --package gentle-engram@latest -- pi-engram init"

func TestCodexODDAssignmentInstallRoutingStep(t *testing.T) {
	home := t.TempDir()
	step := agentRoutingGuidanceStep{
		agent: model.AgentCodex, homeDir: home, scope: ScopeGlobal,
		codexPhaseModels: map[string]string{"odd-worker": "gpt-explicit"},
		codexEfforts:     map[string]model.CodexEffort{"odd-worker": model.CodexEffortXHigh},
		codexCarrils:     map[string]string{"sdd-cheap": "gpt-cheap", "sdd-strong": "gpt-strong"},
	}
	if err := step.Run(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(home, ".codex", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"| `odd-worker` | `gpt-explicit` | `xhigh` |", "| `odd-explorer` | `gpt-cheap` | `high` |", "| `odd-verify` | `gpt-strong` | `medium` |"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("installed Codex guidance missing %q", want)
		}
	}
}

func TestRunInstallCodexKeepsOldRuntimeFailure(t *testing.T) {
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
	cmdLookPath = func(string) (string, error) { return "/usr/local/bin/engram", nil }
	runCommand = func(string, ...string) error { return nil }
	restoreRuntime := codex.SetRuntimeVersionCommandForTest("codex-cli 0.143.9", nil)
	t.Cleanup(restoreRuntime)

	profiles := []string{"sdd-strong.config.toml", "sdd-mid.config.toml", "sdd-cheap.config.toml"}
	for _, name := range profiles {
		path := filepath.Join(home, ".codex", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("user-content\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, err := RunInstall([]string{"--agent", "codex", "--component", "engram"}, macOSDetectionResult())
	if err == nil || !strings.Contains(err.Error(), "Codex >=0.144.0") {
		t.Fatalf("RunInstall() error = %v, want old Codex runtime failure", err)
	}
	for _, name := range profiles {
		content, readErr := os.ReadFile(filepath.Join(home, ".codex", name))
		if readErr != nil || string(content) != "user-content\n" {
			t.Fatalf("old runtime modified %s: got=%q error=%v", name, content, readErr)
		}
	}
}

// TestAgentInstallStepRunsNoCommands proves a selected agent whose runtime is
// absent does not block installation or trigger agent acquisition.
func TestAgentInstallStepRunsNoCommands(t *testing.T) {
	restoreCommand := runCommand
	recorder := &commandRecorder{}
	runCommand = recorder.record
	t.Cleanup(func() { runCommand = restoreCommand })

	step := agentInstallStep{
		id:      "agent:codex",
		agent:   model.AgentCodex,
		homeDir: t.TempDir(),
	}
	if err := step.Run(); err != nil {
		t.Fatalf("agentInstallStep.Run() error = %v, want absent runtime to be skipped", err)
	}
	if got := recorder.get(); len(got) != 0 {
		t.Fatalf("commands executed = %v, want none for a selected agent", got)
	}
}

func TestRunCommandSequenceWithProgressStopsAfterFailedCommand(t *testing.T) {
	restoreCommand := runCommand
	t.Cleanup(func() { runCommand = restoreCommand })
	var commands []string
	runCommand = func(name string, args ...string) error {
		commands = append(commands, strings.Join(append([]string{name}, args...), " "))
		return errors.New("package install failed")
	}

	var events []pipeline.ProgressEvent
	err := runCommandSequenceWithProgress(
		[][]string{{"npm", "install", "first"}, {"npm", "install", "second"}},
		func(event pipeline.ProgressEvent) { events = append(events, event) },
		"agent:codex",
	)
	if err == nil || !strings.Contains(err.Error(), "package install failed") {
		t.Fatalf("runCommandSequenceWithProgress() error = %v, want package failure", err)
	}
	if len(commands) != 1 || commands[0] != "npm install first" {
		t.Fatalf("commands = %v, want only the failed command", commands)
	}
	if len(events) != 2 {
		t.Fatalf("progress events = %v, want running and failed", events)
	}
	if events[0].StepID != "agent:codex:npm install first" || events[0].Status != pipeline.StepStatusRunning {
		t.Fatalf("running event = %+v", events[0])
	}
	if events[1].StepID != events[0].StepID || events[1].Status != pipeline.StepStatusFailed || events[1].Err == nil {
		t.Fatalf("failed event = %+v", events[1])
	}
}

type failingPersonaInstallStep struct{}

func (failingPersonaInstallStep) ID() string { return "test:fail-after-persona" }
func (failingPersonaInstallStep) Run() error {
	return errors.New("forced failure after persona cleanup")
}

// --- Batch D: Linux profile runtime wiring integration tests ---

// linuxDetectionResult builds a DetectionResult with a Linux profile for integration tests.
func linuxDetectionResult(distro, pkgMgr string) system.DetectionResult {
	return system.DetectionResult{
		System: system.SystemInfo{
			OS:        "linux",
			Arch:      "amd64",
			Shell:     "/bin/bash",
			Supported: true,
			Profile: system.PlatformProfile{
				OS:             "linux",
				LinuxDistro:    distro,
				PackageManager: pkgMgr,
				Supported:      true,
			},
		},
	}
}

// commandRecorder captures all external commands invoked during a pipeline run.
type commandRecorder struct {
	mu       sync.Mutex
	commands []string
}

func (r *commandRecorder) record(name string, args ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, fmt.Sprintf("%s %s", name, strings.Join(args, " ")))
	return nil
}

func (r *commandRecorder) get() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]string, len(r.commands))
	copy(cp, r.commands)
	return cp
}

func TestRunInstallLinuxUbuntuResolvesAptCommands(t *testing.T) {
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
	cmdLookPath = missingBinaryLookPath
	recorder := &commandRecorder{}
	runCommand = recorder.record

	detection := linuxDetectionResult(system.LinuxDistroUbuntu, "apt")
	result, err := RunInstall(
		[]string{"--agent", "claude-code", "--component", "permissions"},
		detection,
	)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	if !result.Verify.Ready {
		t.Fatalf("verification ready = false, report = %#v", result.Verify)
	}

	// Verify platform decision was resolved from the Linux profile.
	if result.Resolved.PlatformDecision.OS != "linux" {
		t.Fatalf("platform decision OS = %q, want linux", result.Resolved.PlatformDecision.OS)
	}
	if result.Resolved.PlatformDecision.PackageManager != "apt" {
		t.Fatalf("platform decision package manager = %q, want apt", result.Resolved.PlatformDecision.PackageManager)
	}
}

func TestRunInstallLinuxArchResolvesPacmanCommands(t *testing.T) {
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
	cmdLookPath = missingBinaryLookPath
	recorder := &commandRecorder{}
	runCommand = recorder.record

	detection := linuxDetectionResult(system.LinuxDistroArch, "pacman")
	result, err := RunInstall(
		[]string{"--agent", "claude-code", "--component", "permissions"},
		detection,
	)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	if !result.Verify.Ready {
		t.Fatalf("verification ready = false, report = %#v", result.Verify)
	}

	if result.Resolved.PlatformDecision.PackageManager != "pacman" {
		t.Fatalf("platform decision package manager = %q, want pacman", result.Resolved.PlatformDecision.PackageManager)
	}
}

func TestRunInstallLinuxUbuntuWithEngramUsesDirectDownload(t *testing.T) {
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
	cmdLookPath = missingBinaryLookPath
	recorder := &commandRecorder{}
	runCommand = recorder.record

	// Override engramDownloadFn to avoid real HTTP calls.
	origDownloadFn := engramDownloadFn
	engramDownloadFn = func(profile system.PlatformProfile) (string, error) {
		return "/tmp/fake-engram", nil
	}
	t.Cleanup(func() { engramDownloadFn = origDownloadFn })

	detection := linuxDetectionResult(system.LinuxDistroUbuntu, "apt")
	result, err := RunInstall(
		[]string{"--agent", "claude-code", "--component", "engram"},
		detection,
	)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	if !result.Verify.Ready {
		t.Fatalf("verification ready = false, report = %#v", result.Verify)
	}

	// Must NOT use go install for engram on Linux.
	for _, cmd := range recorder.get() {
		if strings.Contains(cmd, "go install") && strings.Contains(cmd, "engram") {
			t.Fatalf("Linux engram install should NOT use go install, got command: %s", cmd)
		}
	}
}

func TestRunInstallLinuxArchWithEngramUsesDirectDownload(t *testing.T) {
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
	cmdLookPath = missingBinaryLookPath
	recorder := &commandRecorder{}
	runCommand = recorder.record

	origDownloadFn := engramDownloadFn
	engramDownloadFn = func(profile system.PlatformProfile) (string, error) {
		return "/tmp/fake-engram", nil
	}
	t.Cleanup(func() { engramDownloadFn = origDownloadFn })

	detection := linuxDetectionResult(system.LinuxDistroArch, "pacman")
	result, err := RunInstall(
		[]string{"--agent", "claude-code", "--component", "engram"},
		detection,
	)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	if !result.Verify.Ready {
		t.Fatalf("verification ready = false, report = %#v", result.Verify)
	}

	// Must NOT use go install for engram on Arch Linux.
	for _, cmd := range recorder.get() {
		if strings.Contains(cmd, "go install") && strings.Contains(cmd, "engram") {
			t.Fatalf("Arch Linux engram install should NOT use go install, got command: %s", cmd)
		}
	}
}

// TestRunInstallWorkspaceScopeRollback_SurfacesRealError reproduces issue #2451:
// a workspace-scoped install (--scope workspace) legitimately writes files whose
// OriginalPath resolves outside the user home directory. When the backup snapshot
// captures such a path (Existed:false, since it does not exist yet) and a LATER
// apply step fails, rollback fires and must restore/remove that workspace-scoped
// entry — not refuse it and mask the real download failure with a validation
// error claiming the path must be "under the user home directory".
func TestRunInstallWorkspaceScopeRollback_SurfacesRealError(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()

	restoreHome := osUserHomeDir
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current working directory: %v", err)
	}
	t.Cleanup(func() {
		osUserHomeDir = restoreHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
		if err := os.Chdir(originalCwd); err != nil {
			t.Errorf("failed to restore working directory: %v", err)
		}
	})
	cmdLookPath = missingBinaryLookPath

	osUserHomeDir = func() (string, error) { return home, nil }
	runCommand = func(name string, args ...string) error { return nil }

	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("failed to change working directory to temp workspace: %v", err)
	}

	// Fail the engram download AFTER the backup snapshot has already captured
	// the not-yet-existing workspace-scoped engram MCP config path (Existed:false),
	// so the pipeline's apply stage fails and rollback fires against that entry.
	origDownloadFn := engramDownloadFn
	engramDownloadFn = func(profile system.PlatformProfile) (string, error) {
		return "", os.ErrPermission
	}
	t.Cleanup(func() { engramDownloadFn = origDownloadFn })

	detection := linuxDetectionResult(system.LinuxDistroUbuntu, "apt")
	_, err = RunInstall(
		[]string{"--scope", "workspace", "--agent", "claude-code", "--component", "engram"},
		detection,
	)
	if err == nil {
		t.Fatalf("RunInstall() expected error")
	}

	if strings.Contains(err.Error(), "user home directory") {
		t.Fatalf("rollback masked the real download error with a home-directory validation refusal: %v", err)
	}
	if !strings.Contains(err.Error(), "permission") {
		t.Fatalf("RunInstall() error = %v, want it to surface the real download failure (os.ErrPermission)", err)
	}
}

// --- Batch E: Linux verification and macOS parity matrix ---

func TestRunInstallLinuxVerificationReportsReadyOnSuccess(t *testing.T) {
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
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = missingBinaryLookPath

	detection := linuxDetectionResult(system.LinuxDistroUbuntu, "apt")
	result, err := RunInstall(
		[]string{"--agent", "claude-code", "--component", "permissions"},
		detection,
	)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	if !result.Verify.Ready {
		t.Fatalf("Verify.Ready = false, want true for successful Linux install")
	}
	if result.Verify.Failed != 0 {
		t.Fatalf("Verify.Failed = %d, want 0", result.Verify.Failed)
	}
}

func TestRunInstallLinuxArchVerificationReportsReadyOnSuccess(t *testing.T) {
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
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = missingBinaryLookPath

	detection := linuxDetectionResult(system.LinuxDistroArch, "pacman")
	result, err := RunInstall(
		[]string{"--agent", "claude-code", "--component", "permissions"},
		detection,
	)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	if !result.Verify.Ready {
		t.Fatalf("Verify.Ready = false, want true for successful Arch install")
	}
}

func TestRunInstallLinuxDryRunSkipsVerification(t *testing.T) {
	detection := linuxDetectionResult(system.LinuxDistroUbuntu, "apt")
	result, err := RunInstall([]string{"--dry-run"}, detection)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	if !result.DryRun {
		t.Fatalf("DryRun = false, want true")
	}
	// Verify report should be zero-value (no checks run in dry-run)
	if result.Verify.Passed != 0 || result.Verify.Failed != 0 {
		t.Fatalf("expected zero verify counters in dry-run, got passed=%d failed=%d", result.Verify.Passed, result.Verify.Failed)
	}
}

func TestRunInstallLinuxDryRunPlatformDecisionRendersCorrectly(t *testing.T) {
	detection := linuxDetectionResult(system.LinuxDistroArch, "pacman")
	result, err := RunInstall([]string{"--dry-run"}, detection)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	output := RenderDryRun(result)
	want := "os=linux distro=arch package-manager=pacman status=supported"
	if !strings.Contains(output, want) {
		t.Fatalf("RenderDryRun() missing platform decision\noutput=%s\nwant contains=%s", output, want)
	}
}

// --- macOS parity regression checks ---

func macOSDetectionResult() system.DetectionResult {
	return system.DetectionResult{
		System: system.SystemInfo{
			OS:        "darwin",
			Arch:      "arm64",
			Shell:     "/bin/zsh",
			Supported: true,
			Profile: system.PlatformProfile{
				OS:             "darwin",
				PackageManager: "brew",
				Supported:      true,
			},
		},
	}
}

func TestRunInstallMacOSStillResolvesBrewCommands(t *testing.T) {
	home := t.TempDir()
	restoreHome := osUserHomeDir
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	restoreStat := osStat
	t.Cleanup(func() {
		osUserHomeDir = restoreHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
		osStat = restoreStat
	})

	osUserHomeDir = func() (string, error) { return home, nil }
	cmdLookPath = missingBinaryLookPath
	// Force resolveEngramInstalledPath's Homebrew-prefix fallback (#4020) to
	// report "not found" regardless of the real machine running this test,
	// so this test still exercises the genuinely-missing install path.
	osStat = func(name string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	recorder := &commandRecorder{}
	runCommand = recorder.record

	detection := macOSDetectionResult()
	result, err := RunInstall(
		[]string{"--agent", "claude-code", "--component", "engram"},
		detection,
	)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	if !result.Verify.Ready {
		t.Fatalf("macOS verification ready = false")
	}

	// Verify brew install command was used, not apt or pacman.
	commands := recorder.get()
	foundBrew := false
	for _, cmd := range commands {
		if strings.Contains(cmd, "brew install engram") {
			foundBrew = true
			break
		}
	}
	if !foundBrew {
		t.Fatalf("expected brew install for macOS engram, got commands: %v", commands)
	}
}

func TestRunInstallMacOSDryRunPlatformDecision(t *testing.T) {
	detection := macOSDetectionResult()
	result, err := RunInstall([]string{"--dry-run"}, detection)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	if result.Resolved.PlatformDecision.OS != "darwin" {
		t.Fatalf("macOS platform decision OS = %q, want darwin", result.Resolved.PlatformDecision.OS)
	}
	if result.Resolved.PlatformDecision.PackageManager != "brew" {
		t.Fatalf("macOS platform decision PM = %q, want brew", result.Resolved.PlatformDecision.PackageManager)
	}
	if !result.Resolved.PlatformDecision.Supported {
		t.Fatalf("macOS platform decision Supported = false, want true")
	}
}

func TestRunInstallMacOSVerificationMatchesPreLinuxBehavior(t *testing.T) {
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
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = missingBinaryLookPath

	detection := macOSDetectionResult()
	result, err := RunInstall(
		[]string{"--agent", "claude-code", "--component", "permissions"},
		detection,
	)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	if !result.Verify.Ready {
		t.Fatalf("macOS verify ready = false, want true")
	}
	if result.Verify.Failed != 0 {
		t.Fatalf("macOS verify failed = %d, want 0", result.Verify.Failed)
	}
}

// --- Skip-when-installed and Go auto-install tests ---

func TestRunInstallEngramSkipsInstallWhenAlreadyOnPath(t *testing.T) {
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
	// Simulate engram already installed on PATH.
	cmdLookPath = func(name string) (string, error) {
		return "/usr/local/bin/" + name, nil
	}
	recorder := &commandRecorder{}
	runCommand = recorder.record

	detection := macOSDetectionResult()
	result, err := RunInstall(
		[]string{"--agent", "claude-code", "--component", "engram"},
		detection,
	)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	if !result.Verify.Ready {
		t.Fatalf("verification ready = false")
	}

	// No brew/go install commands should have been recorded — only agent install.
	for _, cmd := range recorder.get() {
		if strings.Contains(cmd, "brew install engram") || (strings.Contains(cmd, "go install") && strings.Contains(cmd, "engram")) {
			t.Fatalf("expected engram install to be skipped, but got command: %s", cmd)
		}
	}
}

func TestRunInstallEngramSetupStrictFailsWhenSetupFails(t *testing.T) {
	t.Setenv("BRUNNR_CODE_ENGRAM_SETUP_STRICT", "1")

	home := t.TempDir()
	restoreHome := osUserHomeDir
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	restoreVerifyVersionCommand := verifyEngramVersionCommand
	restoreProbeCommand := probeEngramProtocolFlagCommand
	origUserHomeDirFn := backup.UserHomeDirFn
	t.Cleanup(func() {
		osUserHomeDir = restoreHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
		verifyEngramVersionCommand = restoreVerifyVersionCommand
		probeEngramProtocolFlagCommand = restoreProbeCommand
		backup.UserHomeDirFn = origUserHomeDirFn
	})
	// Override restore path validation to accept test temp dirs.
	backup.UserHomeDirFn = func() (string, error) { return home, nil }

	osUserHomeDir = func() (string, error) { return home, nil }
	const engramPath = "/usr/local/bin/engram"
	cmdLookPath = func(name string) (string, error) {
		return "/usr/local/bin/" + name, nil
	}
	verifyEngramVersionCommand = func(command string) (string, error) {
		if command != engramPath {
			t.Fatalf("verify command = %q, want %q", command, engramPath)
		}
		return "engram 1.20.0", nil
	}
	probeEngramProtocolFlagCommand = func(_ context.Context, command string) (string, error) {
		if command != engramPath {
			t.Fatalf("protocol probe command = %q, want %q", command, engramPath)
		}
		return "Usage: engram setup <slug>", nil
	}
	setupFailureTriggered := false
	runCommand = func(name string, args ...string) error {
		if name == engramPath && len(args) == 2 && args[0] == "setup" && args[1] == "claude-code" {
			setupFailureTriggered = true
			return errors.New("setup failed")
		}
		return nil
	}

	_, err := RunInstall(
		[]string{"--agent", "claude-code", "--component", "engram"},
		macOSDetectionResult(),
	)
	if !setupFailureTriggered {
		t.Fatal("RunInstall() did not execute the controlled strict setup failure path")
	}
	if err == nil {
		t.Fatalf("RunInstall() expected error in strict setup mode")
	}
	if !strings.Contains(err.Error(), "engram setup for \"claude-code\"") {
		t.Fatalf("RunInstall() error = %v, want setup error", err)
	}
}

func TestRunInstallEngramDefaultModeAttemptsClaudeSetup(t *testing.T) {
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
	cmdLookPath = func(name string) (string, error) {
		return "/usr/local/bin/" + name, nil
	}
	recorder := &commandRecorder{}
	runCommand = recorder.record

	result, err := RunInstall(
		[]string{"--agent", "claude-code", "--component", "engram"},
		macOSDetectionResult(),
	)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}
	if !result.Verify.Ready {
		t.Fatalf("verification ready = false")
	}

	commands := recorder.get()
	foundSetup := false
	for _, cmd := range commands {
		if strings.Contains(cmd, "engram setup claude-code") {
			foundSetup = true
			break
		}
	}
	if !foundSetup {
		t.Fatalf("expected default setup mode to attempt claude-code setup, got commands: %v", commands)
	}
}

// TestRunInstallEngramLinuxUsesDirectDownloadNoGoRequired verifies that on Linux,
// engram is now installed via pre-built binary download — Go is NOT required.
func TestRunInstallEngramLinuxUsesDirectDownloadNoGoRequired(t *testing.T) {
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
	// Simulate: engram missing, Go also NOT available — should still succeed.
	cmdLookPath = func(string) (string, error) {
		return "", exec.ErrNotFound
	}
	recorder := &commandRecorder{}
	runCommand = recorder.record

	// Override download to succeed without hitting GitHub.
	origDownloadFn := engramDownloadFn
	engramDownloadFn = func(profile system.PlatformProfile) (string, error) {
		return "/tmp/fake-engram", nil
	}
	t.Cleanup(func() { engramDownloadFn = origDownloadFn })

	detection := linuxDetectionResult(system.LinuxDistroUbuntu, "apt")
	result, err := RunInstall(
		[]string{"--agent", "claude-code", "--component", "engram"},
		detection,
	)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	if !result.Verify.Ready {
		t.Fatalf("verification ready = false")
	}

	// Neither "go install" nor "apt-get install golang" should appear.
	for _, cmd := range recorder.get() {
		if strings.Contains(cmd, "apt-get install -y golang") {
			t.Fatalf("Go should NOT be auto-installed (no longer needed for engram), got command: %s", cmd)
		}
		if strings.Contains(cmd, "go install") && strings.Contains(cmd, "engram") {
			t.Fatalf("engram should NOT be installed via go install, got command: %s", cmd)
		}
	}
}

// TestRunInstallEngramLinuxNeverInstallsGo verifies that even if Go is present,
// we never install Go as a prerequisite for engram (direct download path).
func TestRunInstallEngramLinuxNeverInstallsGo(t *testing.T) {
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
	cmdLookPath = missingBinaryLookPath
	recorder := &commandRecorder{}
	runCommand = recorder.record

	origDownloadFn := engramDownloadFn
	engramDownloadFn = func(profile system.PlatformProfile) (string, error) {
		return "/tmp/fake-engram", nil
	}
	t.Cleanup(func() { engramDownloadFn = origDownloadFn })

	detection := linuxDetectionResult(system.LinuxDistroUbuntu, "apt")
	result, err := RunInstall(
		[]string{"--agent", "claude-code", "--component", "engram"},
		detection,
	)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	if !result.Verify.Ready {
		t.Fatalf("verification ready = false")
	}

	// No Go installation commands should appear.
	for _, cmd := range recorder.get() {
		if strings.Contains(cmd, "apt-get install -y golang") || strings.Contains(cmd, "apt-get install -y go") {
			t.Fatalf("Go should never be installed as engram dependency, got command: %s", cmd)
		}
	}
}

func TestRunInstallEngramBrewSkipsGoCheck(t *testing.T) {
	home := t.TempDir()
	restoreHome := osUserHomeDir
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	restoreStat := osStat
	t.Cleanup(func() {
		osUserHomeDir = restoreHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
		osStat = restoreStat
	})

	osUserHomeDir = func() (string, error) { return home, nil }
	// Simulate: engram missing — brew platform, no Go or download needed.
	cmdLookPath = func(string) (string, error) {
		return "", exec.ErrNotFound
	}
	// Force resolveEngramInstalledPath's Homebrew-prefix fallback (#4020) to
	// report "not found" regardless of the real machine running this test,
	// so this test still exercises the genuinely-missing install path.
	osStat = func(name string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	recorder := &commandRecorder{}
	runCommand = recorder.record

	detection := macOSDetectionResult()
	result, err := RunInstall(
		[]string{"--agent", "claude-code", "--component", "engram"},
		detection,
	)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	if !result.Verify.Ready {
		t.Fatalf("verification ready = false")
	}

	// Should use brew install, NOT go install, and no Go auto-install.
	commands := recorder.get()
	for _, cmd := range commands {
		if strings.Contains(cmd, "golang") || strings.Contains(cmd, "apt-get") {
			t.Fatalf("brew platform should not install Go, got command: %s", cmd)
		}
		if strings.Contains(cmd, "go install") {
			t.Fatalf("brew platform should not use go install, got command: %s", cmd)
		}
	}

	foundBrew := false
	for _, cmd := range commands {
		if strings.Contains(cmd, "brew install engram") {
			foundBrew = true
		}
	}
	if !foundBrew {
		t.Fatalf("expected brew install engram, got commands: %v", commands)
	}
}

// TestRunInstallDryRunMatchesActualInstall verifies parity: every file path
// reported by the dry-run plan is actually created by the real install.
//
// Strategy:
//  1. Run with DryRun=true to obtain the resolved plan (agents + ordered components).
//  2. Derive the expected file paths from the plan using componentPaths() — the
//     same function the runtime uses for backup targets and post-apply verification.
//  3. Run the real install (same flags, same mocks, fresh temp dir).
//  4. Assert that every expected file exists on disk — no missing files.
func TestRunInstallDryRunMatchesActualInstall(t *testing.T) {
	// ── Phase 1: dry-run — resolve the plan ───────────────────────────────────
	// We do NOT need temp dir or mocks for dry-run; it never touches the FS.
	installArgs := []string{"--agent", "claude-code", "--component", "permissions"}
	dryRunArgs := append([]string{"--dry-run"}, installArgs...)
	dryResult, err := RunInstall(dryRunArgs, system.DetectionResult{})
	if err != nil {
		t.Fatalf("dry-run RunInstall() error = %v", err)
	}
	if !dryResult.DryRun {
		t.Fatalf("expected DryRun=true in result, got false")
	}

	// Use a synthetic home dir for path computation — the paths are derived
	// from the resolved plan (agents + components) and will use this root.
	// We reuse the same dir for the real install so the paths are identical.
	home := t.TempDir()

	// Derive expected file paths from the dry-run plan.  componentPaths() is
	// the single source of truth that both backup and verification use.
	adapters := resolveAdapters(dryResult.Resolved.Agents)
	var expectedPaths []string
	for _, component := range dryResult.Resolved.OrderedComponents {
		expectedPaths = append(expectedPaths, componentPathsWithWorkspaceScoped(home, "", ScopeGlobal, dryResult.Selection, adapters, component)...)
	}
	if len(expectedPaths) == 0 {
		t.Fatal("dry-run resolved zero file paths — test is misconfigured")
	}

	// ── Phase 2: real install — apply the plan ────────────────────────────────
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
	cmdLookPath = missingBinaryLookPath

	realResult, err := RunInstall(installArgs, system.DetectionResult{})
	if err != nil {
		t.Fatalf("real RunInstall() error = %v", err)
	}
	if !realResult.Verify.Ready {
		t.Fatalf("post-apply verification not ready: %#v", realResult.Verify)
	}

	// ── Phase 3: parity assertion ─────────────────────────────────────────────
	// Every file the dry-run said would be touched must exist on disk.
	var missing []string
	for _, path := range expectedPaths {
		if _, statErr := os.Stat(path); statErr != nil {
			missing = append(missing, path)
		}
	}
	if len(missing) > 0 {
		t.Errorf("dry-run planned %d file(s) that were NOT created by the real install:", len(missing))
		for _, p := range missing {
			t.Errorf("  missing: %s", p)
		}
	}
}

func TestEnsureGoAvailableAfterInstallWindowsRefreshesPath(t *testing.T) {
	restoreLookPath := cmdLookPath
	restoreStat := osStat
	restoreSetenv := osSetenv
	oldPath := os.Getenv("PATH")
	oldProgramFiles := os.Getenv("ProgramFiles")
	t.Cleanup(func() {
		cmdLookPath = restoreLookPath
		osStat = restoreStat
		osSetenv = restoreSetenv
		_ = os.Setenv("PATH", oldPath)
		_ = os.Setenv("ProgramFiles", oldProgramFiles)
	})

	programFiles := `C:\Program Files`
	if err := os.Setenv("ProgramFiles", programFiles); err != nil {
		t.Fatalf("Setenv(ProgramFiles) error = %v", err)
	}
	if err := os.Setenv("PATH", `C:\Windows\System32`); err != nil {
		t.Fatalf("Setenv(PATH) error = %v", err)
	}

	cmdLookPath = func(name string) (string, error) {
		if name == "go" {
			return "", exec.ErrNotFound
		}
		return name, nil
	}
	osStat = func(name string) (os.FileInfo, error) {
		want := filepath.Join(programFiles, "Go", "bin", "go.exe")
		if name == want {
			return fakeFileInfo{name: "go.exe"}, nil
		}
		return nil, os.ErrNotExist
	}
	osSetenv = os.Setenv

	if err := ensureGoAvailableAfterInstall(system.PlatformProfile{OS: "windows", PackageManager: "winget"}); err != nil {
		t.Fatalf("ensureGoAvailableAfterInstall() error = %v", err)
	}

	updatedPath := os.Getenv("PATH")
	expectedPrefix := filepath.Join(programFiles, "Go", "bin") + string(os.PathListSeparator)
	if !strings.HasPrefix(updatedPath, expectedPrefix) {
		t.Fatalf("PATH = %q, want prefix %q", updatedPath, expectedPrefix)
	}
}

type fakeFileInfo struct{ name string }

func (f fakeFileInfo) Name() string     { return f.name }
func (fakeFileInfo) Size() int64        { return 0 }
func (fakeFileInfo) Mode() os.FileMode  { return 0 }
func (fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (fakeFileInfo) IsDir() bool        { return false }
func (fakeFileInfo) Sys() any           { return nil }

// TestRunInstallUpgradeIdempotency verifies that running install twice with the
// same configuration does NOT duplicate any content.  The second run must be a
// no-op or a clean update — never an append of already-present sections or MCP
// entries.
func TestRunInstallUpgradeIdempotency(t *testing.T) {
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
	runCommand = func(string, ...string) error { return nil }
	// Simulate all binaries already on PATH so install steps are skipped and
	// the test only exercises injection idempotency.
	cmdLookPath = func(name string) (string, error) {
		return "/usr/local/bin/" + name, nil
	}

	args := []string{
		"--agent", "claude-code",
		"--component", "skills",
		"--skills", "go-testing",
		"--component", "engram",
		"--component", "persona",
	}

	// --- Run 1 ---
	result1, err := RunInstall(args, system.DetectionResult{})
	if err != nil {
		t.Fatalf("RunInstall() run 1 error = %v", err)
	}
	if !result1.Verify.Ready {
		t.Fatalf("run 1: verify.Ready = false, report = %#v", result1.Verify)
	}

	// Capture all relevant output files after the first run.
	claudeMDPath := filepath.Join(home, ".claude", "CLAUDE.md")
	engramMCPPath := filepath.Join(home, ".claude.json")

	claudeMDAfterRun1, err := os.ReadFile(claudeMDPath)
	if err != nil {
		t.Fatalf("run 1: ReadFile(%q) error = %v", claudeMDPath, err)
	}
	engramMCPAfterRun1, err := os.ReadFile(engramMCPPath)
	if err != nil {
		t.Fatalf("run 1: ReadFile(%q) error = %v", engramMCPPath, err)
	}

	// --- Run 2 (same flags) ---
	result2, err := RunInstall(args, system.DetectionResult{})
	if err != nil {
		t.Fatalf("RunInstall() run 2 error = %v", err)
	}
	if !result2.Verify.Ready {
		t.Fatalf("run 2: verify.Ready = false, report = %#v", result2.Verify)
	}

	// Capture output files after the second run.
	claudeMDAfterRun2, err := os.ReadFile(claudeMDPath)
	if err != nil {
		t.Fatalf("run 2: ReadFile(%q) error = %v", claudeMDPath, err)
	}
	engramMCPAfterRun2, err := os.ReadFile(engramMCPPath)
	if err != nil {
		t.Fatalf("run 2: ReadFile(%q) error = %v", engramMCPPath, err)
	}

	// --- Assertions ---

	// 1. File bytes must be identical between the two runs.
	if string(claudeMDAfterRun1) != string(claudeMDAfterRun2) {
		t.Errorf("CLAUDE.md changed between run 1 and run 2 (idempotency violation):\n--- run1 ---\n%s\n--- run2 ---\n%s",
			claudeMDAfterRun1, claudeMDAfterRun2)
	}
	if string(engramMCPAfterRun1) != string(engramMCPAfterRun2) {
		t.Errorf("engram MCP config changed between run 1 and run 2 (idempotency violation):\n--- run1 ---\n%s\n--- run2 ---\n%s",
			engramMCPAfterRun1, engramMCPAfterRun2)
	}

	// 2. No duplicate "## Agent Teams Orchestrator" headings in CLAUDE.md.
	content := string(claudeMDAfterRun2)
	orchestratorCount := strings.Count(content, "## Agent Teams Orchestrator")
	if orchestratorCount > 1 {
		t.Errorf("CLAUDE.md contains %d occurrences of '## Agent Teams Orchestrator', want at most 1:\n%s",
			orchestratorCount, content)
	}

	// 3. No duplicate brunnr-code marker blocks — each section's open marker
	// must appear exactly once.
	for _, sectionID := range []string{"engram-protocol"} {
		openMarker := "<!-- brunnr-code:" + sectionID + " -->"
		count := strings.Count(content, openMarker)
		if count != 1 {
			t.Errorf("CLAUDE.md contains %d occurrences of marker %q, want exactly 1:\n%s",
				count, openMarker, content)
		}
	}

	// 4. Engram MCP JSON must not contain duplicate keys.
	// A simple structural check: "command" key should appear exactly once.
	engramJSON := string(engramMCPAfterRun2)
	commandCount := strings.Count(engramJSON, `"command"`)
	if commandCount != 1 {
		t.Errorf("engram MCP JSON contains %d occurrences of \"command\", want exactly 1:\n%s",
			commandCount, engramJSON)
	}
}

// --- Custom preset integration tests ---

func TestRunInstallCustomPresetNoComponentsIsNoop(t *testing.T) {
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
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = missingBinaryLookPath

	result, err := RunInstall(
		[]string{"--agent", "claude-code", "--preset", "custom"},
		system.DetectionResult{},
	)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	// Custom preset with no components should resolve to zero ordered components.
	if len(result.Resolved.OrderedComponents) != 0 {
		t.Fatalf("expected 0 ordered components for custom preset, got %d: %v",
			len(result.Resolved.OrderedComponents), result.Resolved.OrderedComponents)
	}
}

func TestRunInstallCustomPresetExplicitSkillsFlagPopulatesSelection(t *testing.T) {
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
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = func(name string) (string, error) {
		return "/usr/local/bin/" + name, nil
	}

	result, err := RunInstall(
		[]string{
			"--agent", "claude-code",
			"--preset", "custom",
			"--component", "skills",
			"--skills", "go-testing,branch-pr",
		},
		system.DetectionResult{},
	)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	if !result.Verify.Ready {
		t.Fatalf("verification ready = false, report = %#v", result.Verify)
	}

	// Verify the explicitly requested skills were installed.
	goTestingPath := filepath.Join(home, ".claude", "skills", "go-testing", "SKILL.md")
	branchPRPath := filepath.Join(home, ".claude", "skills", "branch-pr", "SKILL.md")
	if _, err := os.Stat(goTestingPath); err != nil {
		t.Fatalf("expected go-testing skill file %q: %v", goTestingPath, err)
	}
	if _, err := os.Stat(branchPRPath); err != nil {
		t.Fatalf("expected branch-pr skill file %q: %v", branchPRPath, err)
	}

	// Regression guard for #3554: `skills` no longer has a hard dependency on
	// `sdd` in the planner graph, so selecting only the skills component must
	// NOT auto-resolve SDD (or Engram). sdd-init and the rest of the SDD
	// orchestrator suite are installed only by the SDD component itself.
	sddInitPath := filepath.Join(home, ".claude", "skills", "sdd-init", "SKILL.md")
	if _, err := os.Stat(sddInitPath); !os.IsNotExist(err) {
		t.Fatalf("sdd-init skill should NOT be installed (skills has no hard dependency on sdd): err=%v", err)
	}

	// Total = 2 explicitly requested skills only.
	skillsDir := filepath.Join(home, ".claude", "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		t.Fatalf("ReadDir(%q) error = %v", skillsDir, err)
	}
	// Count SKILL.md files across all skill subdirectories.
	var skillCount int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillMD := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
		if _, statErr := os.Stat(skillMD); statErr == nil {
			skillCount++
		}
	}
	if skillCount != 2 {
		t.Fatalf("expected 2 skill files (go-testing + branch-pr only, no SDD), got %d", skillCount)
	}
}

func TestRunInstallCustomPresetSkillsNoFlagInstallsNothing(t *testing.T) {
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
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = func(name string) (string, error) {
		return "/usr/local/bin/" + name, nil
	}

	result, err := RunInstall(
		[]string{
			"--agent", "claude-code",
			"--preset", "custom",
			"--component", "skills",
		},
		system.DetectionResult{},
	)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	if !result.Verify.Ready {
		t.Fatalf("verification ready = false, report = %#v", result.Verify)
	}

	// Regression guard for #3554: skills has no hard dependency on sdd, so
	// selecting only --component skills without --skills (which leaves
	// selectedSkillIDs() empty for the custom preset) truly installs nothing —
	// sdd is not auto-resolved and its skills are not written.
	skillsDir := filepath.Join(home, ".claude", "skills")
	// Count SKILL.md files (one per skill, excluding _shared and other non-skill dirs).
	var skillCount int
	if entries, readErr := os.ReadDir(skillsDir); readErr == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			skillMD := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
			if _, statErr := os.Stat(skillMD); statErr == nil {
				skillCount++
			}
		}
	}
	if skillCount != 0 {
		t.Fatalf("expected 0 skill files (skills has no hard dependency on sdd), got %d", skillCount)
	}
}

// Ordinary standalone skills must not duplicate files when the selection is repeated.
func TestRunInstallRepeatedOrdinarySkillsNoDuplicateSkillFiles(t *testing.T) {
	home := t.TempDir()
	restoreHome, restoreCommand, restoreLookPath := osUserHomeDir, runCommand, cmdLookPath
	t.Cleanup(func() { osUserHomeDir, runCommand, cmdLookPath = restoreHome, restoreCommand, restoreLookPath })
	osUserHomeDir = func() (string, error) { return home, nil }
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = func(name string) (string, error) { return "/usr/local/bin/" + name, nil }
	args := []string{"--agent", "claude-code", "--preset", "custom", "--component", "skills", "--skills", "go-testing,branch-pr"}
	for i := 0; i < 2; i++ {
		result, err := RunInstall(args, system.DetectionResult{})
		if err != nil || !result.Verify.Ready {
			t.Fatalf("install %d: %v, verify = %#v", i, err, result.Verify)
		}
	}
	entries, err := os.ReadDir(filepath.Join(home, ".claude", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	var count int
	for _, entry := range entries {
		if _, err := os.Stat(filepath.Join(home, ".claude", "skills", entry.Name(), "SKILL.md")); err == nil {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("expected exactly two standalone skills after repeat install, got %d", count)
	}
}

func TestRunInstallCustomPresetDryRunShowsCustomPreset(t *testing.T) {
	result, err := RunInstall(
		[]string{"--agent", "claude-code", "--preset", "custom", "--dry-run"},
		system.DetectionResult{},
	)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	if !result.DryRun {
		t.Fatalf("expected DryRun=true")
	}

	if result.Selection.Preset != model.PresetCustom {
		t.Fatalf("preset = %q, want %q", result.Selection.Preset, model.PresetCustom)
	}

	// Zero components when no --component flags provided.
	if len(result.Resolved.OrderedComponents) != 0 {
		t.Fatalf("expected 0 ordered components, got %d", len(result.Resolved.OrderedComponents))
	}

	output := RenderDryRun(result)
	if !strings.Contains(output, "custom") {
		t.Fatalf("dry-run output missing 'custom' preset name:\n%s", output)
	}
}

func TestRunInstallCustomPresetExplicitComponentsResolveCorrectly(t *testing.T) {
	result, err := RunInstall(
		[]string{
			"--agent", "claude-code",
			"--preset", "custom",
			"--component", "engram",
			"--component", "skills",
			"--component", "permissions",
			"--dry-run",
		},
		system.DetectionResult{},
	)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	// Custom selection contains exactly the three explicitly requested independent components.
	if len(result.Resolved.OrderedComponents) != 3 {
		t.Fatalf("expected 3 ordered components, got %d: %v",
			len(result.Resolved.OrderedComponents), result.Resolved.OrderedComponents)
	}

	// Verify persona, skills, context7 are NOT in the plan.
	for _, c := range result.Resolved.OrderedComponents {
		switch c {
		case model.ComponentPersona, model.ComponentContext7:
			t.Fatalf("unexpected component %q in custom preset plan", c)
		}
	}
}

// TestRunInstallWorkspaceScopeVerification verifies the user-visible 'install --scope=workspace'
// behavior from issue #785. It ensures that when installing with workspace scope:
// 1. Verification files are written to the workspace directory, NOT the home directory.
// 2. Post-apply verification succeeds because it checks the workspace skill paths.
func TestRunInstallWorkspaceScopeVerification(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()

	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}

	restoreHome := osUserHomeDir
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	t.Cleanup(func() {
		osUserHomeDir = restoreHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
		if err := os.Chdir(originalCwd); err != nil {
			t.Errorf("failed to restore working directory: %v", err)
		}
	})

	osUserHomeDir = func() (string, error) { return home, nil }
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = func(name string) (string, error) {
		return "/usr/local/bin/" + name, nil
	}

	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("failed to change working directory to temp workspace: %v", err)
	}

	// Run install with workspace scope, installing Claude Code agent and skills component
	args := []string{
		"--scope", "workspace",
		"--agent", "claude-code",
		"--component", "skills",
		"--preset", "custom",
		"--skill", "go-testing,branch-pr",
	}

	result, err := RunInstall(args, system.DetectionResult{})
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	if !result.Verify.Ready {
		t.Fatalf("post-apply verification failed, report = %#v", result.Verify)
	}

	// Assert that skill files were written to the workspace directory.
	expectedWorkspaceSkillFile := filepath.Join(workspace, ".claude", "skills", "go-testing", "SKILL.md")
	if _, err := os.Stat(expectedWorkspaceSkillFile); err != nil {
		t.Errorf("expected skill file in workspace %q, but was missing: %v", expectedWorkspaceSkillFile, err)
	}

	// Assert that no skill files were written to the home directory.
	unexpectedHomeSkillFile := filepath.Join(home, ".claude", "skills", "go-testing", "SKILL.md")
	if _, err := os.Stat(unexpectedHomeSkillFile); err == nil {
		t.Errorf("unexpected skill file found in home directory: %q", unexpectedHomeSkillFile)
	}
}

// TestRunInstall_Context7WorkspaceScope_PersistsToWorkspace verifies that executing
// a real workspace operation with --scope workspace and Context7 component:
//  1. Returns a successful result with verification ready.
//  2. Persists Context7 MCP configuration into <project-root>/.mcp.json, the file
//     Claude Code loads project-scoped MCP servers from (issue #2213).
//  3. Leaves the user's home directory settings untouched.
func TestRunInstall_Context7WorkspaceScope_PersistsToWorkspace(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()

	restoreHome := osUserHomeDir
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current working directory: %v", err)
	}

	t.Cleanup(func() {
		osUserHomeDir = restoreHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
		if err := os.Chdir(originalCwd); err != nil {
			t.Errorf("failed to restore working directory: %v", err)
		}
	})

	osUserHomeDir = func() (string, error) { return home, nil }
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = func(name string) (string, error) {
		return "/usr/local/bin/" + name, nil
	}

	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("failed to change working directory to temp workspace: %v", err)
	}

	args := []string{
		"--scope", "workspace",
		"--agent", "claude-code",
		"--component", "context7",
		"--preset", "custom",
	}

	result, err := RunInstall(args, system.DetectionResult{})
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	if !result.Verify.Ready {
		t.Fatalf("post-apply verification failed for Context7 workspace scope: %#v", result.Verify)
	}

	// Context7 MCP configuration must persist to <project-root>/.mcp.json, the
	// file Claude Code loads project-scoped MCP servers from (issue #2213).
	workspaceMCPFile := filepath.Join(workspace, ".mcp.json")
	assertFileContains(t, workspaceMCPFile, "context7")

	// The legacy .claude/settings.json key is inert for MCP discovery and must
	// not carry the managed context7 entry after install.
	if settingsRaw, err := os.ReadFile(filepath.Join(workspace, ".claude", "settings.json")); err == nil {
		if strings.Contains(string(settingsRaw), `"mcpServers"`) {
			t.Errorf("workspace .claude/settings.json must not carry mcpServers; got %s", settingsRaw)
		}
	}

	// Assert that no Context7 configuration was written to home directory settings.
	homeSettingsFile := filepath.Join(home, ".claude", "settings.json")
	if _, err := os.Stat(homeSettingsFile); err == nil {
		content, _ := os.ReadFile(homeSettingsFile)
		if strings.Contains(string(content), "context7") {
			t.Errorf("unexpected Context7 MCP config found in home settings file: %q", homeSettingsFile)
		}
	}
}

// TestRunInstall_Context7WorkspaceScope_FailurePath verifies that executing
// Context7 workspace installation when workspace target is unwriteable fails gracefully,
// returning an error and reporting verification failure.
func TestRunInstall_Context7WorkspaceScope_FailurePath(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()

	restoreHome := osUserHomeDir
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current working directory: %v", err)
	}

	t.Cleanup(func() {
		osUserHomeDir = restoreHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
		if err := os.Chdir(originalCwd); err != nil {
			t.Errorf("failed to restore working directory: %v", err)
		}
	})

	osUserHomeDir = func() (string, error) { return home, nil }
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = func(name string) (string, error) {
		return "/usr/local/bin/" + name, nil
	}

	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("failed to change working directory to temp workspace: %v", err)
	}

	// Block the <project-root>/.mcp.json write by making it a directory, so the
	// atomic file write fails. The primary workspace-scope target is .mcp.json
	// (issue #2213), not .claude/settings.json.
	blockingPath := filepath.Join(workspace, ".mcp.json")
	if err := os.Mkdir(blockingPath, 0o755); err != nil {
		t.Fatalf("failed to create blocking .mcp.json directory: %v", err)
	}

	args := []string{
		"--scope", "workspace",
		"--agent", "claude-code",
		"--component", "context7",
		"--preset", "custom",
	}

	_, err = RunInstall(args, system.DetectionResult{})
	if err == nil {
		t.Fatalf("RunInstall() with unwriteable workspace target expected error, got nil")
	}
}
