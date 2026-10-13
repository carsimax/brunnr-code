package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/components/engram"
	"github.com/carsimax/brunnr-code/internal/system"
	"github.com/carsimax/brunnr-code/internal/verify"
)

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

	detection := macOSDetectionResult()
	_, err := RunInstall(
		[]string{"--agent", "claude-code"},
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

	detection := macOSDetectionResult()
	_, err := RunInstall(
		[]string{"--agent", "claude-code"},
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

// TestRunInstallMacOSEngramDownloadsReleaseBinary verifies macOS installs
// Engram from the release download and, when the install directory was not
// on PATH before the install, names that directory in the final note.
func TestRunInstallMacOSEngramDownloadsReleaseBinary(t *testing.T) {
	home := t.TempDir()
	fakeBinDir := filepath.Join(home, ".local", "bin")
	fakeBinaryPath := filepath.Join(fakeBinDir, "engram")
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
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("SHELL", "/bin/zsh")
	addUserPath = func(dir string) error {
		os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		return nil
	}

	downloads := 0
	origDownloadFn := engramDownloadFn
	engramDownloadFn = func(profile system.PlatformProfile) (string, error) {
		downloads++
		return fakeBinaryPath, nil
	}
	t.Cleanup(func() { engramDownloadFn = origDownloadFn })

	result, err := RunInstall(
		[]string{"--agent", "claude-code"},
		macOSDetectionResult(),
	)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}
	if downloads != 1 {
		t.Fatalf("engram release download calls = %d, want 1", downloads)
	}
	for _, cmd := range recorder.get() {
		if strings.Contains(cmd, "brew") {
			t.Fatalf("macOS engram install must not run brew, got command: %s", cmd)
		}
	}
	want := "The engram binary was installed to " + fakeBinDir + ", which is not on your PATH."
	if !strings.Contains(result.Verify.FinalNote, want) {
		t.Fatalf("FinalNote = %q, want it to contain %q", result.Verify.FinalNote, want)
	}
}

// Make sure the engram package's DownloadLatestBinary is accessible.
var _ = engram.DownloadLatestBinary

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
