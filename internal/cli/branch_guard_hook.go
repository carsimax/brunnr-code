package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// BranchGuardHookCommand is the exact command the Claude PreToolUse hook runs.
// Install deduplicates and uninstall removes the hook by this string.
const BranchGuardHookCommand = "brunnr-code hook branch-guard --agent claude-code"

// maxBranchGuardPayloadBytes bounds the hook stdin payload, like the review
// stop-hook: a PreToolUse event is small, and Write content is never needed.
const maxBranchGuardPayloadBytes = 4 << 20

const branchGuardDenyReason = "Brunnr-Code blocks file edits on the default branch %q. Create a feature branch first (for example `git switch -c feat/<name>`), then retry the edit."

type branchGuardPayload struct {
	Cwd       string `json:"cwd"`
	ToolInput struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"tool_input"`
}

type branchGuardDecision struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// RunHook dispatches `brunnr-code hook <name>`, the commands that managed
// agent hooks run.
func RunHook(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: brunnr-code hook branch-guard --agent claude-code")
	}
	switch args[0] {
	case "branch-guard":
		return runBranchGuardHook(args[1:], stdin, stdout)
	default:
		return fmt.Errorf("unknown hook %q; usage: brunnr-code hook branch-guard --agent claude-code", args[0])
	}
}

// runBranchGuardHook denies an edit when the edited file's repository is on
// its default branch. Every case it cannot decide is allowed silently: a
// guard that fails closed on its own errors would stop all work.
func runBranchGuardHook(args []string, stdin io.Reader, stdout io.Writer) error {
	flags := flag.NewFlagSet("hook branch-guard", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	agent := flags.String("agent", "", "required; only claude-code is supported")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("hook branch-guard: %w", err)
	}
	if *agent != "claude-code" {
		return fmt.Errorf("hook branch-guard requires --agent claude-code; got %q", *agent)
	}

	raw, err := io.ReadAll(io.LimitReader(stdin, maxBranchGuardPayloadBytes))
	if err != nil {
		return nil
	}
	var payload branchGuardPayload
	if json.Unmarshal(raw, &payload) != nil {
		return nil
	}

	target := payload.ToolInput.FilePath
	if target == "" {
		target = payload.ToolInput.NotebookPath
	}
	dir := payload.Cwd
	if target != "" {
		dir = filepath.Dir(target)
	}
	dir = nearestExistingDir(dir)
	if dir == "" {
		return nil
	}

	branch, ok := gitOutput(dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if !ok || !isDefaultBranch(dir, branch) {
		return nil
	}

	var decision branchGuardDecision
	decision.HookSpecificOutput.HookEventName = "PreToolUse"
	decision.HookSpecificOutput.PermissionDecision = "deny"
	decision.HookSpecificOutput.PermissionDecisionReason = fmt.Sprintf(branchGuardDenyReason, branch)
	return json.NewEncoder(stdout).Encode(decision)
}

// isDefaultBranch resolves the default branch locally, without network:
// origin/HEAD, then init.defaultBranch, then main or master.
func isDefaultBranch(dir, branch string) bool {
	if remoteHead, ok := gitOutput(dir, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); ok {
		return branch == strings.TrimPrefix(remoteHead, "origin/")
	}
	if configured, ok := gitOutput(dir, "config", "--get", "init.defaultBranch"); ok {
		return branch == configured
	}
	return branch == "main" || branch == "master"
}

// nearestExistingDir walks up from dir until it finds a directory, so a Write
// that creates new directories is judged by the repository that will hold it.
func nearestExistingDir(dir string) string {
	if dir == "" {
		return ""
	}
	for {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func gitOutput(dir string, args ...string) (string, bool) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return "", false
	}
	value := strings.TrimSpace(string(out))
	return value, value != ""
}
