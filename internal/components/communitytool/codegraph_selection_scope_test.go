package communitytool

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
	"github.com/gentleman-programming/gentle-ai/v4/internal/state"
)

// Regression coverage: the CodeGraph injectors discovered agents purely from
// the filesystem, so Codex and Claude Code merely existing on the machine was
// enough to have their prompt files rewritten on every sync.

// selectOnlyCodexWithClaudePresent builds the reported environment: two
// agents installed on disk, only Codex selected in Gentle AI.
func selectOnlyCodexWithClaudePresent(t *testing.T) (home, codexDir, claudePrompt string) {
	t.Helper()
	home = t.TempDir()

	codexDir = filepath.Join(home, ".codex")
	claudePrompt = filepath.Join(home, ".claude", "CLAUDE.md")

	mustWrite(t, filepath.Join(codexDir, "AGENTS.md"), "user codex instructions\n")
	mustWrite(t, claudePrompt, "user claude instructions\n")
	if err := state.Write(home, state.InstallState{
		InstalledAgents: []string{string(model.AgentCodex)},
	}); err != nil {
		t.Fatalf("state.Write: %v", err)
	}
	return home, codexDir, claudePrompt
}

func TestInjectCodeGraphGuidanceSkipsUnselectedAgents(t *testing.T) {
	home, codexDir, claudePrompt := selectOnlyCodexWithClaudePresent(t)

	result, err := InjectCodeGraphGuidance(home)
	if err != nil {
		t.Fatalf("InjectCodeGraphGuidance() error = %v", err)
	}
	for _, path := range result.Files {
		if !strings.HasPrefix(path, codexDir+string(filepath.Separator)) {
			t.Errorf("InjectCodeGraphGuidance() touched unselected agent file %q", path)
		}
	}

	content, err := os.ReadFile(claudePrompt)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", claudePrompt, err)
	}
	if strings.Contains(string(content), "## CodeGraph") {
		t.Errorf("guidance was injected into unselected agent %q:\n%s", claudePrompt, content)
	}
}

// TestCodeGraphPathSetsSplitWriteScopeFromBackupScope pins the asymmetry: we
// WRITE only to selected agents but BACK UP every detected one, because the
// third-party installer can reach files we never meant to write and rollback
// must still restore them.
func TestCodeGraphPathSetsSplitWriteScopeFromBackupScope(t *testing.T) {
	home, _, claudePrompt := selectOnlyCodexWithClaudePresent(t)

	if slices.Contains(CodeGraphGuidancePaths(home), claudePrompt) {
		t.Errorf("CodeGraphGuidancePaths() must not write unselected %q", claudePrompt)
	}

	managed := CodeGraphManagedPaths(home)
	if !slices.Contains(managed, claudePrompt) {
		t.Errorf("CodeGraphManagedPaths() = %v, must still back up detected %q", managed, claudePrompt)
	}
}
