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

func TestInjectClaudeNeutralWritesResidualPersonaWithoutRegionalLanguage(t *testing.T) {
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
	existingSettings := `{"permissions":{"allow":["Read"]},"outputStyle":"Other"}`
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(existingSettings), 0o644); err != nil {
		t.Fatalf("WriteFile(settings) error = %v", err)
	}

	result, err := Inject(home, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	for _, suffix := range []string{"CLAUDE.md", "neutral.md", "settings.json"} {
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

	second, err := Inject(home, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject() second error = %v", err)
	}
	if second.Changed {
		t.Fatalf("second neutral Claude inject changed = true, want idempotent false")
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

	_, err := Inject(home, claudeAdapter())
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

	_, err := Inject(home, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject(neutral) with malformed settings.json must not error, got: %v", err)
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
		t.Run(string(agent)+"/neutral", func(t *testing.T) {
			residual := isResidualCapable(agent)
			content := neutralPersonaContent(agent, residual)
			if content == "" {
				t.Fatalf("neutralPersonaContent(%q, residual=%v) returned empty string", agent, residual)
			}
			switch {
			case residual:
				if strings.Contains(content, "Senior Architect") {
					t.Fatalf("neutralPersonaContent(%q, residual=true) still contains tone content 'Senior Architect'", agent)
				}
				if !strings.Contains(content, "## Rules") {
					t.Fatalf("neutralPersonaContent(%q, residual=true) missing residual '## Rules'", agent)
				}
			default:
				if content != genericNeutral {
					t.Fatalf("neutralPersonaContent(%q, residual=false) should be byte-identical to generic/persona-neutral.md", agent)
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
	if strings.Count(text, "Senior Architect") != 1 {
		t.Fatalf("expected persona content exactly once after re-injection, got %d occurrences", strings.Count(text, "Senior Architect"))
	}
}

// TestInjectCodexNeutralFreshInstallWrapsPersonaInMarkers ensures the neutral
// persona gets the same marker-bound treatment on Codex.
func TestInjectCodexNeutralFreshInstallWrapsPersonaInMarkers(t *testing.T) {
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
	if !strings.Contains(text, assets.MustRead("generic/persona-neutral.md")) {
		t.Fatal("neutral persona content missing from managed section")
	}
}
