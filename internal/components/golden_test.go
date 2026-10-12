package components_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/agents/claude"
	codexagent "github.com/carsimax/brunnr-code/internal/agents/codex"
	"github.com/carsimax/brunnr-code/internal/components/engram"
	"github.com/carsimax/brunnr-code/internal/components/persona"
	"github.com/carsimax/brunnr-code/internal/components/skills"
	"github.com/carsimax/brunnr-code/internal/model"
)

var update = flag.Bool("update", false, "update golden files")

func claudeAdapter() agents.Adapter { return claude.NewAdapter() }
func codexAdapter() agents.Adapter  { return codexagent.NewAdapter() }

// ---------------------------------------------------------------------------
// Existing golden tests (presets)
// ---------------------------------------------------------------------------

func TestGoldenConfigs(t *testing.T) {
	type presetMapping struct {
		Preset string   `json:"preset"`
		Skills []string `json:"skills"`
	}

	presets := []presetMapping{
		{Preset: "full", Skills: toStringSlice(skills.SkillsForPreset("full"))},
		{Preset: "ecosystem-only", Skills: toStringSlice(skills.SkillsForPreset("ecosystem-only"))},
		{Preset: "minimal", Skills: toStringSlice(skills.SkillsForPreset("minimal"))},
	}
	presetsJSON, err := json.MarshalIndent(presets, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent() error = %v", err)
	}
	presetsJSON = append(presetsJSON, '\n')

	tests := []struct {
		name    string
		path    string
		content []byte
	}{
		{name: "skills presets", path: "skills-presets.json", content: presetsJSON},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertGolden(t, tc.path, tc.content)
		})
	}
}

// ---------------------------------------------------------------------------
// Persona Injector golden tests
// ---------------------------------------------------------------------------

func TestGoldenPersona_Claude(t *testing.T) {
	home := t.TempDir()

	result, err := persona.Inject(home, claudeAdapter())
	if err != nil {
		t.Fatalf("persona.Inject(claude) error = %v", err)
	}
	if !result.Changed {
		t.Fatalf("persona.Inject(claude) changed = false")
	}

	claudeMD := readTestFile(t, filepath.Join(home, ".claude", "CLAUDE.md"))
	assertGolden(t, "persona-claude.golden", claudeMD)

	// Locks the managed Principal Engineer output style written next to the
	// residual CLAUDE.md persona block.
	outputStyle := readTestFile(t, filepath.Join(home, ".claude", "output-styles", "principal-engineer.md"))
	assertGolden(t, "persona-claude-principal-engineer-outputstyle.golden", outputStyle)
}

// ---------------------------------------------------------------------------
// Engram Injector golden tests
// ---------------------------------------------------------------------------

func TestGoldenEngram_Claude(t *testing.T) {
	home := t.TempDir()

	engram.SetLookPathForTest(t, "/usr/local/bin/engram", "")

	// Pin the engram binary version above the Decision 1 floor (v1.4.0) so
	// this golden reflects the SLIM CLAUDE.md section — the MCP `instructions`
	// channel + SessionStart hook are the verified redundant channels for
	// Claude Code (design.md Decision 1). "1.18.0" matches the live evidence
	// cited in design.md.
	result, err := engram.InjectWithOptions(home, claudeAdapter(), engram.InjectOptions{Version: "1.18.0"})
	if err != nil {
		t.Fatalf("engram.InjectWithOptions(claude) error = %v", err)
	}
	if !result.Changed {
		t.Fatalf("engram.InjectWithOptions(claude) changed = false")
	}

	// Claude user MCP registry, the supported user-scope location.
	mcpJSON := readTestFile(t, claude.UserConfigPath(home))
	assertGolden(t, "engram-claude-mcp.golden", mcpJSON)

	// CLAUDE.md with engram-protocol section (slim, per Decision 1).
	claudeMD := readTestFile(t, filepath.Join(home, ".claude", "CLAUDE.md"))
	assertGolden(t, "engram-claude-claudemd.golden", claudeMD)
}

// TestGoldenEngram_Codex captures the rendered Codex model_instructions_file
// and experimental_compact_prompt_file output after the canonical-asset
// consolidation (design.md Decision 3). These goldens catch the content
// growth from consolidating onto the canonical `full` text: the old
// codex/engram-instructions.md (6 "WHEN TO SAVE" bullets, no self-check line)
// is replaced by the fuller canonical text (12 "PROACTIVE SAVE TRIGGERS"
// bullets + a self-check line) concatenated with the unchanged PASSIVE
// CAPTURE section.
func TestGoldenEngram_Codex(t *testing.T) {
	home := t.TempDir()
	restore := codexagent.SetRuntimeVersionCommandForTest("codex-cli 0.144.0", nil)
	t.Cleanup(restore)

	engram.SetLookPathForTest(t, "/usr/local/bin/engram", "")

	result, err := engram.Inject(home, codexAdapter())
	if err != nil {
		t.Fatalf("engram.Inject(codex) error = %v", err)
	}
	if !result.Changed {
		t.Fatalf("engram.Inject(codex) changed = false")
	}

	instructions := readTestFile(t, filepath.Join(home, ".codex", "engram-instructions.md"))
	assertGolden(t, "engram-codex-instructions.golden", instructions)

	compactPrompt := readTestFile(t, filepath.Join(home, ".codex", "engram-compact-prompt.md"))
	assertGolden(t, "engram-codex-compact-prompt.golden", compactPrompt)
}

// ---------------------------------------------------------------------------
// Skills Injector golden tests
// ---------------------------------------------------------------------------

func TestGoldenSkills_Claude(t *testing.T) {
	home := t.TempDir()

	skillIDs := []model.SkillID{model.SkillGoTesting, model.SkillCreator}
	result, err := skills.Inject(home, claudeAdapter(), skillIDs)
	if err != nil {
		t.Fatalf("skills.Inject(claude) error = %v", err)
	}
	if !result.Changed {
		t.Fatalf("skills.Inject(claude) changed = false")
	}

	goTestingSkill := readTestFile(t, filepath.Join(home, ".claude", "skills", "go-testing", "SKILL.md"))
	assertGolden(t, "skills-claude-go-testing.golden", goTestingSkill)

	skillCreator := readTestFile(t, filepath.Join(home, ".claude", "skills", "skill-creator", "SKILL.md"))
	assertGolden(t, "skills-claude-skill-creator.golden", skillCreator)
}

// ---------------------------------------------------------------------------
// Combined injection golden test (multiple components writing to same CLAUDE.md)
// ---------------------------------------------------------------------------

func TestGoldenCombined_Claude(t *testing.T) {
	home := t.TempDir()

	engram.SetLookPathForTest(t, "/usr/local/bin/engram", "")

	// Combine retained persona and Engram sections in CLAUDE.md.
	if _, err := persona.Inject(home, claudeAdapter()); err != nil {
		t.Fatalf("persona.Inject error = %v", err)
	}
	// Pin the engram version above the Decision 1 floor so the combined
	// CLAUDE.md reflects the slim engram-protocol section, matching
	// TestGoldenEngram_Claude above.
	if _, err := engram.InjectWithOptions(home, claudeAdapter(), engram.InjectOptions{Version: "1.18.0"}); err != nil {
		t.Fatalf("engram.InjectWithOptions error = %v", err)
	}

	claudeMD := readTestFile(t, filepath.Join(home, ".claude", "CLAUDE.md"))
	assertGolden(t, "combined-claude-claudemd.golden", claudeMD)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func goldenDir(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "testdata", "golden")
}

func toStringSlice(ids []model.SkillID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = string(id)
	}
	return out
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	return data
}

func assertGolden(t *testing.T, name string, actual []byte) {
	t.Helper()
	goldenPath := filepath.Join(goldenDir(t), name)

	if *update {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("MkdirAll for golden dir: %v", err)
		}
		if err := os.WriteFile(goldenPath, actual, 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", goldenPath, err)
		}
		t.Logf("updated golden file: %s", goldenPath)
		return
	}

	expected, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v\n\nRun with -update to generate golden files:\n  go test ./internal/components/ -run %s -update", goldenPath, err, t.Name())
	}

	if string(actual) != string(expected) {
		// Show first difference for easier debugging.
		diffIdx := firstDiffIndex(string(expected), string(actual))
		context := 80
		start := diffIdx - context
		if start < 0 {
			start = 0
		}

		t.Fatalf("golden mismatch for %s (first diff at byte %d)\n\nexpected[%d:%d]:\n%s\n\nactual[%d:%d]:\n%s\n\nRun with -update to regenerate:\n  go test ./internal/components/ -run %s -update",
			name, diffIdx,
			start, min(diffIdx+context, len(string(expected))), string(expected)[start:min(diffIdx+context, len(string(expected)))],
			start, min(diffIdx+context, len(string(actual))), string(actual)[start:min(diffIdx+context, len(string(actual)))],
			t.Name(),
		)
	}
}

func firstDiffIndex(a, b string) int {
	maxLen := len(a)
	if len(b) < maxLen {
		maxLen = len(b)
	}
	for i := 0; i < maxLen; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return maxLen
	}
	return -1
}
