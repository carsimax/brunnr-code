package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// isolateBranchGuardGit keeps the user's and the system's git config (for
// example a global init.defaultBranch) out of the default-branch decision.
func isolateBranchGuardGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
}

func branchGuardGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newBranchGuardRepo creates a repository with one commit on branch.
func newBranchGuardRepo(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	branchGuardGit(t, dir, "init", "-q", "-b", branch)
	branchGuardGit(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

func branchGuardInput(cwd, filePath string) string {
	payload := map[string]any{
		"session_id":      "s",
		"cwd":             cwd,
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": filePath},
	}
	raw, _ := json.Marshal(payload)
	return string(raw)
}

// runBranchGuard runs the hook and returns its stdout.
func runBranchGuard(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	if args == nil {
		args = []string{"branch-guard", "--agent", "claude-code"}
	}
	var stdout bytes.Buffer
	if err := RunHook(args, strings.NewReader(stdin), &stdout); err != nil {
		t.Fatalf("RunHook() error = %v", err)
	}
	return stdout.String()
}

func assertBranchGuardDenies(t *testing.T, out string) {
	t.Helper()
	var got struct {
		HookSpecificOutput struct {
			HookEventName            string `json:"hookEventName"`
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not the PreToolUse decision JSON: %v\nstdout: %q", err, out)
	}
	h := got.HookSpecificOutput
	if h.HookEventName != "PreToolUse" || h.PermissionDecision != "deny" {
		t.Fatalf("decision = %+v, want PreToolUse deny", h)
	}
	if !strings.Contains(h.PermissionDecisionReason, "git switch -c") {
		t.Fatalf("reason %q must tell the agent to create a branch", h.PermissionDecisionReason)
	}
}

func assertBranchGuardAllows(t *testing.T, out string) {
	t.Helper()
	if out != "" {
		t.Fatalf("stdout = %q, want no output (allow)", out)
	}
}

func TestBranchGuardDeniesEditOnDefaultBranch(t *testing.T) {
	isolateBranchGuardGit(t)
	for _, branch := range []string{"main", "master"} {
		t.Run(branch, func(t *testing.T) {
			repo := newBranchGuardRepo(t, branch)
			assertBranchGuardDenies(t, runBranchGuard(t, branchGuardInput(repo, filepath.Join(repo, "a.go"))))
		})
	}
}

func TestBranchGuardAllowsFeatureBranch(t *testing.T) {
	isolateBranchGuardGit(t)
	repo := newBranchGuardRepo(t, "main")
	branchGuardGit(t, repo, "switch", "-q", "-c", "feat/x")
	assertBranchGuardAllows(t, runBranchGuard(t, branchGuardInput(repo, filepath.Join(repo, "a.go"))))
}

func TestBranchGuardAllowsDetachedHead(t *testing.T) {
	isolateBranchGuardGit(t)
	repo := newBranchGuardRepo(t, "main")
	branchGuardGit(t, repo, "switch", "-q", "--detach")
	assertBranchGuardAllows(t, runBranchGuard(t, branchGuardInput(repo, filepath.Join(repo, "a.go"))))
}

func TestBranchGuardAllowsOutsideGit(t *testing.T) {
	isolateBranchGuardGit(t)
	dir := t.TempDir()
	assertBranchGuardAllows(t, runBranchGuard(t, branchGuardInput(dir, filepath.Join(dir, "a.go"))))
}

func TestBranchGuardUsesOriginHead(t *testing.T) {
	isolateBranchGuardGit(t)
	repo := newBranchGuardRepo(t, "trunk")
	branchGuardGit(t, repo, "update-ref", "refs/remotes/origin/trunk", "HEAD")
	branchGuardGit(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
	assertBranchGuardDenies(t, runBranchGuard(t, branchGuardInput(repo, filepath.Join(repo, "a.go"))))

	// With origin/HEAD known, main is an ordinary branch.
	branchGuardGit(t, repo, "switch", "-q", "-c", "main")
	assertBranchGuardAllows(t, runBranchGuard(t, branchGuardInput(repo, filepath.Join(repo, "a.go"))))
}

func TestBranchGuardUsesInitDefaultBranch(t *testing.T) {
	isolateBranchGuardGit(t)
	repo := newBranchGuardRepo(t, "develop")
	branchGuardGit(t, repo, "config", "init.defaultBranch", "develop")
	assertBranchGuardDenies(t, runBranchGuard(t, branchGuardInput(repo, filepath.Join(repo, "a.go"))))
}

func TestBranchGuardUsesTargetFileRepository(t *testing.T) {
	isolateBranchGuardGit(t)
	feature := newBranchGuardRepo(t, "main")
	branchGuardGit(t, feature, "switch", "-q", "-c", "feat/x")
	onMain := newBranchGuardRepo(t, "main")
	// cwd is on a feature branch, but the edited file lives in a repo on main.
	assertBranchGuardDenies(t, runBranchGuard(t, branchGuardInput(feature, filepath.Join(onMain, "a.go"))))
}

func TestBranchGuardDeniesNewFileInMissingDirectory(t *testing.T) {
	isolateBranchGuardGit(t)
	repo := newBranchGuardRepo(t, "main")
	assertBranchGuardDenies(t, runBranchGuard(t, branchGuardInput(repo, filepath.Join(repo, "new", "dir", "a.go"))))
}

func TestBranchGuardAllowsMalformedPayload(t *testing.T) {
	isolateBranchGuardGit(t)
	assertBranchGuardAllows(t, runBranchGuard(t, "{not json"))
}

func TestBranchGuardRejectsUnsupportedAgent(t *testing.T) {
	var stdout bytes.Buffer
	err := RunHook([]string{"branch-guard", "--agent", "codex"}, strings.NewReader("{}"), &stdout)
	if err == nil || !strings.Contains(err.Error(), "claude-code") {
		t.Fatalf("RunHook() error = %v, want a claude-code-only refusal", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunHookRejectsUnknownHook(t *testing.T) {
	var stdout bytes.Buffer
	if err := RunHook([]string{"nope"}, strings.NewReader(""), &stdout); err == nil {
		t.Fatal("RunHook() error = nil, want unknown hook error")
	}
}

// TestBranchGuardDeniesLargeWrite pins that a Write bigger than any payload
// cap is still judged: an undecodable payload would be allowed.
func TestBranchGuardDeniesLargeWrite(t *testing.T) {
	isolateBranchGuardGit(t)
	repo := newBranchGuardRepo(t, "main")
	payload, _ := json.Marshal(map[string]any{
		"cwd":             repo,
		"hook_event_name": "PreToolUse",
		"tool_name":       "Write",
		"tool_input":      map[string]any{"file_path": filepath.Join(repo, "big.txt"), "content": strings.Repeat("x", 5<<20)},
	})
	assertBranchGuardDenies(t, runBranchGuard(t, string(payload)))
}

// TestBranchGuardIgnoresInheritedGitDir pins that GIT_DIR from the hook's
// environment cannot redirect the decision to another repository.
func TestBranchGuardIgnoresInheritedGitDir(t *testing.T) {
	isolateBranchGuardGit(t)
	feature := newBranchGuardRepo(t, "main")
	branchGuardGit(t, feature, "switch", "-q", "-c", "feat/x")
	onMain := newBranchGuardRepo(t, "main")
	t.Setenv("GIT_DIR", filepath.Join(feature, ".git"))
	assertBranchGuardDenies(t, runBranchGuard(t, branchGuardInput(onMain, filepath.Join(onMain, "a.go"))))
}
