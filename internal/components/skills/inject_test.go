package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/agents"
	"github.com/gentleman-programming/gentle-ai/v4/internal/agents/claude"
	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
	"github.com/gentleman-programming/gentle-ai/v4/internal/skillregistry"
	"github.com/gentleman-programming/gentle-ai/v4/internal/system"
)

func claudeAdapter() agents.Adapter { return claude.NewAdapter() }

func TestInjectWritesSkillFilesForClaude(t *testing.T) {
	home := t.TempDir()

	result, err := Inject(home, claudeAdapter(), []model.SkillID{model.SkillCreator, model.SkillGoTesting})
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	if !result.Changed {
		t.Fatalf("Inject() changed = false")
	}

	for _, path := range []string{
		filepath.Join(home, ".claude", "skills", "skill-creator", "SKILL.md"),
		filepath.Join(home, ".claude", "skills", "go-testing", "SKILL.md"),
		filepath.Join(home, ".claude", "skills", "go-testing", "references", "examples.md"),
	} {
		if !containsFile(result.Files, path) {
			t.Fatalf("Inject() files = %v, missing %q", result.Files, path)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected skill file %q: %v", path, err)
		}
	}
}

func TestInjectSkipsSddSkills(t *testing.T) {
	home := t.TempDir()

	// Retired SDD IDs must not install even when explicitly requested.
	result, err := Inject(home, claudeAdapter(), []model.SkillID{
		model.SkillSDDInit,
		model.SkillSDDApply,
		model.SkillCreator,
	})
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	// Only the non-SDD skill (skill-creator) should be written, including its local references.
	if len(skillFiles(result.Files)) != 2 {
		t.Fatalf("Inject() files len = %d, want 2 (skill-creator plus local reference)", len(skillFiles(result.Files)))
	}

	// No retired skill files may be created.
	for _, id := range []model.SkillID{model.SkillSDDInit, model.SkillSDDApply} {
		path := filepath.Join(home, ".claude", "skills", string(id), "SKILL.md")
		if _, statErr := os.Stat(path); statErr == nil {
			t.Fatalf("skills component must not write retired skill %q", id)
		}
	}
}

// noSkillsAdapter is a mock adapter that does not support skills.
type noSkillsAdapter struct{}

func (a noSkillsAdapter) Agent() model.AgentID    { return "no-skills" }
func (a noSkillsAdapter) Tier() model.SupportTier { return model.TierFull }
func (a noSkillsAdapter) Detect(_ context.Context, _ string) (bool, string, string, bool, error) {
	return false, "", "", false, nil
}
func (a noSkillsAdapter) InstallCommand(_ system.PlatformProfile) ([][]string, error) {
	return nil, nil
}
func (a noSkillsAdapter) GlobalConfigDir(_ string) string  { return "" }
func (a noSkillsAdapter) SystemPromptDir(_ string) string  { return "" }
func (a noSkillsAdapter) SystemPromptFile(_ string) string { return "" }
func (a noSkillsAdapter) SkillsDir(_ string) string        { return "" }
func (a noSkillsAdapter) SettingsPath(_ string) string     { return "" }
func (a noSkillsAdapter) SystemPromptStrategy() model.SystemPromptStrategy {
	return model.StrategyFileReplace
}
func (a noSkillsAdapter) MCPStrategy() model.MCPStrategy          { return model.StrategyMergeIntoSettings }
func (a noSkillsAdapter) MCPConfigPath(_ string, _ string) string { return "" }
func (a noSkillsAdapter) SupportsOutputStyles() bool              { return false }
func (a noSkillsAdapter) OutputStyleDir(_ string) string          { return "" }
func (a noSkillsAdapter) SupportsSlashCommands() bool             { return false }
func (a noSkillsAdapter) CommandsDir(_ string) string             { return "" }
func (a noSkillsAdapter) SupportsSubAgents() bool                 { return false }
func (a noSkillsAdapter) SubAgentsDir(_ string) string            { return "" }
func (a noSkillsAdapter) EmbeddedSubAgentsDir() string            { return "" }
func (a noSkillsAdapter) SupportsSkills() bool                    { return false }
func (a noSkillsAdapter) SupportsSystemPrompt() bool              { return false }
func (a noSkillsAdapter) SupportsMCP() bool                       { return false }

func TestInjectSkipsUnsupportedAgent(t *testing.T) {
	home := t.TempDir()

	// Mock adapter that does not support skills — Inject should skip gracefully.
	result, injectErr := Inject(home, noSkillsAdapter{}, []model.SkillID{model.SkillCreator})
	if injectErr != nil {
		t.Fatalf("Inject() unexpected error = %v", injectErr)
	}

	// All skills should be skipped.
	if len(result.Skipped) != 1 {
		t.Fatalf("Inject() skipped = %v, want 1 skill", result.Skipped)
	}
	if result.Changed {
		t.Fatal("Inject() changed = true, want false for unsupported agent")
	}
}

func TestInjectUsesRealEmbeddedContent(t *testing.T) {
	home := t.TempDir()

	_, err := Inject(home, claudeAdapter(), []model.SkillID{model.SkillCreator})
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	path := filepath.Join(home, ".claude", "skills", "skill-creator", "SKILL.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	// Real embedded content should be substantial (not a one-line stub).
	if len(content) < 100 {
		t.Fatalf("skill file content looks like a stub (len=%d)", len(content))
	}
}

func TestInjectRequiredBundledSkillsForEverySkillsCapableDefaultAdapter(t *testing.T) {
	registry, err := agents.NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry() error = %v", err)
	}

	required := requiredBundledSkillIDs()

	for _, agentID := range registry.SupportedAgents() {
		adapter, ok := registry.Get(agentID)
		if !ok {
			t.Fatalf("registry missing adapter %q", agentID)
		}
		if !adapter.SupportsSkills() {
			continue
		}

		t.Run(string(agentID), func(t *testing.T) {
			home := t.TempDir()
			result, injectErr := InjectWithCapability(home, adapter, required, "capable")
			if injectErr != nil {
				t.Fatalf("InjectWithCapability() error = %v", injectErr)
			}
			if !result.Changed {
				t.Fatalf("InjectWithCapability() changed = false")
			}
			if len(result.Skipped) != 0 {
				t.Fatalf("InjectWithCapability() skipped = %v, want none", result.Skipped)
			}

			skillsDir := adapter.SkillsDir(home)
			if skillsDir == "" {
				t.Fatalf("adapter %q supports skills but returned empty SkillsDir", agentID)
			}

			entries := skillregistry.List(t.TempDir(), home)
			for _, id := range required {
				path := filepath.Join(skillsDir, string(id), "SKILL.md")
				assertNonEmptyFile(t, path)
				if id == model.SkillSkillRegistry {
					continue // Registry plumbing intentionally excludes itself.
				}
				found := false
				for _, entry := range entries {
					if entry.Name == string(id) && entry.Path == path {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("skill registry did not discover %q at %q", id, path)
				}
			}
		})
	}
}

func TestSkillPathForAgent(t *testing.T) {
	path := SkillPathForAgent("/home/test", claudeAdapter(), model.SkillCreator)
	want := filepath.Join("/home/test", ".claude", "skills", "skill-creator", "SKILL.md")
	if path != want {
		t.Fatalf("SkillPathForAgent() = %q, want %q", path, want)
	}
}

func assertNonEmptyFile(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected file %q: %v", path, err)
	}
	if info.Size() == 0 {
		t.Fatalf("expected file %q to be non-empty", path)
	}
}

func containsFile(files []string, want string) bool {
	for _, file := range files {
		if file == want {
			return true
		}
	}
	return false
}

func requiredBundledSkillIDs() []model.SkillID {
	return []model.SkillID{
		model.SkillCreator,
		model.SkillSkillRegistry,
		model.SkillCognitiveDoc,
		model.SkillCommentWriter,
		model.SkillJudgmentDay,
		model.SkillWorkUnitCommits,
		model.SkillGoTesting,
		model.SkillImprover,
		model.SkillRDDDefectWorkflow,
		model.SkillSystemicIssueTriage,
		model.SkillGentleAIBench,
	}
}
