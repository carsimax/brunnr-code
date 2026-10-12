package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
)

func TestGlobalInstallKeepsAmbientProjectUnchanged(t *testing.T) {
	testGlobalArtifactRoots(t, false)
}

func TestGlobalSyncKeepsAmbientProjectUnchanged(t *testing.T) {
	testGlobalArtifactRoots(t, true)
}

// testGlobalArtifactRoots proves a global-scope install or sync writes only
// under the home directory and never into the process working directory.
func testGlobalArtifactRoots(t *testing.T, sync bool) {
	t.Helper()
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(project)
	mustWriteFile(t, filepath.Join(project, "go.mod"), []byte("module untouched\n"))

	selection := model.Selection{
		Agents:     []model.AgentID{model.AgentClaudeCode, model.AgentCodex},
		Components: []model.ComponentID{model.ComponentPersona, model.ComponentSkills, model.ComponentEngram, model.ComponentContext7},
		Skills:     []model.SkillID{model.SkillGoTesting},
		Persona:    model.PersonaGentleman, StrictTDD: true,
	}
	previous := cmdLookPath
	t.Cleanup(func() { cmdLookPath = previous })
	cmdLookPath = func(name string) (string, error) { return filepath.Join(home, "bin", name), nil }

	if sync {
		rt, err := newSyncRuntimeWithScope(home, selection, ScopeGlobal)
		if err != nil {
			t.Fatal(err)
		}
		for _, step := range rt.stagePlan().Apply {
			if err := step.Run(); err != nil {
				t.Fatalf("%s: %v", step.ID(), err)
			}
		}
	} else {
		runInstallInjectionSteps(t, newTestInstallRuntime(t, home, selection))
	}

	entries, err := os.ReadDir(project)
	if err != nil || len(entries) != 1 || entries[0].Name() != "go.mod" {
		t.Errorf("ambient root changed: %s: %v (%v)", project, entries, err)
	}
	if got := readTextFile(t, filepath.Join(project, "go.mod")); got != "module untouched\n" {
		t.Errorf("ambient content changed: %q", got)
	}
	for _, path := range []string{".claude/CLAUDE.md", ".claude/skills/go-testing/SKILL.md", ".codex/AGENTS.md", ".codex/skills/go-testing/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(home, path)); err != nil {
			t.Errorf("missing global artifact %s: %v", path, err)
		}
	}
}
