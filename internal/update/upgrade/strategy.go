package upgrade

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/carsimax/brunnr-code/internal/components/engram"
	"github.com/carsimax/brunnr-code/internal/system"
	"github.com/carsimax/brunnr-code/internal/update"
)

// engramDownloadFn downloads the latest engram release binary.
// Package-level var for testability — swapped in tests to avoid real network calls.
var engramDownloadFn = engram.DownloadLatestBinary

// execCommand is a package-level var declared in executor.go (same package).

// runStrategy executes the upgrade for a single tool using the appropriate strategy
// for the given platform profile.
//
// Strategy routing:
//   - binary method + engram → engramBinaryUpgrade (all platforms)
//   - binary method + other tools → manualFallback
//   - unknown method → manualFallback with explicit message
func runStrategy(ctx context.Context, r update.UpdateResult, profile system.PlatformProfile) error {
	method := r.Tool.InstallMethod
	switch method {
	case update.InstallBinary:
		return binaryUpgrade(r, profile)
	default:
		return &ManualFallbackError{
			Hint: fmt.Sprintf("upgrade %q: unsupported install method %q — please update manually. See: https://github.com/Gentleman-Programming/%s",
				r.Tool.Name, method, r.Tool.Repo),
		}
	}
}

// binaryUpgrade handles binary-release upgrades.
//
// engram has its own cross-platform binary downloader (DownloadLatestBinary)
// that works on all platforms including Windows. No other managed tool ships
// a binary upgrade path, so any other tool returns ManualFallbackError and the
// executor surfaces it as UpgradeSkipped.
func binaryUpgrade(r update.UpdateResult, profile system.PlatformProfile) error {
	if r.Tool.Name == "engram" {
		return engramBinaryUpgrade(profile)
	}
	hint := r.UpdateHint
	if hint == "" {
		hint = fmt.Sprintf("Download manually from https://github.com/%s/%s/releases", r.Tool.Owner, r.Tool.Repo)
	}
	return &ManualFallbackError{
		Hint: fmt.Sprintf("upgrade %q requires manual update: %s", r.Tool.Name, hint),
	}
}

// engramBinaryUpgrade downloads the latest engram release binary via
// engramDownloadFn. On Windows, PATH changes are persisted to the user
// registry via PowerShell.
func engramBinaryUpgrade(profile system.PlatformProfile) error {
	binaryPath, err := engramDownloadFn(profile)
	if err != nil {
		return fmt.Errorf("download engram binary: %w", err)
	}

	// Add install dir to PATH. On Windows this also persists via PowerShell (user registry).
	binDir := filepath.Dir(binaryPath)
	if err := system.AddToUserPath(binDir); err != nil {
		// Non-fatal: the binary was downloaded successfully. Warn and continue.
		fmt.Fprintf(os.Stderr, "WARNING: could not add %s to PATH: %v\n", binDir, err)
	}
	return nil
}
