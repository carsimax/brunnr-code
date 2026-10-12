package persona

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/agents/claude"
	"github.com/carsimax/brunnr-code/internal/agents/codex"
	"github.com/carsimax/brunnr-code/internal/assets"
	"github.com/carsimax/brunnr-code/internal/model"
)

// genericVoiceSentinel appears only in the full generic persona voice, never in
// the Claude residual block, so it tells the two assets apart.
const genericVoiceSentinel = "generation-verification loop"

func claudeAdapter() agents.Adapter { return claude.NewAdapter() }
func codexAdapter() agents.Adapter  { return codex.NewAdapter() }

var claudeOutputStyleLanguageGuardrails = []string{
	"Determine the reply language from the latest actual user request",
	"For mixed-language prompts, use the dominant language of the user's direct request.",
	`phrases like "the Spanish part" do not switch the reply language by themselves.`,
	"If the selected reply language is English, every part of the direct reply must be English: greetings, interjections, acknowledgements, transition phrases, and the first sentence.",
	"Do not use Hola, dale, listo, Spanish punctuation, or other Spanish fragments.",
	"Prompts starting with or dominated by hi, hello, hey, or similar English greetings are English prompts unless the user explicitly asks for another language.",
	// Decision 4 union lines — reconciled from the drifted persona copy, now
	// canonical only in the output style.
	"Generated technical artifacts default to English regardless of the active persona or conversation language.",
	"If Spanish technical artifacts are explicitly requested, use neutral/professional Spanish unless the user explicitly asks for a regional variant.",
	"Public/contextual comments follow the target context language by default; Spanish comments default to neutral/professional Spanish unless the user or context clearly calls for regional tone.",
	"Do not switch languages unless the user does, asks you to, or you are quoting/translating content.",
}

func assertLanguageGuardrails(t *testing.T, text string, required []string, banned []string) {
	t.Helper()

	for _, needle := range required {
		if !strings.Contains(text, needle) {
			t.Fatalf("missing language guardrail %q", needle)
		}
	}

	for _, needle := range banned {
		if strings.Contains(text, needle) {
			t.Fatalf("contains drift-prone language instruction %q", needle)
		}
	}
}

func TestInjectClaudeWritesResidualPersonaWithoutRegionalLanguage(t *testing.T) {
	home := t.TempDir()

	result, err := Inject(home, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	if !result.Changed {
		t.Fatalf("Inject() changed = false")
	}

	path := filepath.Join(home, ".claude", "CLAUDE.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	text := string(content)
	// Claude gets its voice from the Principal Engineer output style, so the
	// persona section is the residual, not the full generic voice block.
	if strings.Contains(text, genericVoiceSentinel) {
		t.Fatalf("Claude persona section should be a residual — %q must not appear", genericVoiceSentinel)
	}
	if !strings.Contains(text, "## Rules") {
		t.Fatal("Claude persona residual section missing '## Rules'")
	}
	if !strings.Contains(text, "Principal Engineer") {
		t.Fatal("Claude persona residual should point to the Principal Engineer output style")
	}
	// Should NOT have regional language.
	if strings.Contains(text, "Rioplatense") {
		t.Fatal("Claude persona should not contain Rioplatense language")
	}
}

func TestInjectClaudeWritesPrincipalEngineerOutputStyleAndSettings(t *testing.T) {
	home := t.TempDir()
	settingsDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(filepath.Join(settingsDir, "output-styles"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	existingSettings := `{"permissions":{"allow":["Read"]},"outputStyle":"Other"}`
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(existingSettings), 0o644); err != nil {
		t.Fatalf("WriteFile(settings) error = %v", err)
	}

	result, err := Inject(home, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	for _, suffix := range []string{"CLAUDE.md", "principal-engineer.md", "settings.json"} {
		found := false
		for _, file := range result.Files {
			if strings.HasSuffix(file, suffix) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("persona result missing %q in files %v", suffix, result.Files)
		}
	}

	stylePath := filepath.Join(home, ".claude", "output-styles", "principal-engineer.md")
	styleContent, err := os.ReadFile(stylePath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", stylePath, err)
	}
	styleText := string(styleContent)
	for _, want := range []string{"name: Principal Engineer", "Principal Engineer Output Style", "minimum useful response", "Generated technical artifacts default to English"} {
		if !strings.Contains(styleText, want) {
			t.Fatalf("principal engineer output style missing %q; got:\n%s", want, styleText)
		}
	}
	assertLanguageGuardrails(t, styleText, claudeOutputStyleLanguageGuardrails, nil)
	if strings.Contains(styleText, "Rioplatense") || strings.Contains(styleText, "voseo") {
		t.Fatalf("principal engineer output style contains regional wording:\n%s", styleText)
	}

	settingsContent, err := os.ReadFile(filepath.Join(settingsDir, "settings.json"))
	if err != nil {
		t.Fatalf("ReadFile(settings) error = %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(settingsContent, &settings); err != nil {
		t.Fatalf("Unmarshal settings error = %v", err)
	}
	if got, want := settings["outputStyle"], "Principal Engineer"; got != want {
		t.Fatalf("settings outputStyle = %q, want %q", got, want)
	}
	if _, ok := settings["permissions"]; !ok {
		t.Fatal("settings lost existing permissions key")
	}

	second, err := Inject(home, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject() second error = %v", err)
	}
	if second.Changed {
		t.Fatalf("second Claude persona inject changed = true, want idempotent false")
	}
}

func TestInjectClaudeIsIdempotent(t *testing.T) {
	home := t.TempDir()

	first, err := Inject(home, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject() first error = %v", err)
	}
	if !first.Changed {
		t.Fatalf("Inject() first changed = false")
	}

	second, err := Inject(home, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject() second error = %v", err)
	}
	if second.Changed {
		t.Fatalf("Inject() second changed = true")
	}
}

func TestInjectClaudeHealDoesNotTouchNonPersonaContent(t *testing.T) {
	home := t.TempDir()

	// CLAUDE.md has user content that does NOT match persona fingerprints.
	claudeMD := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(claudeMD), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	userContent := "# My custom config\n\nI like turtles.\n"
	if err := os.WriteFile(claudeMD, []byte(userContent), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	result, err := Inject(home, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	if !result.Changed {
		t.Fatal("Inject() should write persona section")
	}

	content, err := os.ReadFile(claudeMD)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(content)

	// User content must be preserved.
	if !strings.Contains(text, "I like turtles.") {
		t.Fatal("user content was erased — heal was too aggressive")
	}
	// Persona section must be appended.
	if !strings.Contains(text, "<!-- brunnr-code:persona -->") {
		t.Fatal("persona section not appended")
	}
}

func TestInjectClaude_SelectsManagedOutputStyleAndPreservesOtherSettings(t *testing.T) {
	home := t.TempDir()

	// Pre-create settings.json with a user-defined outputStyle that is NOT "Custom".
	settingsDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	userSettings := `{"outputStyle": "MyCustom", "syntaxHighlightingDisabled": true}`
	settingsPath := filepath.Join(settingsDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(userSettings), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Inject(home, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	settingsRaw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile(settings.json) error = %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(settingsRaw, &settings); err != nil {
		t.Fatalf("Unmarshal settings.json error = %v", err)
	}

	if got, want := settings["outputStyle"], "Principal Engineer"; got != want {
		t.Fatalf("outputStyle = %v, want %q", got, want)
	}
	// Other user keys must also survive.
	if settings["syntaxHighlightingDisabled"] != true {
		t.Fatal("syntaxHighlightingDisabled was lost")
	}
}

func TestInjectClaude_MalformedJSON_DoesNotPanic(t *testing.T) {
	home := t.TempDir()

	settingsDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	malformed := `{ "outputStyle": "Custom", invalid`
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(malformed), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Inject(home, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject() with malformed settings.json must not error, got: %v", err)
	}
}

// TestPersonaContentResidualDispatchAllAgents is a table-driven regression test
// covering every supported agent: Claude Code receives the residual (tone-free)
// persona asset when residual=true; all other agents receive the full persona
// section unaffected by the residual flag. generic/persona.md
// remains the byte-identical fallback for every non-Claude agent.
func TestPersonaContentResidualDispatchAllAgents(t *testing.T) {
	allAgents := []model.AgentID{
		model.AgentClaudeCode,
		model.AgentCodex,
	}

	isResidualCapable := func(agent model.AgentID) bool {
		return agent == model.AgentClaudeCode
	}

	genericPersona := assets.MustRead("generic/persona.md")

	for _, agent := range allAgents {
		t.Run(string(agent), func(t *testing.T) {
			residual := isResidualCapable(agent)
			content := managedPersonaContent(agent, residual)
			if content == "" {
				t.Fatalf("managedPersonaContent(%q, residual=%v) returned empty string", agent, residual)
			}
			switch {
			case residual:
				if strings.Contains(content, genericVoiceSentinel) {
					t.Fatalf("managedPersonaContent(%q, residual=true) still contains voice content %q", agent, genericVoiceSentinel)
				}
				if !strings.Contains(content, "## Rules") {
					t.Fatalf("managedPersonaContent(%q, residual=true) missing residual '## Rules'", agent)
				}
			default:
				if content != genericPersona {
					t.Fatalf("managedPersonaContent(%q, residual=false) should be byte-identical to generic/persona.md", agent)
				}
			}
		})
	}
}

// TestResidualChannelAllAgents is a direct table test over
// residualChannel(adapter) using real adapter instances for all 16
// model.AgentID constants (JD-021): TestPersonaContentResidualDispatchAllAgents
// hand-derives the residual predicate via isResidualCapable() instead of
// exercising residualChannel() itself, so a bug in residualChannel would not
// be caught there. This test calls residualChannel() directly.
func TestResidualChannelAllAgents(t *testing.T) {
	allAgentIDs := []model.AgentID{
		model.AgentClaudeCode,
		model.AgentCodex,
	}

	for _, agentID := range allAgentIDs {
		t.Run(string(agentID), func(t *testing.T) {
			adapter, err := agents.NewAdapter(agentID)
			if err != nil {
				t.Fatalf("agents.NewAdapter(%q) error = %v", agentID, err)
			}

			want := agentID == model.AgentClaudeCode
			got := residualChannel(adapter)
			if got != want {
				t.Fatalf("residualChannel(%q) = %v, want %v", agentID, got, want)
			}
		})
	}
}

func TestMergeJSONFileToleratingMalformed(t *testing.T) {
	home := t.TempDir()

	t.Run("merges valid json", func(t *testing.T) {
		path := filepath.Join(home, "valid.json")
		if err := os.WriteFile(path, []byte(`{"permissions":{"allow":["Read"]}}`), 0o644); err != nil {
			t.Fatalf("WriteFile(valid): %v", err)
		}

		result, err := mergeJSONFileToleratingMalformed(path, []byte(`{"outputStyle":"Principal Engineer"}`))
		if err != nil {
			t.Fatalf("mergeJSONFileToleratingMalformed(valid) error = %v", err)
		}
		if !result.Changed {
			t.Fatal("mergeJSONFileToleratingMalformed(valid) changed = false")
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(valid): %v", err)
		}
		text := string(raw)
		if !strings.Contains(text, `"outputStyle": "Principal Engineer"`) {
			t.Fatalf("merged JSON missing outputStyle; got:\n%s", text)
		}
		if !strings.Contains(text, `"permissions"`) {
			t.Fatalf("merged JSON lost existing permissions; got:\n%s", text)
		}
	})

	t.Run("ignores malformed overlay to avoid data loss", func(t *testing.T) {
		path := filepath.Join(home, "malformed-overlay.json")
		original := `{"outputStyle":"Custom"}`
		if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
			t.Fatalf("WriteFile(malformed overlay): %v", err)
		}

		result, err := mergeJSONFileToleratingMalformed(path, []byte(`{"outputStyle":"Principal Engineer"`))
		if err != nil {
			t.Fatalf("mergeJSONFileToleratingMalformed(malformed overlay) error = %v", err)
		}
		if result.Changed {
			t.Fatal("mergeJSONFileToleratingMalformed(malformed overlay) changed = true")
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(malformed overlay): %v", err)
		}
		if string(raw) != original {
			t.Fatalf("JSON was modified after malformed overlay; got %q, want %q", string(raw), original)
		}
	})

	t.Run("returns non-json read errors", func(t *testing.T) {
		originalReadFile := osReadFile
		t.Cleanup(func() { osReadFile = originalReadFile })
		osReadFile = func(string) ([]byte, error) {
			return nil, fmt.Errorf("permission denied")
		}

		if _, err := mergeJSONFileToleratingMalformed(filepath.Join(home, "denied.json"), []byte(`{}`)); err == nil {
			t.Fatal("mergeJSONFileToleratingMalformed(non-json error) error = nil")
		}
	})
}

// --- Codex persona marker injection (issue #981) ---

func codexAgentsMDPath(home string) string {
	return filepath.Join(home, ".codex", "AGENTS.md")
}

// TestInjectCodexPreservesUserPrefaceContent ensures an existing user-authored
// AGENTS.md is never replaced wholesale: user content stays and the managed
// persona is appended as a marker section. This includes lookalike user
// content that shares persona fingerprints but has no managed persona marker.
func TestInjectCodexPreservesUserPrefaceContent(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(codexAgentsMDPath(home)), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	existing := "# My team rules\n\n- Always answer in Spanish.\n\n## Rules\n\n- Keep PRs small.\n"
	if err := os.WriteFile(codexAgentsMDPath(home), []byte(existing), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := Inject(home, codexAdapter()); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	content, err := os.ReadFile(codexAgentsMDPath(home))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(content)
	if !strings.HasPrefix(text, "# My team rules") {
		t.Fatalf("user preface was removed or reordered:\n%s", text)
	}
	if !strings.Contains(text, "Always answer in Spanish.") || !strings.Contains(text, "Keep PRs small.") {
		t.Fatal("user-authored rules were stripped")
	}
	if strings.Count(text, "<!-- brunnr-code:persona -->") != 1 {
		t.Fatalf("expected exactly 1 persona marker, got %d", strings.Count(text, "<!-- brunnr-code:persona -->"))
	}
}

// TestInjectCodexDoesNotStripLookalikeUserContent pins the conservative
// legacy-stripping rule: pre-marker user content that merely resembles the
// persona (all fingerprints present) must survive when no managed persona
// marker exists.
func TestInjectCodexDoesNotStripLookalikeUserContent(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(codexAgentsMDPath(home)), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	lookalike := "## Personality\n\nSenior Architect in my org.\n\n## Rules\n\n- My own rule.\n"
	if err := os.WriteFile(codexAgentsMDPath(home), []byte(lookalike), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := Inject(home, codexAdapter()); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	content, err := os.ReadFile(codexAgentsMDPath(home))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(content), "My own rule.") {
		t.Fatal("lookalike user content was stripped without a managed persona marker")
	}

	if _, err := Inject(home, codexAdapter()); err != nil {
		t.Fatalf("second Inject() error = %v", err)
	}
	content, err = os.ReadFile(codexAgentsMDPath(home))
	if err != nil {
		t.Fatalf("ReadFile() after second injection error = %v", err)
	}
	if !strings.Contains(string(content), "My own rule.") {
		t.Fatal("lookalike user content was stripped on repeated injection")
	}
}

// TestInjectCodexIsIdempotent ensures repeated persona injections keep a
// single marker section and do not duplicate content.
func TestInjectCodexIsIdempotent(t *testing.T) {
	home := t.TempDir()

	first, err := Inject(home, codexAdapter())
	if err != nil {
		t.Fatalf("first Inject() error = %v", err)
	}
	if !first.Changed {
		t.Fatal("first Inject() changed = false, want true")
	}

	second, err := Inject(home, codexAdapter())
	if err != nil {
		t.Fatalf("second Inject() error = %v", err)
	}
	if second.Changed {
		t.Fatal("second Inject() changed = true, want false (idempotent)")
	}

	content, err := os.ReadFile(codexAgentsMDPath(home))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(content)
	if strings.Count(text, "<!-- brunnr-code:persona -->") != 1 {
		t.Fatalf("expected exactly 1 persona marker after re-injection, got %d", strings.Count(text, "<!-- brunnr-code:persona -->"))
	}
	if strings.Count(text, genericVoiceSentinel) != 1 {
		t.Fatalf("expected persona content exactly once after re-injection, got %d occurrences", strings.Count(text, genericVoiceSentinel))
	}
}

// TestInjectCodexFreshInstallWrapsPersonaInMarkers ensures the managed
// persona gets the same marker-bound treatment on Codex.
func TestInjectCodexFreshInstallWrapsPersonaInMarkers(t *testing.T) {
	home := t.TempDir()

	if _, err := Inject(home, codexAdapter()); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	content, err := os.ReadFile(codexAgentsMDPath(home))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(content)
	if strings.Count(text, "<!-- brunnr-code:persona -->") != 1 {
		t.Fatalf("expected exactly 1 persona marker, got %d", strings.Count(text, "<!-- brunnr-code:persona -->"))
	}
	if !strings.Contains(text, assets.MustRead("generic/persona.md")) {
		t.Fatal("managed persona content missing from managed section")
	}
}

// TestInjectClaudeRemovesLegacyNeutralOutputStyle covers installs made before
// Principal Engineer replaced the managed Neutral style: install and sync must
// delete the retired neutral.md so Claude Code no longer offers it.
func TestInjectClaudeRemovesLegacyNeutralOutputStyle(t *testing.T) {
	home := t.TempDir()
	styleDir := filepath.Join(home, ".claude", "output-styles")
	if err := os.MkdirAll(styleDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	legacy := filepath.Join(styleDir, "neutral.md")

	for _, inject := range []struct {
		name string
		run  func(string, agents.Adapter) (InjectionResult, error)
	}{
		{name: "install", run: Inject},
		{name: "sync", run: InjectForSync},
	} {
		t.Run(inject.name, func(t *testing.T) {
			if err := os.WriteFile(legacy, []byte("---\nname: Neutral\n---\n"), 0o644); err != nil {
				t.Fatalf("WriteFile(legacy) error = %v", err)
			}
			result, err := inject.run(home, claudeAdapter())
			if err != nil {
				t.Fatalf("%s error = %v", inject.name, err)
			}
			if !result.Changed {
				t.Fatalf("%s changed = false after removing the legacy style", inject.name)
			}
			if _, err := os.Stat(legacy); !os.IsNotExist(err) {
				t.Fatalf("legacy %s still present after %s (err=%v)", legacy, inject.name, err)
			}
			if _, err := os.Stat(filepath.Join(styleDir, "principal-engineer.md")); err != nil {
				t.Fatalf("principal-engineer.md missing after %s: %v", inject.name, err)
			}

			again, err := inject.run(home, claudeAdapter())
			if err != nil {
				t.Fatalf("second %s error = %v", inject.name, err)
			}
			if again.Changed {
				t.Fatalf("second %s changed = true, want idempotent false", inject.name)
			}
		})
	}
}
