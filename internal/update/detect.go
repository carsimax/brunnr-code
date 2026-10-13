package update

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Package-level vars for testability (swap in tests via t.Cleanup).
var (
	execCommand = exec.Command
	lookPath    = exec.LookPath
	userHomeDir = os.UserHomeDir
	osStat      = os.Stat
)

// versionRegexp extracts a semver-like version from command output.
// Same pattern as internal/system/deps.go for consistency.
var versionRegexp = regexp.MustCompile(`(\d+\.\d+(?:\.\d+)?)`)

// devVersionRegexp matches common unversioned source-build output like
// "engram dev" or "version: dev".
var devVersionRegexp = regexp.MustCompile(`(?i)(?:^|\s)dev(?:$|\s)`)

// detectInstalledVersion determines the installed version of a tool: it
// checks LookPath then runs the detect command.
func detectInstalledVersion(ctx context.Context, tool ToolInfo) string {
	if len(tool.DetectCmd) == 0 {
		return ""
	}

	binary := tool.DetectCmd[0]
	if _, err := lookPath(binary); err != nil {
		// LookPath failed — the running process PATH may be stale (the binary
		// installer may have placed engram in ~/.local/bin, which a shell only
		// picks up in a new session). Fall back to checking known install locations on disk.
		fullPath := findFallbackBinary(tool)
		if fullPath == "" {
			return "" // binary not found anywhere
		}
		// Use the full filesystem path to invoke the binary directly, bypassing PATH.
		binary = fullPath
	}

	// Apply a bounded timeout so a hanging binary (e.g. engram stuck on DB
	// lock) cannot block update/upgrade flows forever.
	detectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := execCommand(binary, tool.DetectCmd[1:]...)

	// Start and wait on the command in this execution path so no goroutine reads
	// mutable exec.Cmd state while Start initializes it. The cancellation callback
	// only receives the concurrency-safe Process after Start has completed.
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := detectCtx.Err(); err != nil {
		return ""
	}
	if err := cmd.Start(); err != nil {
		return ""
	}
	process := cmd.Process
	killDone := make(chan struct{})
	stopKill := context.AfterFunc(detectCtx, func() {
		defer close(killDone)
		_ = process.Kill()
	})
	err := cmd.Wait()
	if !stopKill() {
		<-killDone
	}
	if err != nil {
		return "" // command failed or timed out — binary exists but version unknown
	}

	return parseVersionFromOutput(strings.TrimSpace(out.String()))
}

// findFallbackBinary checks the tool's FallbackPaths for a binary that exists
// on disk. It returns the first path that stat succeeds on, or "" if none found.
// This is used when exec.LookPath fails due to a stale process PATH (e.g.,
// right after install, before a new shell session picks up ~/.local/bin).
func findFallbackBinary(tool ToolInfo) string {
	if tool.FallbackPaths == nil {
		return ""
	}
	homeDir, _ := userHomeDir()
	for _, path := range tool.FallbackPaths(homeDir) {
		if _, err := osStat(path); err == nil {
			return path
		}
	}
	return ""
}

// parseVersionFromOutput extracts the first semver-like pattern from raw output.
func parseVersionFromOutput(output string) string {
	if output == "" {
		return ""
	}

	if devVersionRegexp.MatchString(output) {
		return "dev"
	}

	match := versionRegexp.FindStringSubmatch(output)
	if len(match) >= 2 {
		return match[1]
	}

	return ""
}
