package upgrade

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/carsimax/brunnr-code/internal/system"
	"github.com/carsimax/brunnr-code/internal/testenv"
	"github.com/carsimax/brunnr-code/internal/update"
)

func TestMain(m *testing.M) {
	// Neutralize ambient agent runtime-dir overrides up front so executor
	// tests resolve agent config paths from their own fixtures. Individual
	// tests keep their own explicit t.Setenv("", "") resets, which still work
	// fine on top of this baseline.
	testenv.Isolate()
	// No test may download a real engram release.
	engramDownloadFn = func(system.PlatformProfile) (string, error) {
		return filepath.Join(os.TempDir(), "engram"), nil
	}

	os.Exit(m.Run())
}

// --- TestRunStrategy_UnsupportedMethodManualFallback ---

func TestRunStrategy_UnsupportedMethodManualFallback(t *testing.T) {
	r := update.UpdateResult{
		Tool: update.ToolInfo{
			Name:          "some-tool",
			InstallMethod: update.InstallMethod("unsupported-method"),
		},
		LatestVersion: "1.0.0",
	}
	profile := system.PlatformProfile{OS: "darwin", Supported: true}

	err := runStrategy(context.Background(), r, profile)
	// Unsupported method → manual fallback error.
	if err == nil {
		t.Errorf("expected error for unsupported install method, got nil")
	}
}

// --- TestEngramUpgradeMacOSUsesDownload ---

// TestEngramUpgradeMacOSUsesDownload verifies that engram upgrade uses the
// binary download and never execs (no go install; regression for issue #160).
func TestEngramUpgradeMacOSUsesDownload(t *testing.T) {
	origExecCommand := execCommand
	origEngramDownloadFn := engramDownloadFn
	t.Cleanup(func() {
		execCommand = origExecCommand
		engramDownloadFn = origEngramDownloadFn
	})

	execCalled := false
	execCommand = func(name string, args ...string) *exec.Cmd {
		execCalled = true
		return mockCmd("false")
	}

	downloadCalled := false
	engramDownloadFn = func(profile system.PlatformProfile) (string, error) {
		downloadCalled = true
		return "/usr/local/bin/engram", nil
	}

	r := update.UpdateResult{
		Tool: update.ToolInfo{
			Name:          "engram",
			Owner:         "Gentleman-Programming",
			Repo:          "engram",
			InstallMethod: update.InstallBinary,
		},
		LatestVersion: "0.5.0",
	}
	profile := system.PlatformProfile{OS: "darwin", Supported: true}

	if err := runStrategy(context.Background(), r, profile); err != nil {
		t.Fatalf("runStrategy engram macOS: unexpected error: %v", err)
	}
	if !downloadCalled {
		t.Errorf("expected engramDownloadFn to be called for engram on macOS, but it was not")
	}
	if execCalled {
		t.Errorf("no command should be executed for engram on macOS — use binary download")
	}
}

// TestEngramBinaryUpgrade_CallsDownloadFn verifies that engramBinaryUpgrade
// delegates to engramDownloadFn (the release-download path) and never execs.
func TestEngramBinaryUpgrade_CallsDownloadFn(t *testing.T) {
	for _, tt := range []struct{ name string }{{name: "release download"}} {
		t.Run(tt.name, func(t *testing.T) {
			origDownloadFn := engramDownloadFn
			origExecCommand := execCommand
			t.Cleanup(func() {
				engramDownloadFn = origDownloadFn
				execCommand = origExecCommand
			})

			downloadCalled := false
			engramDownloadFn = func(profile system.PlatformProfile) (string, error) {
				downloadCalled = true
				return "/tmp/engram", nil
			}

			execCommand = func(name string, args ...string) *exec.Cmd {
				t.Errorf("execCommand called unexpectedly: %s %v", name, args)
				return mockCmd("echo", "unexpected")
			}

			profile := system.PlatformProfile{OS: "darwin", Supported: true}
			err := engramBinaryUpgrade(profile)
			if err != nil {
				t.Fatalf("engramBinaryUpgrade: unexpected error: %v", err)
			}
			if !downloadCalled {
				t.Error("expected engramDownloadFn to be called, but it was not")
			}
		})
	}
}
