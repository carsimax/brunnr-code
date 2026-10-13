//go:build real_agent_e2e

// This file is gated behind the real_agent_e2e build tag, the same shape
// internal/reviewtransaction uses for bench_fixture: code that needs a real prerequisite the ordinary `go test ./...` sweep
// never provides does not compile into that sweep at all, rather than
// running and depending on ambient machine state, or skipping in a way that
// reads as passing.
//
// Claude Code's routing-guidance case needs brunnr-code's real
// Detect() to find a real installed binary on PATH: install now refuses
// instead of installing a missing runtime (agentInstallStep in
// internal/cli/run.go), so without the real binary these cases fail with a
// refusal, not a skip. `go test ./...` at the repo root (the Unit Tests CI
// job) never installs agent runtimes, so these cases run only where the
// runtimes are installed.
//
// The organic-runtime-e2e job in .github/workflows/ci.yml installs Claude
// Code before running `go test -tags real_agent_e2e ./e2e/organicruntime`,
// and its "Verify real-agent detection E2E is registered" step fails loudly
// if this test ever stops compiling into that job.
package organicruntime_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestOrganicConfiguredAgentReceivesRoutingGuidanceRealAgents proves the
// markdown-section delivery strategy for Claude Code, which needs a real
// installed agent binary. See this file's package comment for why it is
// gated.
func TestOrganicConfiguredAgentReceivesRoutingGuidanceRealAgents(t *testing.T) {
	t.Parallel()
	agents := []struct {
		name    string
		agentID string
		path    string
	}{
		{name: "markdown section", agentID: "claude-code", path: ".claude/CLAUDE.md"},
	}
	for _, agent := range agents {
		t.Run(agent.name, func(t *testing.T) {
			t.Parallel()
			workspace := t.TempDir()
			home := t.TempDir()
			if _, err := organicGitOutput(context.Background(), workspace, "init", "--quiet", "--initial-branch=main", "."); err != nil {
				t.Fatal(err)
			}
			output, stderr, err := runOrganicCommand(
				t, organicBinary, workspace, organicEnvironment(home),
				"install", "--agent", agent.agentID, "--scope", "workspace",
			)
			if err != nil {
				t.Fatalf("install %s: %v\nstdout:\n%s\nstderr:\n%s", agent.agentID, err, output, stderr)
			}
			rendered, readErr := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(agent.path)))
			if readErr != nil {
				t.Fatalf("configured agent %s received no routing guidance at %s: %v", agent.agentID, agent.path, readErr)
			}
			required := append(append([]string{}, organicRoutingGuidanceRequiredFragments...), organicRoutingGuidanceRDDFragments...)
			for _, fragment := range required {
				if !bytes.Contains(rendered, []byte(fragment)) {
					t.Fatalf("routing guidance for %s omits %q:\n%s", agent.agentID, fragment, rendered)
				}
			}
		})
	}
}
