package screens

import (
	"errors"
	"strings"
	"testing"

	componentuninstall "github.com/gentleman-programming/gentle-ai/v4/internal/components/uninstall"
	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
)

func TestRenderUninstallResultIncludesManualCleanup(t *testing.T) {
	out := RenderUninstallResult(componentuninstall.Result{
		RemovedDirectories: []string{"/tmp/skills"},
		ManualActions: []string{
			"Remove manually if no longer needed: /tmp/skills (directory still contains non-managed files)",
		},
	}, nil, "", model.EngramUninstallScopeGlobal, false, nil, nil)

	if !strings.Contains(out, "Manual cleanup required") {
		t.Fatalf("RenderUninstallResult() should include manual cleanup heading; got:\n%s", out)
	}
	if !strings.Contains(out, "/tmp/skills") {
		t.Fatalf("RenderUninstallResult() should include manual cleanup item; got:\n%s", out)
	}
}

func TestRenderUninstallResultFailureShowsManualActions(t *testing.T) {
	const action = "Inspect /home/u/.claude/CLAUDE.md and /home/u/.claude/gentle-ai/orchestrator before rerunning the uninstall"
	for _, actions := range [][]string{{action}, nil} {
		result := componentuninstall.Result{FailedAgents: []model.AgentID{model.AgentClaudeCode}, ManualActions: actions}
		result.Manifest.ID = "backup-test"
		out := RenderUninstallResult(result, errors.New("retire Claude orchestrator modules"), "", "", false, nil, nil)
		for _, want := range []string{"✗ Uninstall failed", "retire Claude orchestrator modules", "Backup created before failure", "backup-test"} {
			if !strings.Contains(out, want) {
				t.Fatalf("RenderUninstallResult() missing %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "Manual cleanup required") != (actions != nil) || strings.Contains(out, action) != (actions != nil) {
			t.Fatalf("RenderUninstallResult() manual actions %v rendered incorrectly:\n%s", actions, out)
		}
	}
}

func TestRenderUninstallConfirmIncludesEngramProjectScopeDetails(t *testing.T) {
	out := RenderUninstallConfirm(
		model.UninstallModePartial,
		[]model.AgentID{model.AgentCodex},
		[]model.ComponentID{model.ComponentEngram},
		model.EngramUninstallScopeProject,
		true,
		0,
		false,
		0,
	)

	if !strings.Contains(out, "Engram cleanup scope") {
		t.Fatalf("RenderUninstallConfirm() should include Engram cleanup scope heading; got:\n%s", out)
	}
	if !strings.Contains(out, "Project-only") {
		t.Fatalf("RenderUninstallConfirm() should include project-only scope label; got:\n%s", out)
	}
	if !strings.Contains(out, ".engram/") {
		t.Fatalf("RenderUninstallConfirm() should mention .engram project data removal; got:\n%s", out)
	}
}

func TestRenderUninstallResultIncludesEngramScopeSummary(t *testing.T) {
	out := RenderUninstallResult(componentuninstall.Result{
		RemovedDirectories: []string{"/tmp/workspace/.engram"},
	}, nil, model.UninstallModePartial, model.EngramUninstallScopeProject, true, nil, nil)

	if !strings.Contains(out, "Engram scope: Project-only") {
		t.Fatalf("RenderUninstallResult() should include Engram project scope summary; got:\n%s", out)
	}
}
