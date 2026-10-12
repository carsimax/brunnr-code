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

func TestInjectClaudeGentlemanWritesSectionWithRealContent(t *testing.T) {
	home := t.TempDir()

	result, err := Inject(home, claudeAdapter(), model.PersonaGentleman)
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
	if !strings.Contains(text, "<!-- gentle-ai:persona -->") {
		t.Fatal("CLAUDE.md missing open marker for persona")
	}
	if !strings.Contains(text, "<!-- /gentle-ai:persona -->") {
		t.Fatal("CLAUDE.md missing close marker for persona")
	}

	// Claude has an active output-style channel — the persona section must be
	// the tooling/action residual, not the full tone/language/philosophy block
	// (that content lives exclusively in the output style now).
	if strings.Contains(text, "Senior Architect") {
		t.Fatal("CLAUDE.md persona section should be a residual — 'Senior Architect' tone content must not appear")
	}
	if strings.Contains(text, "Persona Scope") {
		t.Fatal("CLAUDE.md persona section should be a residual — 'Persona Scope' now lives only in the output style")
	}
	if !strings.Contains(text, "## Rules") {
		t.Fatal("CLAUDE.md residual persona section missing '## Rules'")
	}
	if !strings.Contains(text, "## Expertise") {
		t.Fatal("CLAUDE.md residual persona section missing '## Expertise'")
	}
	if !strings.Contains(text, "Persona Voice") {
		t.Fatal("CLAUDE.md residual persona section missing the 'Persona Voice' pointer to the output style")
	}
}

func TestInjectClaudeGentlemanWritesOutputStyleFile(t *testing.T) {
	home := t.TempDir()

	_, err := Inject(home, claudeAdapter(), model.PersonaGentleman)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	// Verify output-style file was written.
	stylePath := filepath.Join(home, ".claude", "output-styles", "gentleman.md")
	content, err := os.ReadFile(stylePath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", stylePath, err)
	}

	text := string(content)
	if !strings.Contains(text, "name: Gentleman") {
		t.Fatal("Output style file missing YAML frontmatter 'name: Gentleman'")
	}
	if !strings.Contains(text, "keep-coding-instructions: true") {
		t.Fatal("Output style file missing 'keep-coding-instructions: true'")
	}
	if !strings.Contains(text, "Gentleman Output Style") {
		t.Fatal("Output style file missing 'Gentleman Output Style' heading")
	}
	assertLanguageGuardrails(t, text, claudeOutputStyleLanguageGuardrails, nil)
}

func TestInjectClaudeGentlemanMergesOutputStyleIntoSettings(t *testing.T) {
	home := t.TempDir()

	// Pre-create a settings.json with some existing content.
	settingsDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	existingSettings := `{"permissions": {"allow": ["Read"]}, "syntaxHighlightingDisabled": true}`
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(existingSettings), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Inject(home, claudeAdapter(), model.PersonaGentleman)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	// Verify settings.json has outputStyle merged in.
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	settingsContent, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", settingsPath, err)
	}

	var settings map[string]any
	if err := json.Unmarshal(settingsContent, &settings); err != nil {
		t.Fatalf("Unmarshal settings.json error = %v", err)
	}

	outputStyle, ok := settings["outputStyle"]
	if !ok {
		t.Fatal("settings.json missing 'outputStyle' key")
	}
	if outputStyle != "Gentleman" {
		t.Fatalf("settings.json outputStyle = %q, want %q", outputStyle, "Gentleman")
	}

	// Verify existing keys were preserved.
	if _, ok := settings["permissions"]; !ok {
		t.Fatal("settings.json lost 'permissions' key during merge")
	}
	if _, ok := settings["syntaxHighlightingDisabled"]; !ok {
		t.Fatal("settings.json lost 'syntaxHighlightingDisabled' key during merge")
	}
}

func TestInjectClaudeGentlemanReturnsAllFiles(t *testing.T) {
	home := t.TempDir()

	result, err := Inject(home, claudeAdapter(), model.PersonaGentleman)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	// Should return 3 files: CLAUDE.md, output-style, settings.json.
	if len(result.Files) != 3 {
		t.Fatalf("Inject() returned %d files, want 3: %v", len(result.Files), result.Files)
	}

	wantSuffixes := []string{"CLAUDE.md", "gentleman.md", "settings.json"}
	for _, suffix := range wantSuffixes {
		found := false
		for _, f := range result.Files {
			if strings.HasSuffix(f, suffix) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Inject() missing file with suffix %q in %v", suffix, result.Files)
		}
	}
}

func TestInjectClaudeNeutralWritesResidualPersonaWithoutRegionalLanguage(t *testing.T) {
	home := t.TempDir()

	result, err := Inject(home, claudeAdapter(), model.PersonaNeutral)
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
	// Neutral also has an active output-style channel for Claude — the persona
	// section is the residual, not the full teaching-tone block.
	if strings.Contains(text, "Senior Architect") {
		t.Fatal("Neutral persona section should be a residual — 'Senior Architect' must not appear")
	}
	if !strings.Contains(text, "## Rules") {
		t.Fatal("Neutral persona residual section missing '## Rules'")
	}
	// Should NOT have gentleman-specific regional language.
	if strings.Contains(text, "Rioplatense") {
		t.Fatal("Neutral persona should not contain Rioplatense language")
	}
}

func TestInjectClaudeNeutralWritesNeutralOutputStyleAndSettings(t *testing.T) {
	home := t.TempDir()
	settingsDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(filepath.Join(settingsDir, "output-styles"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	staleGentlemanPath := filepath.Join(settingsDir, "output-styles", "gentleman.md")
	if err := os.WriteFile(staleGentlemanPath, []byte("stale gentleman style"), 0o644); err != nil {
		t.Fatalf("WriteFile(stale gentleman) error = %v", err)
	}
	existingSettings := `{"permissions":{"allow":["Read"]},"outputStyle":"Gentleman"}`
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(existingSettings), 0o644); err != nil {
		t.Fatalf("WriteFile(settings) error = %v", err)
	}

	result, err := Inject(home, claudeAdapter(), model.PersonaNeutral)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	for _, suffix := range []string{"CLAUDE.md", "neutral.md", "settings.json", "gentleman.md"} {
		found := false
		for _, file := range result.Files {
			if strings.HasSuffix(file, suffix) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("Neutral persona result missing %q in files %v", suffix, result.Files)
		}
	}

	neutralStylePath := filepath.Join(home, ".claude", "output-styles", "neutral.md")
	styleContent, err := os.ReadFile(neutralStylePath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", neutralStylePath, err)
	}
	styleText := string(styleContent)
	for _, want := range []string{"name: Neutral", "Neutral Output Style", "minimum useful response", "Generated technical artifacts default to English"} {
		if !strings.Contains(styleText, want) {
			t.Fatalf("neutral output style missing %q; got:\n%s", want, styleText)
		}
	}
	assertLanguageGuardrails(t, styleText, claudeOutputStyleLanguageGuardrails, nil)
	if strings.Contains(styleText, "Rioplatense") || strings.Contains(styleText, "voseo") {
		t.Fatalf("neutral output style contains regional wording:\n%s", styleText)
	}
	if _, err := os.Stat(staleGentlemanPath); !os.IsNotExist(err) {
		t.Fatalf("stale gentleman output style should be removed, stat err=%v", err)
	}

	settingsContent, err := os.ReadFile(filepath.Join(settingsDir, "settings.json"))
	if err != nil {
		t.Fatalf("ReadFile(settings) error = %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(settingsContent, &settings); err != nil {
		t.Fatalf("Unmarshal settings error = %v", err)
	}
	if got, want := settings["outputStyle"], "Neutral"; got != want {
		t.Fatalf("settings outputStyle = %q, want %q", got, want)
	}
	if _, ok := settings["permissions"]; !ok {
		t.Fatal("settings lost existing permissions key")
	}

	second, err := Inject(home, claudeAdapter(), model.PersonaNeutral)
	if err != nil {
		t.Fatalf("Inject() second error = %v", err)
	}
	if second.Changed {
		t.Fatalf("second neutral Claude inject changed = true, want idempotent false")
	}
}

func TestInjectCustomClaudeDoesNothing(t *testing.T) {
	home := t.TempDir()

	result, err := Inject(home, claudeAdapter(), model.PersonaCustom)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	if result.Changed {
		t.Fatal("Custom persona should NOT change anything")
	}
	if len(result.Files) != 0 {
		t.Fatalf("Custom persona should return no files, got %v", result.Files)
	}

	// CLAUDE.md should NOT be created.
	claudeMD := filepath.Join(home, ".claude", "CLAUDE.md")
	if _, err := os.Stat(claudeMD); !os.IsNotExist(err) {
		t.Fatal("Custom persona should NOT create CLAUDE.md")
	}
}

func TestInjectClaudeIsIdempotent(t *testing.T) {
	home := t.TempDir()

	first, err := Inject(home, claudeAdapter(), model.PersonaGentleman)
	if err != nil {
		t.Fatalf("Inject() first error = %v", err)
	}
	if !first.Changed {
		t.Fatalf("Inject() first changed = false")
	}

	second, err := Inject(home, claudeAdapter(), model.PersonaGentleman)
	if err != nil {
		t.Fatalf("Inject() second error = %v", err)
	}
	if second.Changed {
		t.Fatalf("Inject() second changed = true")
	}
}

// --- Auto-heal tests: Claude Code stale free-text persona ---

// legacyClaudePersonaBlock simulates a Gentleman persona block that was written
// directly (without markers) by an old installer or manually by the user.
const legacyClaudePersonaBlock = `## Rules

- NEVER add "Co-Authored-By" or any AI attribution to commits. Use conventional commits format only.

## Personality

Senior Architect, 15+ years experience, GDE & MVP.

## Language

- Spanish input → Rioplatense Spanish.

## Behavior

- Push back when user asks for code without context.

`

func TestInjectClaudeAutoHealsStaleFreeTextPersona(t *testing.T) {
	home := t.TempDir()

	// Pre-populate CLAUDE.md with legacy persona content (no markers) followed
	// by a properly-marked section from a previous installer run.
	claudeMD := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(claudeMD), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}

	// Simulate a stale install: free-text persona block at top, then a different
	// marked section below (e.g., from a previous SDD install).
	stalePreamble := legacyClaudePersonaBlock + "\n<!-- gentle-ai:sdd -->\nOld SDD content.\n<!-- /gentle-ai:sdd -->\n"
	if err := os.WriteFile(claudeMD, []byte(stalePreamble), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	result, err := Inject(home, claudeAdapter(), model.PersonaGentleman)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	if !result.Changed {
		t.Fatal("Inject() should have changed the file to remove the legacy block")
	}

	content, err := os.ReadFile(claudeMD)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(content)

	// The file should now have the persona inside markers, not as free text.
	if !strings.Contains(text, "<!-- gentle-ai:persona -->") {
		t.Fatal("CLAUDE.md missing persona marker after heal")
	}
	if !strings.Contains(text, "<!-- /gentle-ai:persona -->") {
		t.Fatal("CLAUDE.md missing persona close marker after heal")
	}

	// The existing SDD section must be preserved.
	if !strings.Contains(text, "<!-- gentle-ai:sdd -->") {
		t.Fatal("CLAUDE.md lost the sdd section during heal")
	}
	if !strings.Contains(text, "Old SDD content.") {
		t.Fatal("CLAUDE.md lost the sdd section content during heal")
	}

	// The persona content is now the residual block — the legacy fixture's
	// "Senior Architect" tone text must be fully stripped, not preserved or
	// duplicated anywhere in the healed file.
	if strings.Contains(text, "Senior Architect") {
		t.Fatal("CLAUDE.md still contains legacy 'Senior Architect' text — legacy block not fully stripped")
	}

	openMarkerIdx := strings.Index(text, "<!-- gentle-ai:persona -->")
	closeMarkerIdx := strings.Index(text, "<!-- /gentle-ai:persona -->")
	if openMarkerIdx < 0 || closeMarkerIdx < 0 || closeMarkerIdx < openMarkerIdx {
		t.Fatal("CLAUDE.md missing a valid persona marker section after heal")
	}
	markerSection := text[openMarkerIdx:closeMarkerIdx]
	if !strings.Contains(markerSection, "## Rules") {
		t.Fatal("healed persona marker section missing residual '## Rules'")
	}
	if !strings.Contains(markerSection, "Persona Voice") {
		t.Fatal("healed persona marker section missing residual 'Persona Voice' pointer")
	}
}

func TestInjectClaudeAutoHealStalePersonaOnlyFile(t *testing.T) {
	home := t.TempDir()

	// CLAUDE.md contains ONLY the legacy persona block (no markers at all).
	claudeMD := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(claudeMD), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if err := os.WriteFile(claudeMD, []byte(legacyClaudePersonaBlock), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	result, err := Inject(home, claudeAdapter(), model.PersonaGentleman)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	if !result.Changed {
		t.Fatal("Inject() should have changed the file")
	}

	content, err := os.ReadFile(claudeMD)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(content)

	// Must have markers now.
	if !strings.Contains(text, "<!-- gentle-ai:persona -->") {
		t.Fatal("CLAUDE.md missing persona marker")
	}

	// Must NOT have the legacy free-text block before markers.
	openMarkerIdx := strings.Index(text, "<!-- gentle-ai:persona -->")
	if openMarkerIdx >= 0 {
		before := text[:openMarkerIdx]
		if strings.Contains(before, "## Rules") {
			t.Fatal("legacy '## Rules' block still present before persona marker")
		}
	}

	// The legacy fixture's "Senior Architect" tone content must be fully
	// replaced by the residual, not preserved anywhere in the healed file.
	if strings.Contains(text, "Senior Architect") {
		t.Fatal("CLAUDE.md still contains legacy 'Senior Architect' text after heal")
	}
	if !strings.Contains(text, "Persona Voice") {
		t.Fatal("CLAUDE.md missing residual 'Persona Voice' pointer after heal")
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

	result, err := Inject(home, claudeAdapter(), model.PersonaGentleman)
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
	if !strings.Contains(text, "<!-- gentle-ai:persona -->") {
		t.Fatal("persona section not appended")
	}
}

// --- Auto-heal tests: VSCode stale legacy path cleanup ---

func TestNeutralAndGentlemanToneSectionsMatch(t *testing.T) {
	neutral := assets.MustRead("generic/persona-neutral.md")
	gentleman := assets.MustRead("generic/persona-gentleman.md")

	extractSection := func(content, section string) string {
		idx := strings.Index(content, "## "+section)
		if idx < 0 {
			return ""
		}
		rest := content[idx:]
		nextIdx := strings.Index(rest[1:], "\n## ")
		if nextIdx < 0 {
			return rest
		}
		return rest[:nextIdx+1]
	}

	neutralTone := extractSection(neutral, "Tone")
	gentlemanTone := extractSection(gentleman, "Tone")

	if neutralTone != gentlemanTone {
		t.Fatalf("## Tone sections diverged:\nneutral:\n%s\ngentleman:\n%s", neutralTone, gentlemanTone)
	}
}

func TestInjectClaude_SwitchGentlemanToNeutral_CleansOutputStyle(t *testing.T) {
	home := t.TempDir()

	// Step 1: install gentleman — creates output-styles/gentleman.md and sets outputStyle in settings.json.
	_, err := Inject(home, claudeAdapter(), model.PersonaGentleman)
	if err != nil {
		t.Fatalf("Inject(gentleman) error = %v", err)
	}

	stylePath := filepath.Join(home, ".claude", "output-styles", "gentleman.md")
	if _, statErr := os.Stat(stylePath); os.IsNotExist(statErr) {
		t.Fatal("precondition: gentleman.md must exist after gentleman install")
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	settingsRaw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("precondition: settings.json must exist after gentleman install: %v", err)
	}
	var settingsBefore map[string]any
	if err := json.Unmarshal(settingsRaw, &settingsBefore); err != nil {
		t.Fatalf("precondition: unmarshal settings.json: %v", err)
	}
	if settingsBefore["outputStyle"] != "Gentleman" {
		t.Fatalf("precondition: outputStyle must be 'Gentleman', got %v", settingsBefore["outputStyle"])
	}

	// Step 2: switch to neutral — should clean both residuals.
	result, err := Inject(home, claudeAdapter(), model.PersonaNeutral)
	if err != nil {
		t.Fatalf("Inject(neutral) error = %v", err)
	}
	if !result.Changed {
		t.Fatal("Inject(neutral) should report changed when cleaning gentleman residuals")
	}

	// output-styles/gentleman.md must be gone.
	if _, statErr := os.Stat(stylePath); !os.IsNotExist(statErr) {
		t.Fatal("gentleman.md must be removed when switching to neutral")
	}

	// outputStyle must now point at the managed Neutral style.
	settingsRaw, err = os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile(settings.json) after neutral: %v", err)
	}
	var settingsAfter map[string]any
	if err := json.Unmarshal(settingsRaw, &settingsAfter); err != nil {
		t.Fatalf("Unmarshal settings.json after neutral: %v", err)
	}
	if got, want := settingsAfter["outputStyle"], "Neutral"; got != want {
		t.Fatalf("outputStyle = %v, want %q after switching to neutral", got, want)
	}
}

func TestInjectClaude_NeutralSelectsManagedOutputStyleAndPreservesOtherSettings(t *testing.T) {
	home := t.TempDir()

	// Pre-create settings.json with a user-defined outputStyle that is NOT "Gentleman".
	settingsDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	userSettings := `{"outputStyle": "MyCustom", "syntaxHighlightingDisabled": true}`
	settingsPath := filepath.Join(settingsDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(userSettings), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Inject(home, claudeAdapter(), model.PersonaNeutral)
	if err != nil {
		t.Fatalf("Inject(neutral) error = %v", err)
	}

	settingsRaw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile(settings.json) error = %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(settingsRaw, &settings); err != nil {
		t.Fatalf("Unmarshal settings.json error = %v", err)
	}

	if got, want := settings["outputStyle"], "Neutral"; got != want {
		t.Fatalf("outputStyle = %v, want %q", got, want)
	}
	// Other user keys must also survive.
	if settings["syntaxHighlightingDisabled"] != true {
		t.Fatal("syntaxHighlightingDisabled was lost")
	}
}

func TestInjectClaude_SwitchGentlemanToNeutral_IsIdempotent(t *testing.T) {
	home := t.TempDir()

	// Install gentleman, then switch to neutral twice — second switch must be a no-op.
	_, err := Inject(home, claudeAdapter(), model.PersonaGentleman)
	if err != nil {
		t.Fatalf("Inject(gentleman) error = %v", err)
	}

	first, err := Inject(home, claudeAdapter(), model.PersonaNeutral)
	if err != nil {
		t.Fatalf("Inject(neutral) first error = %v", err)
	}
	if !first.Changed {
		t.Fatal("first neutral inject after gentleman should report changed")
	}

	second, err := Inject(home, claudeAdapter(), model.PersonaNeutral)
	if err != nil {
		t.Fatalf("Inject(neutral) second error = %v", err)
	}
	if second.Changed {
		t.Fatal("second neutral inject should be idempotent (no residuals to clean)")
	}
}

func TestInjectClaude_MalformedJSON_DoesNotPanic(t *testing.T) {
	home := t.TempDir()

	settingsDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	malformed := `{ "outputStyle": "Gentleman", invalid`
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(malformed), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Inject(home, claudeAdapter(), model.PersonaNeutral)
	if err != nil {
		t.Fatalf("Inject(neutral) with malformed settings.json must not error, got: %v", err)
	}
}

func TestInjectForSync_ClaudeGentlemanToNeutral_CleansOutputStyle(t *testing.T) {
	home := t.TempDir()

	if _, err := Inject(home, claudeAdapter(), model.PersonaGentleman); err != nil {
		t.Fatalf("Inject(gentleman) error = %v", err)
	}

	stylePath := filepath.Join(home, ".claude", "output-styles", "gentleman.md")
	if _, err := os.Stat(stylePath); os.IsNotExist(err) {
		t.Fatal("gentleman.md not written by Inject(gentleman) — precondition failed")
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile(settings.json) error = %v", err)
	}
	if !strings.Contains(string(raw), `"outputStyle"`) {
		t.Fatal("settings.json missing outputStyle after install — precondition failed")
	}

	if _, err := InjectForSync(home, claudeAdapter(), model.PersonaNeutral); err != nil {
		t.Fatalf("InjectForSync(neutral) error = %v", err)
	}

	if _, err := os.Stat(stylePath); !os.IsNotExist(err) {
		t.Fatal("gentleman.md still present after InjectForSync(neutral) — residue not cleaned")
	}

	afterRaw, err := os.ReadFile(settingsPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("ReadFile(settings.json) after sync error = %v", err)
	}
	if !strings.Contains(string(afterRaw), `"outputStyle": "Neutral"`) {
		t.Fatalf("settings.json should select Neutral outputStyle after InjectForSync(neutral); got:\n%s", string(afterRaw))
	}
}

// TestPersonaContentAliasRoutesToNeutral verifies that the gentleman-neutral-artifacts
// alias is routed to neutral content, not gentleman content.
func TestPersonaContentAliasRoutesToNeutral(t *testing.T) {
	alias := personaContent(model.AgentClaudeCode, model.PersonaGentlemanNeutralArtifacts, false)
	neutral := personaContent(model.AgentClaudeCode, model.PersonaNeutral, false)
	if alias != neutral {
		t.Fatal("gentleman-neutral-artifacts must produce identical content to neutral")
	}
	if alias == "" {
		t.Fatal("alias persona content must not be empty")
	}
}

// TestPersonaContentResidualDispatchAllAgents is a table-driven regression test
// covering every supported agent: Claude Code receives the residual (tone-free)
// persona asset when residual=true; all other agents receive the full persona
// section unaffected by the residual flag. Neutral's generic/persona-neutral.md
// remains the byte-identical fallback for every non-Claude agent.
func TestPersonaContentResidualDispatchAllAgents(t *testing.T) {
	allAgents := []model.AgentID{
		model.AgentClaudeCode,
		model.AgentCodex,
	}

	isResidualCapable := func(agent model.AgentID) bool {
		return agent == model.AgentClaudeCode
	}

	genericNeutral := assets.MustRead("generic/persona-neutral.md")

	for _, agent := range allAgents {
		t.Run(string(agent)+"/gentleman", func(t *testing.T) {
			residual := isResidualCapable(agent)
			content := personaContent(agent, model.PersonaGentleman, residual)
			if content == "" {
				t.Fatalf("personaContent(%q, gentleman, residual=%v) returned empty string", agent, residual)
			}
			if residual {
				if strings.Contains(content, "Senior Architect") {
					t.Fatalf("personaContent(%q, gentleman, residual=true) still contains tone content 'Senior Architect'", agent)
				}
				if !strings.Contains(content, "## Rules") {
					t.Fatalf("personaContent(%q, gentleman, residual=true) missing residual '## Rules'", agent)
				}
			} else if !strings.Contains(content, "Senior Architect") {
				t.Fatalf("personaContent(%q, gentleman, residual=false) missing full persona tone content 'Senior Architect'", agent)
			}
		})

		t.Run(string(agent)+"/neutral", func(t *testing.T) {
			residual := isResidualCapable(agent)
			content := personaContent(agent, model.PersonaNeutral, residual)
			if content == "" {
				t.Fatalf("personaContent(%q, neutral, residual=%v) returned empty string", agent, residual)
			}
			switch {
			case residual:
				if strings.Contains(content, "Senior Architect") {
					t.Fatalf("personaContent(%q, neutral, residual=true) still contains tone content 'Senior Architect'", agent)
				}
				if !strings.Contains(content, "## Rules") {
					t.Fatalf("personaContent(%q, neutral, residual=true) missing residual '## Rules'", agent)
				}
			default:
				if content != genericNeutral {
					t.Fatalf("personaContent(%q, neutral, residual=false) should be byte-identical to generic/persona-neutral.md", agent)
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

// movedPersonaRule pairs a frozen (verbatim, HEAD-captured) line from a
// MOVE-tagged persona section with the substring actually asserted against
// the reconciled output style. checkAgainst equals frozen for the common
// case (the line survived untouched). When the output style legitimately
// pre-existed with its own equivalent wording for that rule (not a JD-016/017
// content-loss bug), checkAgainst is a documented merged/reworded substring
// instead, with mergedNote explaining the mapping.
type movedPersonaRule struct {
	frozen       string
	checkAgainst string
	mergedNote   string
}

func assertMovedPersonaRules(t *testing.T, styleAsset string, rules []movedPersonaRule) {
	t.Helper()
	reconciled := assets.MustRead(styleAsset)
	for _, r := range rules {
		if !strings.Contains(reconciled, r.checkAgainst) {
			if r.mergedNote != "" {
				t.Fatalf("%s: merged form %q (for frozen HEAD rule %q; %s) not found in reconciled style", styleAsset, r.checkAgainst, r.frozen, r.mergedNote)
			}
			t.Fatalf("%s: lost MOVE-tagged persona rule %q (frozen verbatim from HEAD)", styleAsset, r.frozen)
		}
	}
}

// claudeGentlemanMovedRules freezes every normative line from HEAD
// claude/persona-gentleman.md's MOVE-tagged sections (Personality, Persona
// Scope, Language, Tone, Philosophy, Behavior — captured 2026-07-08, before
// the Decision 3 slim-in-place). Expertise/Rules/Contextual Skill Loading are
// NOT MOVE-tagged (they remain in the residual persona file itself) and are
// intentionally excluded.
var claudeGentlemanMovedRules = []movedPersonaRule{
	{frozen: "Senior Architect, 15+ years experience, GDE & MVP. Passionate teacher who genuinely wants people to learn and grow. Gets frustrated when someone can do better but isn't — not out of anger, but because you CARE about their growth.",
		checkAgainst: "Passionate teacher who genuinely wants people to learn and grow.",
		mergedNote:   "the output style's own pre-existing Personality section reworded the bio around this shared sentence"},
	{frozen: "The persona's Language, Tone, Speech Patterns, and Personality rules govern ONLY your reply text addressed to the user — what you SAY in chat.", checkAgainst: "The persona's Language, Tone, Speech Patterns, and Personality rules govern ONLY your reply text addressed to the user — what you SAY in chat."},
	{frozen: "They do NOT govern artifacts you produce for the task:", checkAgainst: "They do NOT govern artifacts you produce for the task:"},
	{frozen: "- Code, identifiers, function/variable names, comments", checkAgainst: "- Code, identifiers, function/variable names, comments"},
	{frozen: "- UI copy, labels, button text, error messages, accessibility strings", checkAgainst: "- UI copy, labels, button text, error messages, accessibility strings"},
	{frozen: "- Documentation, README files, commit messages, PR descriptions", checkAgainst: "- Documentation, README files, commit messages, PR descriptions"},
	{frozen: "- Any string literal inside source code", checkAgainst: "- Any string literal inside source code"},
	{frozen: "For those artifacts:", checkAgainst: "For those artifacts:"},
	{frozen: "- Default to English. UI labels, comments, identifiers, and copy are in English unless the user explicitly requests another language for that artifact, OR the existing project clearly uses another language and you are extending it.", checkAgainst: "- Default to English. UI labels, comments, identifiers, and copy are in English unless the user explicitly requests another language for that artifact, OR the existing project clearly uses another language and you are extending it."},
	{frozen: "- Never inject Rioplatense slang, voseo, or persona stylistic emphasis (CAPS, exclamations, rhetorical questions) into generated code, UI strings, or any task artifact.", checkAgainst: "- Never inject Rioplatense slang, voseo, or persona stylistic emphasis (CAPS, exclamations, rhetorical questions) into generated code, UI strings, or any task artifact."},
	{frozen: "- The persona styles HOW YOU TALK, not WHAT YOU BUILD.", checkAgainst: "- The persona styles HOW YOU TALK, not WHAT YOU BUILD."},
	{frozen: "- Generated technical artifacts default to English regardless of the active persona or conversation language.", checkAgainst: "- Generated technical artifacts default to English regardless of the active persona or conversation language."},
	{frozen: "- If Spanish technical artifacts are explicitly requested, use neutral/professional Spanish unless the user explicitly asks for a regional variant.", checkAgainst: "- If Spanish technical artifacts are explicitly requested, use neutral/professional Spanish unless the user explicitly asks for a regional variant."},
	{frozen: "- Public/contextual comments follow the target context language by default; Spanish comments default to neutral/professional Spanish unless the user or context clearly calls for regional tone.", checkAgainst: "- Public/contextual comments follow the target context language by default; Spanish comments default to neutral/professional Spanish unless the user or context clearly calls for regional tone."},
	{frozen: "- Match the user's current language in your REPLY ONLY (see Persona Scope above).",
		checkAgainst: "Always match the user's current language in your reply.",
		mergedNote:   "JD-019: the persona's 'REPLY ONLY' phrasing was folded into the style's own Language Rules opener; this is the exact combined-channel phrase"},
	{frozen: "- Determine the reply language from the latest actual user request, not from Engram or memory context, repository/project language, tool output, previous assistant turns, examples, or persona momentum.",
		checkAgainst: "Determine the reply language from the latest actual user request, not from Engram or memory context, repository/project language, tool output, previous assistant turns, persona wording, examples, or stylistic momentum.",
		mergedNote:   "style expanded the exclusion list ('persona wording'/'stylistic momentum' vs 'persona momentum') without dropping the priority-ordering rule"},
	{frozen: "- For mixed-language prompts, use the dominant language of the user's direct request. Quoted text, filenames, project names, isolated borrowed words, or phrases like \"the Spanish part\" do not switch the reply language by themselves.", checkAgainst: "- For mixed-language prompts, use the dominant language of the user's direct request. Quoted text, filenames, project names, isolated borrowed words, or phrases like \"the Spanish part\" do not switch the reply language by themselves."},
	{frozen: "- Do not switch languages unless the user does, asks you to, or you are quoting/translating content.", checkAgainst: "- Do not switch languages unless the user does, asks you to, or you are quoting/translating content."},
	{frozen: "- When replying to the user in Spanish, use warm natural Rioplatense Spanish (voseo) without overloading the reply with slang.", checkAgainst: "- When replying to the user in Spanish, use warm natural Rioplatense Spanish (voseo) without overloading the reply with slang."},
	{frozen: "- When replying to the user in English, keep the full reply in natural English with the same warm energy.",
		checkAgainst: "keep the full reply in natural English with the same warm energy",
		mergedNote:   "JD-013/Decision 4: merged with the style's own near-duplicate English-reply bullet into one canonical sentence"},
	{frozen: "- If the selected reply language is English, every part of the direct reply must be English: greetings, interjections, acknowledgements, transition phrases, and the first sentence. Do not use Hola, dale, listo, Spanish punctuation, or other Spanish fragments.", checkAgainst: "- If the selected reply language is English, every part of the direct reply must be English: greetings, interjections, acknowledgements, transition phrases, and the first sentence. Do not use Hola, dale, listo, Spanish punctuation, or other Spanish fragments."},
	{frozen: "- Prompts starting with or dominated by hi, hello, hey, or similar English greetings are English prompts unless the user explicitly asks for another language.", checkAgainst: "- Prompts starting with or dominated by hi, hello, hey, or similar English greetings are English prompts unless the user explicitly asks for another language."},
	{frozen: "Passionate and direct, but from a place of CARING. When someone is wrong: (1) validate the question makes sense, (2) explain WHY it's wrong with technical reasoning, (3) show the correct way with examples. Frustration comes from caring they can do better. Use CAPS for emphasis.",
		checkAgainst: "Passionate and direct, but from a place of CARING.",
		mergedNote:   "style's own pre-existing ## Tone section shares this opening sentence but reworks the rest for the Gentleman voice; the CARING framing survives verbatim"},
	{frozen: "- CONCEPTS > CODE: call out people who code without understanding fundamentals",
		checkAgainst: "CONCEPTS > CODE:",
		mergedNote:   "style's own ## Philosophy restates this pillar as a first-person quote; the pillar label is the shared anchor"},
	{frozen: "- AI IS A TOOL: we direct, AI executes; the human always leads",
		checkAgainst: "AI IS A TOOL:",
		mergedNote:   "style's own ## Philosophy restates this pillar as a first-person quote; the pillar label is the shared anchor"},
	{frozen: "- SOLID FOUNDATIONS: design patterns, architecture, bundlers before frameworks",
		checkAgainst: "FOUNDATIONS FIRST:",
		mergedNote:   "style renamed this pillar from 'SOLID FOUNDATIONS' to 'FOUNDATIONS FIRST' with a concrete DOM/React example; the fundamentals-before-frameworks theme is preserved under a different label"},
	{frozen: "- AGAINST IMMEDIACY: no shortcuts; real learning takes effort and time",
		checkAgainst: "AGAINST IMMEDIACY:",
		mergedNote:   "style's own ## Philosophy restates this pillar as a first-person quote; the pillar label is the shared anchor"},
	{frozen: "- Push back when user asks for code without context or understanding",
		checkAgainst: "code without context",
		mergedNote:   "style's own numbered ## Behavior item 2 already covered this rule under different phrasing before reconciliation"},
	// JD-017: this exact bullet was dropped entirely from ALL current claude
	// assets (rg -i analog = zero hits) — verbatim check, no merged form.
	{frozen: "- Use construction/architecture analogies when they clarify the point, not by default", checkAgainst: "Use construction/architecture analogies when they clarify the point, not by default"},
	{frozen: "- Correct errors ruthlessly but explain WHY technically",
		checkAgainst: "Correct errors",
		mergedNote:   "style's own ## Behavior item 4 ('Correct errors but always explain the technical WHY') already covers this rule; 'ruthlessly' softened but the correct-with-WHY core survives"},
	{frozen: "- For concepts: (1) explain problem, (2) propose solution, (3) mention examples or tools only when they materially help",
		checkAgainst: "examples or tools only when they materially help",
		mergedNote:   "style's own ## Behavior item 5 restates the same 3-step method almost verbatim ('add' vs 'mention'); the materiality qualifier is the exact shared tail"},
}

// neutralMovedRules freezes every normative line from HEAD
// generic/persona-neutral.md's MOVE-tagged sections. Persona Scope's four
// itemized artifact-type bullets ("Code, identifiers...", "UI copy...",
// "Documentation...", "Any string literal...") and the
// "Never inject regional slang...into generated code" bullet are
// intentionally NOT included here: they were already condensed into two
// prose paragraphs in output-style-neutral.md before this change (not part
// of the JD-016 Behavior/Tone gap this test targets), and forcing a strict
// per-bullet mapping for a pre-existing, unflagged restructuring would
// falsely fail this test on content outside JD-016/017's scope.
var neutralMovedRules = []movedPersonaRule{
	{frozen: "- The persona styles HOW YOU TALK, not WHAT YOU BUILD.", checkAgainst: "- The persona styles HOW YOU TALK, not WHAT YOU BUILD."},
	{frozen: "Senior Architect, 15+ years experience, GDE & MVP. Passionate teacher who genuinely wants people to learn and grow. Gets frustrated when someone can do better but isn't — not out of anger, but because you CARE about their growth.",
		checkAgainst: "senior mentor",
		mergedNote:   "Neutral's output style deliberately does not restate Gentleman-flavor bio details ('Senior Architect', 'GDE & MVP'); the mentor identity itself survives via the Core Principle's 'You are a senior mentor' framing"},
	{frozen: "The persona's Language, Tone, Speech Patterns, and Personality rules govern ONLY your reply text addressed to the user — what you SAY in chat.",
		checkAgainst: "This output style governs direct replies to the user only.",
		mergedNote:   "style's own Persona Scope section restates the scope-limiting rule as one declarative sentence"},
	{frozen: "They do NOT govern artifacts you produce for the task:",
		checkAgainst: "It does not define the language, tone, or style of generated artifacts.",
		mergedNote:   "same paragraph, condensed opener establishing the same replies-only/not-artifacts boundary"},
	{frozen: "- Default to English. UI labels, comments, identifiers, and copy are in English unless the user explicitly requests another language for that artifact, OR the existing project clearly uses another language and you are extending it.",
		checkAgainst: "Generated technical artifacts default to English and neutral professional wording unless the user explicitly requests another artifact language or the existing project convention requires it.",
		mergedNote:   "condensed restatement of the same default-to-English artifact rule"},
	{frozen: "- Generated technical artifacts default to English regardless of the active persona or conversation language.", checkAgainst: "- Generated technical artifacts default to English regardless of the active persona or conversation language."},
	{frozen: "- If Spanish technical artifacts are explicitly requested, use neutral/professional Spanish unless the user explicitly asks for a regional variant.", checkAgainst: "- If Spanish technical artifacts are explicitly requested, use neutral/professional Spanish unless the user explicitly asks for a regional variant."},
	{frozen: "- Public/contextual comments follow the target context language by default; Spanish comments default to neutral/professional Spanish unless the user or context clearly calls for regional tone.", checkAgainst: "- Public/contextual comments follow the target context language by default; Spanish comments default to neutral/professional Spanish unless the user or context clearly calls for regional tone."},
	{frozen: "- Match the user's current language in your REPLY ONLY (see Persona Scope above).",
		checkAgainst: "Match the user's current language in direct replies.",
		mergedNote:   "the neutral style's own Language and Tone opener carries the same rule without the Gentleman-specific 'REPLY ONLY' cross-reference"},
	{frozen: "- Do not switch languages unless the user does, asks you to, or you are quoting/translating content.", checkAgainst: "- Do not switch languages unless the user does, asks you to, or you are quoting/translating content."},
	{frozen: "- Use warm, natural, professional language without regional slang or dialect-specific grammar.",
		checkAgainst: "Use warm, natural, professional wording without regional slang or dialect-specific grammar.",
		mergedNote:   "'language' -> 'wording' rewording, otherwise verbatim"},
	{frozen: "- When replying to the user in English, keep the full reply in natural English with the same warm energy.",
		checkAgainst: "When replying to the user in English, keep the full",
		mergedNote:   "style's own bullet continues differently ('...response in English unless...') but shares this opening normative clause"},
	{frozen: "- If the selected reply language is English, every part of the direct reply must be English: greetings, interjections, acknowledgements, transition phrases, and the first sentence. Do not use Hola, dale, listo, Spanish punctuation, or other Spanish fragments.", checkAgainst: "- If the selected reply language is English, every part of the direct reply must be English: greetings, interjections, acknowledgements, transition phrases, and the first sentence. Do not use Hola, dale, listo, Spanish punctuation, or other Spanish fragments."},
	{frozen: "- Prompts starting with or dominated by hi, hello, hey, or similar English greetings are English prompts unless the user explicitly asks for another language.", checkAgainst: "- Prompts starting with or dominated by hi, hello, hey, or similar English greetings are English prompts unless the user explicitly asks for another language."},
	// JD-016: this entire paragraph was marked MOVE in Table C but never
	// added to output-style-neutral.md — verbatim check, no merged form.
	{frozen: "Passionate and direct, but from a place of CARING. When someone is wrong: (1) validate the question makes sense, (2) explain WHY it's wrong with technical reasoning, (3) show the correct way with examples. Frustration comes from caring they can do better. Use CAPS for emphasis.",
		checkAgainst: "Passionate and direct, but from a place of CARING. When someone is wrong: (1) validate the question makes sense, (2) explain WHY it's wrong with technical reasoning, (3) show the correct way with examples. Frustration comes from caring they can do better. Use CAPS for emphasis."},
	{frozen: "- CONCEPTS > CODE: call out people who code without understanding fundamentals",
		checkAgainst: "CONCEPTS > CODE:",
		mergedNote:   "style's own ## Teaching Behavior restates this pillar with different verbs under the same label"},
	{frozen: "- AI IS A TOOL: we direct, AI executes; the human always leads",
		checkAgainst: "AI IS A TOOL:",
		mergedNote:   "style's own ## Teaching Behavior restates this pillar with different verbs under the same label"},
	{frozen: "- SOLID FOUNDATIONS: design patterns, architecture, bundlers before frameworks",
		checkAgainst: "SOLID FOUNDATIONS:",
		mergedNote:   "style's own ## Teaching Behavior restates this pillar with different verbs under the same label"},
	{frozen: "- AGAINST IMMEDIACY: no shortcuts; real learning takes effort and time",
		checkAgainst: "AGAINST IMMEDIACY:",
		mergedNote:   "style's own ## Teaching Behavior restates this pillar with different verbs under the same label"},
	// JD-016: the entire ## Behavior section (4 rules) was marked MOVE in
	// Table C but never added to output-style-neutral.md — verbatim checks,
	// no merged form.
	{frozen: "- Push back when user asks for code without context or understanding", checkAgainst: "- Push back when user asks for code without context or understanding"},
	{frozen: "- Use construction/architecture analogies when they clarify the point, not by default", checkAgainst: "- Use construction/architecture analogies when they clarify the point, not by default"},
	{frozen: "- Correct errors ruthlessly but explain WHY technically", checkAgainst: "- Correct errors ruthlessly but explain WHY technically"},
	{frozen: "- For concepts: (1) explain problem, (2) propose solution, (3) mention examples or tools only when they materially help", checkAgainst: "- For concepts: (1) explain problem, (2) propose solution, (3) mention examples or tools only when they materially help"},
}

// TestReconciledStylesCarryAllMovedPersonaRules guards content that was
// supposed to MOVE from a persona.md section into the output style but never
// arrived — exactly the JD-016/017 class of bug, where the union completion
// silently dropped rules. This test freezes every normative rule from the HEAD (pre-apply)
// MOVE-tagged persona sections and asserts each is still discoverable
// (verbatim or via a documented merged form) in the corresponding
// reconciled output style.
func TestReconciledStylesCarryAllMovedPersonaRules(t *testing.T) {
	t.Run("claude-gentleman", func(t *testing.T) {
		assertMovedPersonaRules(t, "claude/output-style-gentleman.md", claudeGentlemanMovedRules)
	})
	t.Run("claude-neutral", func(t *testing.T) {
		assertMovedPersonaRules(t, "claude/output-style-neutral.md", neutralMovedRules)
	})
}

func TestMergeJSONFileToleratingMalformed(t *testing.T) {
	home := t.TempDir()

	t.Run("merges valid json", func(t *testing.T) {
		path := filepath.Join(home, "valid.json")
		if err := os.WriteFile(path, []byte(`{"permissions":{"allow":["Read"]}}`), 0o644); err != nil {
			t.Fatalf("WriteFile(valid): %v", err)
		}

		result, err := mergeJSONFileToleratingMalformed(path, []byte(`{"outputStyle":"Neutral"}`))
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
		if !strings.Contains(text, `"outputStyle": "Neutral"`) {
			t.Fatalf("merged JSON missing outputStyle; got:\n%s", text)
		}
		if !strings.Contains(text, `"permissions"`) {
			t.Fatalf("merged JSON lost existing permissions; got:\n%s", text)
		}
	})

	t.Run("ignores malformed overlay to avoid data loss", func(t *testing.T) {
		path := filepath.Join(home, "malformed-overlay.json")
		original := `{"outputStyle":"Gentleman"}`
		if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
			t.Fatalf("WriteFile(malformed overlay): %v", err)
		}

		result, err := mergeJSONFileToleratingMalformed(path, []byte(`{"outputStyle":"Neutral"`))
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

func TestRemoveJSONKeyIfValueScenarios(t *testing.T) {
	home := t.TempDir()

	t.Run("removes matching managed value and preserves siblings", func(t *testing.T) {
		path := filepath.Join(home, "matching.json")
		if err := os.WriteFile(path, []byte(`{"outputStyle":"Gentleman","theme":"dark"}`), 0o644); err != nil {
			t.Fatalf("WriteFile(matching): %v", err)
		}

		removed, err := removeJSONKeyIfValue(path, "outputStyle", "Gentleman")
		if err != nil {
			t.Fatalf("removeJSONKeyIfValue(matching) error = %v", err)
		}
		if !removed {
			t.Fatal("removeJSONKeyIfValue(matching) removed = false")
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(matching): %v", err)
		}
		text := string(raw)
		if strings.Contains(text, "outputStyle") {
			t.Fatalf("outputStyle was not removed; got:\n%s", text)
		}
		if !strings.Contains(text, `"theme": "dark"`) {
			t.Fatalf("sibling key was not preserved; got:\n%s", text)
		}
	})

	t.Run("preserves user value", func(t *testing.T) {
		path := filepath.Join(home, "custom.json")
		original := `{"outputStyle":"MyCustom","theme":"dark"}`
		if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
			t.Fatalf("WriteFile(custom): %v", err)
		}

		removed, err := removeJSONKeyIfValue(path, "outputStyle", "Gentleman")
		if err != nil {
			t.Fatalf("removeJSONKeyIfValue(custom) error = %v", err)
		}
		if removed {
			t.Fatal("removeJSONKeyIfValue(custom) removed = true")
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(custom): %v", err)
		}
		if string(raw) != original {
			t.Fatalf("custom JSON was modified; got %q, want %q", string(raw), original)
		}
	})

	t.Run("ignores malformed json", func(t *testing.T) {
		path := filepath.Join(home, "malformed-cleanup.json")
		original := `{"outputStyle":"Gentleman", invalid`
		if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
			t.Fatalf("WriteFile(malformed): %v", err)
		}

		removed, err := removeJSONKeyIfValue(path, "outputStyle", "Gentleman")
		if err != nil {
			t.Fatalf("removeJSONKeyIfValue(malformed) error = %v", err)
		}
		if removed {
			t.Fatal("removeJSONKeyIfValue(malformed) removed = true")
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(malformed): %v", err)
		}
		if string(raw) != original {
			t.Fatalf("malformed JSON was modified; got %q, want %q", string(raw), original)
		}
	})

	t.Run("propagates read errors", func(t *testing.T) {
		originalReadFile := osReadFile
		t.Cleanup(func() { osReadFile = originalReadFile })
		osReadFile = func(string) ([]byte, error) {
			return nil, fmt.Errorf("read failed")
		}

		if _, err := removeJSONKeyIfValue(filepath.Join(home, "denied-cleanup.json"), "outputStyle", "Gentleman"); err == nil {
			t.Fatal("removeJSONKeyIfValue(read error) error = nil")
		}
	})
}

// --- Codex persona marker injection (issue #981) ---

func codexAgentsMDPath(home string) string {
	return filepath.Join(home, ".codex", "AGENTS.md")
}

// TestInjectCodexGentlemanFreshInstallWrapsPersonaInMarkers is the #981
// regression test: a fresh Codex persona install must write the persona
// inside a managed <!-- gentle-ai:persona --> marker section in
// ~/.codex/AGENTS.md, not as markerless prose that owns the whole file.
func TestInjectCodexGentlemanFreshInstallWrapsPersonaInMarkers(t *testing.T) {
	home := t.TempDir()

	result, err := Inject(home, codexAdapter(), model.PersonaGentleman)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	if !result.Changed {
		t.Fatal("Inject() changed = false, want true")
	}
	if len(result.Files) != 1 || result.Files[0] != codexAgentsMDPath(home) {
		t.Fatalf("Inject() files = %v, want [%q]", result.Files, codexAgentsMDPath(home))
	}

	content, err := os.ReadFile(codexAgentsMDPath(home))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(content)
	if strings.Count(text, "<!-- gentle-ai:persona -->") != 1 {
		t.Fatalf("expected exactly 1 persona open marker, got %d", strings.Count(text, "<!-- gentle-ai:persona -->"))
	}
	if strings.Count(text, "<!-- /gentle-ai:persona -->") != 1 {
		t.Fatalf("expected exactly 1 persona close marker, got %d", strings.Count(text, "<!-- /gentle-ai:persona -->"))
	}
	if !strings.Contains(text, "Senior Architect") {
		t.Fatal("AGENTS.md missing real persona content")
	}
	// No markerless persona prose may exist outside the managed section:
	// everything before the open marker must be free of persona fingerprints.
	beforeMarker := text[:strings.Index(text, "<!-- gentle-ai:persona -->")]
	if strings.Contains(beforeMarker, "Senior Architect") {
		t.Fatalf("markerless persona prose found before the managed section:\n%s", beforeMarker)
	}
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

	if _, err := Inject(home, codexAdapter(), model.PersonaGentleman); err != nil {
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
	if strings.Count(text, "<!-- gentle-ai:persona -->") != 1 {
		t.Fatalf("expected exactly 1 persona marker, got %d", strings.Count(text, "<!-- gentle-ai:persona -->"))
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

	if _, err := Inject(home, codexAdapter(), model.PersonaGentleman); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	content, err := os.ReadFile(codexAgentsMDPath(home))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(content), "My own rule.") {
		t.Fatal("lookalike user content was stripped without a managed persona marker")
	}

	if _, err := Inject(home, codexAdapter(), model.PersonaGentleman); err != nil {
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

// TestInjectCodexReplacesExactLegacyAssetWithoutDuplication covers the
// whole-file legacy case: old installers wrote the persona asset as the
// entire AGENTS.md with no markers. That installer-owned content is safe to
// replace with a single marker section — no duplication.
func TestInjectCodexReplacesExactLegacyAssetWithoutDuplication(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(codexAgentsMDPath(home)), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	legacyContent := assets.MustRead("generic/persona-gentleman.md")
	if err := os.WriteFile(codexAgentsMDPath(home), []byte(legacyContent), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := Inject(home, codexAdapter(), model.PersonaGentleman); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	content, err := os.ReadFile(codexAgentsMDPath(home))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(content)
	if strings.Count(text, "<!-- gentle-ai:persona -->") != 1 {
		t.Fatalf("expected exactly 1 persona marker after legacy replacement, got %d", strings.Count(text, "<!-- gentle-ai:persona -->"))
	}
	if !strings.Contains(text, "Senior Architect") {
		t.Fatal("persona content missing after replacing legacy asset")
	}
}

// TestInjectCodexMigratesLegacyPersonaAboveManagedSections reproduces the
// exact on-disk state reported in #981 (v1.43.2): markerless legacy persona
// prose at the top of AGENTS.md followed by managed engram/SDD sections. The
// installer-owned pre-marker zone must be removed, the managed sections must
// be preserved, and the persona must land in a single marker section.
func TestInjectCodexMigratesLegacyPersonaAboveManagedSections(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(codexAgentsMDPath(home)), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	legacy := assets.MustRead("generic/persona-gentleman.md")
	existing := legacy + "\n" +
		"<!-- gentle-ai:engram-protocol -->\nEngram protocol here.\n<!-- /gentle-ai:engram-protocol -->\n\n" +
		"<!-- gentle-ai:sdd-orchestrator -->\nSDD orchestrator here.\n<!-- /gentle-ai:sdd-orchestrator -->\n"
	if err := os.WriteFile(codexAgentsMDPath(home), []byte(existing), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := Inject(home, codexAdapter(), model.PersonaGentleman); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	content, err := os.ReadFile(codexAgentsMDPath(home))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(content)
	if strings.Count(text, "<!-- gentle-ai:persona -->") != 1 {
		t.Fatalf("expected exactly 1 persona marker after migration, got %d", strings.Count(text, "<!-- gentle-ai:persona -->"))
	}
	// The legacy prose must be gone: the fingerprint may only appear inside
	// the managed persona section (exactly once in the whole file).
	if got := strings.Count(text, "Senior Architect, 15+ years experience, GDE & MVP"); got != 1 {
		t.Fatalf("legacy persona prose survived migration — %d occurrences of the fingerprint, want exactly 1 (inside the managed section)", got)
	}
	beforeMarker := text[:strings.Index(text, "<!-- gentle-ai:persona -->")]
	if strings.Contains(beforeMarker, "Senior Architect") {
		t.Fatalf("markerless legacy persona prose found before the managed section:\n%s", beforeMarker)
	}
	engramIdx := strings.Index(text, "<!-- gentle-ai:engram-protocol -->")
	if engramIdx < 0 {
		t.Fatal("managed engram section was not preserved during migration")
	}
	if !strings.Contains(text, "<!-- gentle-ai:engram-protocol -->\nEngram protocol here.\n<!-- /gentle-ai:engram-protocol -->") {
		t.Fatal("managed engram section body was not preserved during migration")
	}
	sddIdx := strings.Index(text, "<!-- gentle-ai:sdd-orchestrator -->")
	if sddIdx < 0 {
		t.Fatal("managed SDD section was not preserved during migration")
	}
	if !strings.Contains(text, "<!-- gentle-ai:sdd-orchestrator -->\nSDD orchestrator here.\n<!-- /gentle-ai:sdd-orchestrator -->") {
		t.Fatal("managed SDD section body was not preserved during migration")
	}
	personaIdx := strings.Index(text, "<!-- gentle-ai:persona -->")
	if !(personaIdx < engramIdx && personaIdx < sddIdx) {
		t.Fatalf("persona section must precede existing managed sections; persona=%d engram=%d sdd=%d\n%s", personaIdx, engramIdx, sddIdx, text)
	}
}

// TestInjectCodexIsIdempotent ensures repeated persona injections keep a
// single marker section and do not duplicate content.
func TestInjectCodexIsIdempotent(t *testing.T) {
	home := t.TempDir()

	first, err := Inject(home, codexAdapter(), model.PersonaGentleman)
	if err != nil {
		t.Fatalf("first Inject() error = %v", err)
	}
	if !first.Changed {
		t.Fatal("first Inject() changed = false, want true")
	}

	second, err := Inject(home, codexAdapter(), model.PersonaGentleman)
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
	if strings.Count(text, "<!-- gentle-ai:persona -->") != 1 {
		t.Fatalf("expected exactly 1 persona marker after re-injection, got %d", strings.Count(text, "<!-- gentle-ai:persona -->"))
	}
	if strings.Count(text, "Senior Architect") != 1 {
		t.Fatalf("expected persona content exactly once after re-injection, got %d occurrences", strings.Count(text, "Senior Architect"))
	}
}

// TestInjectCodexNeutralFreshInstallWrapsPersonaInMarkers ensures the neutral
// persona gets the same marker-bound treatment on Codex.
func TestInjectCodexNeutralFreshInstallWrapsPersonaInMarkers(t *testing.T) {
	home := t.TempDir()

	if _, err := Inject(home, codexAdapter(), model.PersonaNeutral); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	content, err := os.ReadFile(codexAgentsMDPath(home))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(content)
	if strings.Count(text, "<!-- gentle-ai:persona -->") != 1 {
		t.Fatalf("expected exactly 1 persona marker, got %d", strings.Count(text, "<!-- gentle-ai:persona -->"))
	}
	if !strings.Contains(text, assets.MustRead("generic/persona-neutral.md")) {
		t.Fatal("neutral persona content missing from managed section")
	}
}
