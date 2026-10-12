package upgrade

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/carsimax/brunnr-code/internal/components/engram"
	"github.com/carsimax/brunnr-code/internal/system"
	"github.com/carsimax/brunnr-code/internal/update"
)

// engramDownloadFn downloads the latest engram release binary.
// Package-level var for testability — swapped in tests to avoid real network calls.
var engramDownloadFn = engram.DownloadLatestBinary

// execCommand is a package-level var declared in executor.go (same package).

type strategyOutcome struct {
	exitRequested bool
}

// runStrategy executes the upgrade for a single tool using the appropriate strategy
// for the given platform profile.
//
// Strategy routing:
//   - brew profile → brewUpgrade (regardless of tool's declared method)
//   - binary method + engram → engramBinaryUpgrade (all platforms)
//   - binary method + other tools → manualFallback
//   - unknown method → manualFallback with explicit message
func runStrategy(ctx context.Context, r update.UpdateResult, profile system.PlatformProfile) (bool, error) {
	ownership := update.HomebrewNone
	if profile.PackageManager == "brew" {
		var err error
		ownership, err = homebrewOwnershipDetector(r.Tool.Name)
		if err != nil {
			return false, fmt.Errorf("detect Homebrew ownership for %s: %w", r.Tool.Name, err)
		}
	}
	method := effectiveMethod(r.Tool, profile)
	if ownership != update.HomebrewNone {
		method = update.InstallBrew
	}

	switch method {
	case update.InstallBrew:
		return false, brewUpgrade(ctx, r, ownership)
	case update.InstallBinary:
		return false, binaryUpgrade(r, profile)
	default:
		return false, &ManualFallbackError{
			Hint: fmt.Sprintf("upgrade %q: unsupported install method %q — please update manually. See: https://github.com/Gentleman-Programming/%s",
				r.Tool.Name, method, r.Tool.Repo),
		}
	}
}

func runStrategyWithOutcome(ctx context.Context, r update.UpdateResult, profile system.PlatformProfile) (strategyOutcome, error) {
	exitRequested, err := runStrategy(ctx, r, profile)
	return strategyOutcome{exitRequested: exitRequested}, err
}

// brewUpgrade runs `brew update` (non-fatal) then `brew upgrade <toolName>`.
//
// brew update refreshes the local formula cache so that Homebrew is aware of
// new versions published since the user last ran it. If update fails (e.g. no
// network), the upgrade is still attempted using the existing cache — a stale
// cache is better than no upgrade at all.
func brewUpgrade(ctx context.Context, r update.UpdateResult, ownership update.HomebrewOwnership) error {
	toolName := r.Tool.Name
	flag := "--" + string(ownership)
	// Ensure the Gentleman-Programming homebrew tap is present before upgrading.
	// Non-fatal: brew tap is a no-op when already present; if it fails for any other
	// reason, the subsequent brew upgrade will surface the real error. See issue #455:
	// without this, a lost tap (untap, machine swap, brew cleanup) makes upgrades fail
	// with "No available formula" for engram.
	tapCmd := execCommand("brew", "tap", "Gentleman-Programming/homebrew-tap")
	tapCmd.Stdin = nil
	_ = tapCmd.Run()

	// Trust only the Gentleman Programming artifact being upgraded. Homebrew 6 can
	// require explicit trust for non-official taps; this is intentionally scoped to
	// our formula/cask, not the whole tap or third-party taps. Older Homebrew versions
	// may not support `brew trust`, so this is non-fatal and the upgrade output
	// below remains the source of truth.
	trustCmd := execCommand("brew", "trust", flag, gentlemanProgrammingTapRef(toolName))
	trustCmd.Stdin = nil
	_ = trustCmd.Run()

	// Update Homebrew formula cache before upgrading.
	// Non-fatal: if update fails (e.g. no network), attempt upgrade with existing cache.
	updateCmd := execCommand("brew", "update")
	updateCmd.Stdin = nil
	_ = updateCmd.Run() // ignore error intentionally

	upgradeCmd := execCommand("brew", "upgrade", flag, toolName)
	upgradeCmd.Stdin = nil
	if out, err := upgradeCmd.CombinedOutput(); err != nil {
		return formatBrewUpgradeError(toolName, ownership, err, string(out))
	}
	if ownership == update.HomebrewCask {
		return verifyLegacyCaskTarget(r)
	}
	return nil
}

var brewVersionRegexp = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?(?:\+[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?`)

func verifyLegacyCaskTarget(r update.UpdateResult) error {
	migration := legacyCaskMigration(r.Tool.Name)
	if len(r.Tool.DetectCmd) == 0 {
		return fmt.Errorf("cannot verify Homebrew cask %s after upgrade; %s", r.Tool.Name, migration)
	}
	cmd := execCommand(r.Tool.DetectCmd[0], r.Tool.DetectCmd[1:]...)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("verify Homebrew cask %s after upgrade: %w; %s", r.Tool.Name, err, migration)
	}
	installed := brewVersionRegexp.FindString(string(out))
	target := brewVersionRegexp.FindString(r.LatestVersion)
	if installed == "" || target == "" || installed != target {
		return fmt.Errorf("Homebrew cask %s remains at %q after targeting %q; %s", r.Tool.Name, installed, target, migration)
	}
	return nil
}

func gentlemanProgrammingTapRef(toolName string) string {
	return "gentleman-programming/tap/" + strings.TrimSpace(toolName)
}

func homebrewTrustFlag(toolName string) string {
	if strings.TrimSpace(toolName) == "engram" {
		return "--cask"
	}
	return "--formula"
}

func legacyCaskMigration(toolName string) string {
	return fmt.Sprintf("migrate the legacy cask to the current formula:\n  brew uninstall --cask %s\n  brew install --formula %s", strings.TrimSpace(toolName), gentlemanProgrammingTapRef(toolName))
}

func formatBrewUpgradeError(toolName string, ownership update.HomebrewOwnership, err error, output string) error {
	message := fmt.Sprintf("brew upgrade --%s %s: %v (output: %s)", ownership, toolName, err, output)
	if advice := homebrewFailureAdvice(toolName, output, ownership); advice != "" {
		message += "\n\n" + advice
	}
	if ownership == update.HomebrewCask {
		message += "\n\n" + legacyCaskMigration(toolName)
	}
	return errors.New(message)
}

func homebrewFailureAdvice(toolName string, output string, detected ...update.HomebrewOwnership) string {
	lower := strings.ToLower(output)
	ref := gentlemanProgrammingTapRef(toolName)
	flag := homebrewTrustFlag(toolName)
	if len(detected) > 0 {
		flag = "--" + string(detected[0])
	}

	if strings.Contains(lower, "untrusted tap") || strings.Contains(lower, "tap trust is required") || strings.Contains(lower, "homebrew_require_tap_trust") {
		artifact := strings.TrimPrefix(flag, "--")
		if strings.Contains(lower, "--cask") || strings.Contains(lower, "load cask") {
			flag = "--cask"
			artifact = "cask"
		} else if strings.Contains(lower, "--formula") || strings.Contains(lower, "load formula") {
			flag = "--formula"
			artifact = "formula"
		}
		return fmt.Sprintf("Homebrew requires explicit trust for external taps. Trust only this Brunnr-Code %s, then retry:\n  brew trust %s %s\n  brew upgrade %s %s", artifact, flag, ref, flag, toolName)
	}

	if strings.Contains(lower, "bubblewrap is installed but cannot create a rootless sandbox") ||
		strings.Contains(lower, "rootless sandbox") ||
		strings.Contains(lower, "homebrew_no_sandbox_linux") {
		return "Homebrew on Linux could not create its Bubblewrap rootless sandbox. This requires an explicit admin/security decision: enabling unprivileged user namespaces lets Homebrew use its sandbox but changes host kernel/AppArmor policy. If acceptable, run:\n  sudo sysctl -w kernel.unprivileged_userns_clone=1\n  sudo sysctl -w user.max_user_namespaces=28633\n  sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0 || true\n\nFinal workaround if your distro policy forbids this sandbox:\n  HOMEBREW_NO_SANDBOX_LINUX=1 brew upgrade " + flag + " " + toolName
	}

	return ""
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
