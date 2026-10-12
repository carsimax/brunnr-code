package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/components/engram"
	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/system"
	"github.com/carsimax/brunnr-code/internal/verify"
)

// TestRunInstallLinuxEngramUsesDownloadNotGoInstall verifies that after the fix,
// Linux engram installation does NOT use "go install" but instead calls
// DownloadLatestBinary (i.e. no "go install" in recorder.get()).
func TestRunInstallLinuxEngramUsesDownloadNotGoInstall(t *testing.T) {
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

	// Override the engram download function to succeed without hitting GitHub.
	origDownloadFn := engramDownloadFn
	engramDownloadFn = func(profile system.PlatformProfile) (string, error) {
		// Simulate a successful binary download to a temp path.
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

	// Must NOT have called "go install" for engram.
	for _, cmd := range recorder.get() {
		if strings.Contains(cmd, "go install") && strings.Contains(cmd, "engram") {
			t.Fatalf("Linux engram install should NOT use go install, got command: %s", cmd)
		}
	}
}

// TestRunInstallEngramDownloadAddsBinDirToPath verifies that after downloading
// the engram binary, its directory is prepended to PATH so that subsequent
// commands (engram setup, resolveEngramCommand) can find it.
func TestRunInstallEngramDownloadAddsBinDirToPath(t *testing.T) {
	home := t.TempDir()
	restoreHome := osUserHomeDir
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	restorePath := os.Getenv("PATH")
	t.Cleanup(func() {
		osUserHomeDir = restoreHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
		os.Setenv("PATH", restorePath)
	})

	osUserHomeDir = func() (string, error) { return home, nil }
	cmdLookPath = missingBinaryLookPath
	recorder := &commandRecorder{}
	runCommand = recorder.record

	fakeBinDir := filepath.Join(home, "engram-bin")
	os.MkdirAll(fakeBinDir, 0o755)
	fakeBinaryPath := filepath.Join(fakeBinDir, "engram")

	origDownloadFn := engramDownloadFn
	engramDownloadFn = func(profile system.PlatformProfile) (string, error) {
		return fakeBinaryPath, nil
	}
	t.Cleanup(func() { engramDownloadFn = origDownloadFn })

	detection := linuxDetectionResult(system.LinuxDistroUbuntu, "apt")
	_, err := RunInstall(
		[]string{"--agent", "claude-code", "--component", "engram"},
		detection,
	)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	currentPath := os.Getenv("PATH")
	if !strings.Contains(currentPath, fakeBinDir) {
		t.Fatalf("PATH should contain engram bin dir %q after download, got PATH=%q", fakeBinDir, currentPath)
	}
}

func TestRunInstallFreshEngramDownloadUsesDownloadedBinaryForVersionProbeAndSetup(t *testing.T) {
	home := t.TempDir()
	fakeBinDir := filepath.Join(home, "engram-bin")
	fakeBinaryPath := filepath.Join(fakeBinDir, "engram")

	restoreHome := osUserHomeDir
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	restoreAddUserPath := addUserPath
	restoreVerifyVersionCommand := verifyEngramVersionCommand
	restoreProbeCommand := probeEngramProtocolFlagCommand
	t.Cleanup(func() {
		osUserHomeDir = restoreHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
		addUserPath = restoreAddUserPath
		verifyEngramVersionCommand = restoreVerifyVersionCommand
		probeEngramProtocolFlagCommand = restoreProbeCommand
	})

	osUserHomeDir = func() (string, error) { return home, nil }
	cmdLookPath = missingBinaryLookPath
	recorder := &commandRecorder{}
	runCommand = recorder.record
	addUserPath = func(dir string) error {
		if dir != fakeBinDir {
			t.Fatalf("addUserPath() dir = %q, want %q", dir, fakeBinDir)
		}
		return os.ErrPermission
	}
	var versionCommand string
	verifyEngramVersionCommand = func(command string) (string, error) {
		versionCommand = command
		return "engram 1.18.0", nil
	}
	var probeCommand string
	probeEngramProtocolFlagCommand = func(_ context.Context, command string) (string, error) {
		probeCommand = command
		return "Usage: engram setup <slug> [--protocol=slim|full]\n", nil
	}

	origDownloadFn := engramDownloadFn
	engramDownloadFn = func(profile system.PlatformProfile) (string, error) {
		return fakeBinaryPath, nil
	}
	t.Cleanup(func() { engramDownloadFn = origDownloadFn })

	detection := linuxDetectionResult(system.LinuxDistroUbuntu, "apt")
	_, err := RunInstall(
		[]string{"--agent", "claude-code", "--component", "engram"},
		detection,
	)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	commands := recorder.get()
	foundSetupWithDownloadedBinary := false
	for _, cmd := range commands {
		if strings.HasPrefix(cmd, fakeBinaryPath+" setup ") {
			foundSetupWithDownloadedBinary = true
		}
		if strings.HasPrefix(cmd, "engram setup ") {
			t.Fatalf("engram setup should use downloaded binary %q, got commands: %v", fakeBinaryPath, commands)
		}
	}
	if !foundSetupWithDownloadedBinary {
		t.Fatalf("expected setup to use downloaded binary %q, got commands: %v", fakeBinaryPath, commands)
	}
	if versionCommand != fakeBinaryPath {
		t.Fatalf("expected version gate to use downloaded binary %q, got %q", fakeBinaryPath, versionCommand)
	}
	if probeCommand != fakeBinaryPath {
		t.Fatalf("expected protocol probe to use downloaded binary %q, got %q", fakeBinaryPath, probeCommand)
	}
}

// TestRunInstallWindowsEngramUsesDownloadNotGoInstall verifies Windows path.
func TestRunInstallWindowsEngramUsesDownloadNotGoInstall(t *testing.T) {
	home := t.TempDir()
	restoreHome := osUserHomeDir
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	restoreAddUserPath := addUserPath
	t.Cleanup(func() {
		osUserHomeDir = restoreHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
		addUserPath = restoreAddUserPath
	})

	osUserHomeDir = func() (string, error) { return home, nil }
	cmdLookPath = missingBinaryLookPath
	recorder := &commandRecorder{}
	runCommand = recorder.record
	fakeBinDir := filepath.Join(home, "engram-bin")
	fakeBinaryPath := filepath.Join(fakeBinDir, "engram.exe")
	var addedPath string
	addUserPath = func(dir string) error {
		addedPath = dir
		return nil
	}

	origDownloadFn := engramDownloadFn
	engramDownloadFn = func(profile system.PlatformProfile) (string, error) {
		return fakeBinaryPath, nil
	}
	t.Cleanup(func() { engramDownloadFn = origDownloadFn })

	detection := system.DetectionResult{
		System: system.SystemInfo{
			OS:        "windows",
			Arch:      "amd64",
			Supported: true,
			Profile: system.PlatformProfile{
				OS:             "windows",
				PackageManager: "winget",
				Supported:      true,
			},
		},
	}

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
	if addedPath != fakeBinDir {
		t.Fatalf("Windows engram install should request adding downloaded binary dir to PATH, got %q", addedPath)
	}

	// Must NOT have called "go install" for engram.
	for _, cmd := range recorder.get() {
		if strings.Contains(cmd, "go install") && strings.Contains(cmd, "engram") {
			t.Fatalf("Windows engram install should NOT use go install, got command: %s", cmd)
		}
	}
}

func TestRunInstallWindowsRefreshesEngramWhenDuplicatePathEntriesShadowManagedBinary(t *testing.T) {
	home := t.TempDir()
	staleDir := filepath.Join(home, "stale")
	managedDir := filepath.Join(home, "managed")
	if err := os.MkdirAll(staleDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(staleDir) error = %v", err)
	}
	if err := os.MkdirAll(managedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(managedDir) error = %v", err)
	}
	staleBinary := filepath.Join(staleDir, "engram.exe")
	managedBinary := filepath.Join(managedDir, "engram.exe")
	if err := os.WriteFile(staleBinary, []byte("stale"), 0o755); err != nil {
		t.Fatalf("WriteFile(staleBinary) error = %v", err)
	}
	if err := os.WriteFile(managedBinary, []byte("managed"), 0o755); err != nil {
		t.Fatalf("WriteFile(managedBinary) error = %v", err)
	}

	restoreHome := osUserHomeDir
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	restorePathEntries := pathEnvEntries
	restoreEnsureUserPathFirst := ensureUserPathFirst
	restoreUserPathEntries := userPathEntries
	restoreVerifyVersionCommand := verifyEngramVersionCommand
	restoreProbeCommand := probeEngramProtocolFlagCommand
	restorePath := os.Getenv("PATH")
	t.Cleanup(func() {
		osUserHomeDir = restoreHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
		pathEnvEntries = restorePathEntries
		ensureUserPathFirst = restoreEnsureUserPathFirst
		userPathEntries = restoreUserPathEntries
		verifyEngramVersionCommand = restoreVerifyVersionCommand
		probeEngramProtocolFlagCommand = restoreProbeCommand
		os.Setenv("PATH", restorePath)
	})

	osUserHomeDir = func() (string, error) { return home, nil }
	cmdLookPath = func(name string) (string, error) {
		if name == "engram" {
			return staleBinary, nil
		}
		return missingBinaryLookPath(name)
	}
	pathEnvEntries = func(profile system.PlatformProfile) []string {
		return []string{staleDir, managedDir}
	}
	userPathEntries = func(goos string) ([]string, error) {
		return []string{staleDir, managedDir}, nil
	}
	recorder := &commandRecorder{}
	runCommand = recorder.record
	var versionCommand string
	verifyEngramVersionCommand = func(command string) (string, error) {
		versionCommand = command
		return "engram 1.18.0", nil
	}
	var probeCommand string
	probeEngramProtocolFlagCommand = func(_ context.Context, command string) (string, error) {
		probeCommand = command
		return "Usage: engram setup <slug> [--protocol=slim|full]\n", nil
	}
	var repairedPath string
	ensureUserPathFirst = func(dir string) error {
		repairedPath = dir
		return os.Setenv("PATH", dir+";"+staleDir)
	}

	downloadCalled := false
	origDownloadFn := engramDownloadFn
	engramDownloadFn = func(profile system.PlatformProfile) (string, error) {
		downloadCalled = true
		return managedBinary, nil
	}
	t.Cleanup(func() { engramDownloadFn = origDownloadFn })

	step := componentApplyStep{
		component: model.ComponentEngram,
		homeDir:   home,
		agents:    []model.AgentID{model.AgentCodex},
		profile: system.PlatformProfile{
			OS:             "windows",
			PackageManager: "winget",
			Supported:      true,
		},
	}

	err := step.Run()
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}
	if !downloadCalled {
		t.Fatal("engramDownloadFn was not called for shadowed duplicate Windows PATH entries")
	}
	if repairedPath != managedDir {
		t.Fatalf("persistent user PATH repair should prioritize managed dir %q, got %q", managedDir, repairedPath)
	}
	if gotPath := os.Getenv("PATH"); !strings.HasPrefix(gotPath, managedDir+";") {
		t.Fatalf("PATH should prioritize managed engram for current install, got %q", gotPath)
	}
	commands := recorder.get()
	foundSetupWithManagedBinary := false
	for _, cmd := range commands {
		if strings.HasPrefix(cmd, managedBinary+" setup ") {
			foundSetupWithManagedBinary = true
		}
		if strings.HasPrefix(cmd, "engram setup ") || strings.HasPrefix(cmd, staleBinary+" setup ") {
			t.Fatalf("engram setup should use refreshed managed binary %q, got commands: %v", managedBinary, commands)
		}
	}
	if !foundSetupWithManagedBinary {
		t.Fatalf("expected engram setup to use refreshed managed binary %q, got commands: %v", managedBinary, commands)
	}
	if versionCommand != managedBinary {
		t.Fatalf("expected version gate to use refreshed managed binary %q, got %q", managedBinary, versionCommand)
	}
	if probeCommand != managedBinary {
		t.Fatalf("expected protocol probe to use refreshed managed binary %q, got %q", managedBinary, probeCommand)
	}
}

func TestRunInstallWindowsFailsWhenShadowedEngramPathCannotBePersistentlyRepaired(t *testing.T) {
	home := t.TempDir()
	staleDir := filepath.Join(home, "stale")
	managedDir := filepath.Join(home, "managed")
	if err := os.MkdirAll(staleDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(staleDir) error = %v", err)
	}
	if err := os.MkdirAll(managedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(managedDir) error = %v", err)
	}
	staleBinary := filepath.Join(staleDir, "engram.exe")
	managedBinary := filepath.Join(managedDir, "engram.exe")
	if err := os.WriteFile(staleBinary, []byte("stale"), 0o755); err != nil {
		t.Fatalf("WriteFile(staleBinary) error = %v", err)
	}
	if err := os.WriteFile(managedBinary, []byte("managed"), 0o755); err != nil {
		t.Fatalf("WriteFile(managedBinary) error = %v", err)
	}

	restoreHome := osUserHomeDir
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	restorePathEntries := pathEnvEntries
	restoreEnsureUserPathFirst := ensureUserPathFirst
	restoreUserPathEntries := userPathEntries
	t.Cleanup(func() {
		osUserHomeDir = restoreHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
		pathEnvEntries = restorePathEntries
		ensureUserPathFirst = restoreEnsureUserPathFirst
		userPathEntries = restoreUserPathEntries
	})

	osUserHomeDir = func() (string, error) { return home, nil }
	cmdLookPath = func(name string) (string, error) {
		if name == "engram" {
			return staleBinary, nil
		}
		return missingBinaryLookPath(name)
	}
	pathEnvEntries = func(profile system.PlatformProfile) []string {
		return []string{staleDir, managedDir}
	}
	userPathEntries = func(goos string) ([]string, error) {
		return []string{staleDir, managedDir}, nil
	}
	recorder := &commandRecorder{}
	runCommand = recorder.record
	ensureUserPathFirst = func(dir string) error {
		return os.ErrPermission
	}

	origDownloadFn := engramDownloadFn
	engramDownloadFn = func(profile system.PlatformProfile) (string, error) {
		return managedBinary, nil
	}
	t.Cleanup(func() { engramDownloadFn = origDownloadFn })

	step := componentApplyStep{
		component: model.ComponentEngram,
		homeDir:   home,
		agents:    []model.AgentID{model.AgentCodex},
		profile: system.PlatformProfile{
			OS:             "windows",
			PackageManager: "winget",
			Supported:      true,
		},
	}

	err := step.Run()
	if err == nil {
		t.Fatal("componentApplyStep.Run() should fail when shadowed Engram PATH cannot be persistently repaired")
	}
	errText := err.Error()
	for _, want := range []string{"repair Windows Engram PATH shadowing", "Move", managedDir, staleDir, "rerun install"} {
		if !strings.Contains(errText, want) {
			t.Fatalf("error %q missing actionable text %q", errText, want)
		}
	}
	if commands := recorder.get(); len(commands) != 0 {
		t.Fatalf("engram setup should not run after persistent PATH repair fails, got commands: %v", commands)
	}
}

func TestRunInstallWindowsFailsWhenShadowedEngramPathIsNotInUserPath(t *testing.T) {
	home := t.TempDir()
	staleMachineDir := filepath.Join(home, "machine-stale")
	managedDir := filepath.Join(home, "managed")
	if err := os.MkdirAll(staleMachineDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(staleMachineDir) error = %v", err)
	}
	if err := os.MkdirAll(managedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(managedDir) error = %v", err)
	}
	staleBinary := filepath.Join(staleMachineDir, "engram.exe")
	managedBinary := filepath.Join(managedDir, "engram.exe")
	if err := os.WriteFile(staleBinary, []byte("stale"), 0o755); err != nil {
		t.Fatalf("WriteFile(staleBinary) error = %v", err)
	}
	if err := os.WriteFile(managedBinary, []byte("managed"), 0o755); err != nil {
		t.Fatalf("WriteFile(managedBinary) error = %v", err)
	}

	restoreHome := osUserHomeDir
	restoreCommand := runCommand
	restoreLookPath := cmdLookPath
	restorePathEntries := pathEnvEntries
	restoreEnsureUserPathFirst := ensureUserPathFirst
	restoreUserPathEntries := userPathEntries
	t.Cleanup(func() {
		osUserHomeDir = restoreHome
		runCommand = restoreCommand
		cmdLookPath = restoreLookPath
		pathEnvEntries = restorePathEntries
		ensureUserPathFirst = restoreEnsureUserPathFirst
		userPathEntries = restoreUserPathEntries
	})

	osUserHomeDir = func() (string, error) { return home, nil }
	cmdLookPath = func(name string) (string, error) {
		if name == "engram" {
			return staleBinary, nil
		}
		return missingBinaryLookPath(name)
	}
	pathEnvEntries = func(profile system.PlatformProfile) []string {
		return []string{staleMachineDir, managedDir}
	}
	userPathEntries = func(goos string) ([]string, error) {
		return []string{managedDir}, nil
	}
	recorder := &commandRecorder{}
	runCommand = recorder.record
	ensureUserPathFirst = func(dir string) error {
		t.Fatal("ensureUserPathFirst should not run when stale entry is outside user PATH")
		return nil
	}

	origDownloadFn := engramDownloadFn
	engramDownloadFn = func(profile system.PlatformProfile) (string, error) {
		return managedBinary, nil
	}
	t.Cleanup(func() { engramDownloadFn = origDownloadFn })

	step := componentApplyStep{
		component: model.ComponentEngram,
		homeDir:   home,
		agents:    []model.AgentID{model.AgentCodex},
		profile: system.PlatformProfile{
			OS:             "windows",
			PackageManager: "winget",
			Supported:      true,
		},
	}

	err := step.Run()
	if err == nil {
		t.Fatal("componentApplyStep.Run() should fail when the stale Engram path is not in user PATH")
	}
	errText := err.Error()
	for _, want := range []string{"cannot safely repair PATH order", "not in the user PATH", "Machine/System PATH", managedDir, staleMachineDir, "rerun install"} {
		if !strings.Contains(errText, want) {
			t.Fatalf("error %q missing actionable text %q", errText, want)
		}
	}
	if commands := recorder.get(); len(commands) != 0 {
		t.Fatalf("engram setup should not run after unsafe PATH repair detection, got commands: %v", commands)
	}
}

// TestRunInstallMacOSEngramStillUsesBrew verifies macOS unchanged.
func TestRunInstallMacOSEngramStillUsesBrew(t *testing.T) {
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

	// DownloadFn should NOT be called for macOS (brew handles it).
	origDownloadFn := engramDownloadFn
	engramDownloadFn = func(profile system.PlatformProfile) (string, error) {
		t.Error("DownloadLatestBinary should NOT be called on macOS (brew handles it)")
		return "", nil
	}
	t.Cleanup(func() { engramDownloadFn = origDownloadFn })

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

	// Must use brew install engram.
	commands := recorder.get()
	foundBrew := false
	for _, cmd := range commands {
		if strings.Contains(cmd, "brew install engram") {
			foundBrew = true
		}
	}
	if !foundBrew {
		t.Fatalf("expected brew install engram on macOS, got commands: %v", commands)
	}
}

// Make sure the engram package's DownloadLatestBinary is accessible.
var _ = engram.DownloadLatestBinary

// Engram is user-owned: installing ordinary guidance does not attempt an implicit download.
func TestRunInstallClaudeGuidanceDoesNotAutoInstallEngram(t *testing.T) {
	home := t.TempDir()
	restoreHome, restoreCommand, restoreLookPath, restoreDownload := osUserHomeDir, runCommand, cmdLookPath, engramDownloadFn
	t.Cleanup(func() {
		osUserHomeDir, runCommand, cmdLookPath, engramDownloadFn = restoreHome, restoreCommand, restoreLookPath, restoreDownload
	})
	osUserHomeDir = func() (string, error) { return home, nil }
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = missingBinaryLookPath
	engramDownloadFn = func(system.PlatformProfile) (string, error) {
		t.Fatal("ordinary guidance must not download Engram")
		return "", nil
	}
	result, err := RunInstall([]string{"--agent", "claude-code", "--components", "persona"}, linuxDetectionResult(system.LinuxDistroUbuntu, "apt"))
	if err != nil || !result.Verify.Ready {
		t.Fatalf("install: %v, report: %#v", err, result.Verify)
	}
	path := filepath.Join(home, ".claude", "CLAUDE.md")
	if !verifyReportRequiresFile(result.Verify, path) {
		t.Fatalf("guidance not verified at %q: %#v", path, result.Verify)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(err) {
		t.Fatalf("implicit Engram MCP config: %v", err)
	}
}

// verifyReportRequiresFile reports whether the post-apply verification listed
// path as a required file, regardless of whether that check passed.
func verifyReportRequiresFile(report verify.Report, path string) bool {
	for _, check := range report.Checks {
		if check.ID == "verify:file:"+path {
			return true
		}
	}
	return false
}
