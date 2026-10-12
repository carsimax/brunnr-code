package persona

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gentleman-programming/gentle-ai/v4/internal/agents"
	"github.com/gentleman-programming/gentle-ai/v4/internal/assets"
	"github.com/gentleman-programming/gentle-ai/v4/internal/components/filemerge"
	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
)

type InjectionResult struct {
	Changed bool
	Files   []string
}

// outputStyleOverlayJSON is the settings.json overlay to enable the selected
// managed Claude Code output style.
func outputStyleOverlayJSON(name string) []byte {
	return []byte(fmt.Sprintf("{\n  \"outputStyle\": %q\n}\n", name))
}

// Inject performs a full persona injection: the marker-bound markdown block
// and the Claude Code output-style overlay. Used by `gentle-ai install`.
func Inject(homeDir string, adapter agents.Adapter, persona model.PersonaID) (InjectionResult, error) {
	return injectInternal(homeDir, adapter, persona, false)
}

// InjectForSync regenerates the persona assets that `gentle-ai sync` is
// allowed to touch. It writes:
//   - The marker-bound persona block in the agent's prompt file (markdown).
//   - The Gentleman output-style file + outputStyle settings overlay (Claude
//     Code only — no conflict with other components).
func InjectForSync(homeDir string, adapter agents.Adapter, persona model.PersonaID) (InjectionResult, error) {
	return injectInternal(homeDir, adapter, persona, true)
}

// syncManaged is the internal flag previously called `markdownOnly`.
func injectInternal(homeDir string, adapter agents.Adapter, persona model.PersonaID, syncManaged bool) (InjectionResult, error) {
	persona = canonicalPersona(persona)
	if !adapter.SupportsSystemPrompt() {
		return InjectionResult{}, nil
	}
	if err := validateOpenClawWorkspacePath(homeDir, adapter); err != nil {
		return InjectionResult{}, err
	}

	// Custom persona does nothing — user keeps their own config.
	if persona == model.PersonaCustom {
		return InjectionResult{}, nil
	}
	files := make([]string, 0, 3)
	changed := false

	content := personaContent(adapter.Agent(), persona, residualChannel(adapter))
	if content == "" {
		return InjectionResult{}, nil
	}

	// 1. Inject persona content based on system prompt strategy.
	if adapter.Agent() == model.AgentOpenClaw {
		return injectOpenClawSoulPersona(homeDir, content)
	}

	switch adapter.SystemPromptStrategy() {
	case model.StrategyMarkdownSections:
		promptPath := adapter.SystemPromptFile(homeDir)
		existing, err := readFileOrEmpty(promptPath)
		if err != nil {
			return InjectionResult{}, err
		}

		// Auto-heal: strip any legacy free-text Gentleman persona block that was
		// written before the marker-based injection system existed. This is safe
		// for StrategyMarkdownSections because InjectMarkdownSection preserves
		// all existing marker sections — only the unmarked free-text preamble is
		// removed, and StripLegacyPersonaBlock requires ALL three fingerprints
		// to be present in the pre-marker zone before stripping.
		healed := filemerge.StripLegacyPersonaBlock(existing)

		// Also strip legacy Agent Teams Lite block (standalone ATL installer leftover).
		healed = filemerge.StripLegacyATLBlock(healed)

		updated := filemerge.InjectMarkdownSection(healed, "persona", content)

		writeResult, err := filemerge.WriteFileAtomic(promptPath, []byte(updated), 0o644)
		if err != nil {
			return InjectionResult{}, err
		}
		changed = changed || writeResult.Changed
		files = append(files, promptPath)

	case model.StrategyFileReplace:
		promptPath := adapter.SystemPromptFile(homeDir)

		// Codex shares ~/.codex/AGENTS.md with user-authored content and with
		// other managed sections (engram, SDD) that are appended after persona.
		// The persona must therefore live inside a <!-- gentle-ai:persona -->
		// marker section instead of owning the whole file, so install, sync and
		// uninstall can replace or remove exactly the managed persona content
		// (issue #981).
		if adapter.Agent() == model.AgentCodex {
			existing, err := readFileOrEmpty(promptPath)
			if err != nil {
				return InjectionResult{}, err
			}

			healed := stripExactLegacyPersonaAsset(existing)
			healed = filemerge.StripLegacyATLBlock(healed)
			updated := injectPersonaBeforeManagedSections(healed, content)

			writeResult, err := filemerge.WriteFileAtomic(promptPath, []byte(updated), 0o644)
			if err != nil {
				return InjectionResult{}, err
			}
			changed = changed || writeResult.Changed
			files = append(files, promptPath)
			break
		}

		// For non-Gentleman personas (e.g. neutral), the content is just a short
		// one-liner. Writing ONLY that content would destroy any SDD/engram
		// sections that are injected later in the pipeline. Instead, we write the
		// persona content as the base and let subsequent inject steps (SDD, engram)
		// append their sections. For Gentleman, the content is the full persona
		// asset which is safe to write as-is.
		//
		// If the file already exists and has managed sections (SDD, engram), we
		// must preserve them — replace only the persona portion at the top.
		existing, readErr := readFileOrEmpty(promptPath)
		if readErr != nil {
			return InjectionResult{}, readErr
		}

		if preserved, ok := preserveManagedSections(existing, content, persona); ok {
			writeResult, err := filemerge.WriteFileAtomic(promptPath, []byte(preserved), 0o644)
			if err != nil {
				return InjectionResult{}, err
			}
			changed = changed || writeResult.Changed
			files = append(files, promptPath)
			break
		}

		writeResult, err := filemerge.WriteFileAtomic(promptPath, []byte(content), 0o644)
		if err != nil {
			return InjectionResult{}, err
		}
		changed = changed || writeResult.Changed
		files = append(files, promptPath)

	case model.StrategyInstructionsFile:
		promptPath := adapter.SystemPromptFile(homeDir)

		// Auto-heal: remove any stale Gentleman persona content left at the
		// old VSCode path (~/.github/copilot-instructions.md) that was written
		// by an older installer version.  VS Code still reads that path for
		// global instructions, so the two files would conflict.
		if cleaned, cleanErr := cleanLegacyVSCodePersona(homeDir); cleanErr == nil && cleaned {
			changed = true
		}

		// For non-Gentleman personas, preserve managed sections (same logic
		// as StrategyFileReplace above).
		existing, readErr := readFileOrEmpty(promptPath)
		if readErr != nil {
			return InjectionResult{}, readErr
		}

		if preserved, ok := preserveManagedSections(existing, wrapInstructionsFile(content), persona); ok {
			writeResult, err := filemerge.WriteFileAtomic(promptPath, []byte(preserved), 0o644)
			if err != nil {
				return InjectionResult{}, err
			}
			changed = changed || writeResult.Changed
			files = append(files, promptPath)
			break
		}

		// Write the new instructions file (with YAML frontmatter) to the current path.
		// WriteFileAtomic compares bytes, so it is naturally idempotent: it rewrites
		// whenever the on-disk content differs from instructionsContent, which covers
		// the case where an older install wrote persona content without frontmatter.
		instructionsContent := wrapInstructionsFile(content)
		writeResult, err := filemerge.WriteFileAtomic(promptPath, []byte(instructionsContent), 0o644)
		if err != nil {
			return InjectionResult{}, err
		}
		changed = changed || writeResult.Changed
		files = append(files, promptPath)

	case model.StrategySteeringFile:
		promptPath := adapter.SystemPromptFile(homeDir)

		existing, readErr := readFileOrEmpty(promptPath)
		if readErr != nil {
			return InjectionResult{}, readErr
		}

		var steeringContent string
		if preserved, ok := preserveManagedSections(existing, wrapSteeringFile(content), persona); ok {
			steeringContent = preserved
		} else {
			steeringContent = wrapSteeringFile(content)
		}

		if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
			return InjectionResult{}, err
		}
		writeResult, err := filemerge.WriteFileAtomic(promptPath, []byte(steeringContent), 0o644)
		if err != nil {
			return InjectionResult{}, err
		}
		changed = changed || writeResult.Changed
		files = append(files, promptPath)

	case model.StrategyAppendToFile:
		promptPath := adapter.SystemPromptFile(homeDir)

		existing, err := readFileOrEmpty(promptPath)
		if err != nil {
			return InjectionResult{}, err
		}

		// Append-style agents still need marker-bound persona sections so sync can
		// replace managed content without duplicating it or disturbing user-authored
		// rules in the shared prompt file.
		healed := filemerge.StripLegacyPersonaBlock(existing)
		healed = filemerge.StripLegacyATLBlock(healed)
		updated := filemerge.InjectMarkdownSection(healed, "persona", content)

		writeResult, err := filemerge.WriteFileAtomic(promptPath, []byte(updated), 0o644)
		if err != nil {
			return InjectionResult{}, err
		}
		changed = changed || writeResult.Changed
		files = append(files, promptPath)

	}

	// 2. Write the selected managed output style, then remove only the retired
	// resources declared by the persona plan. The backup/verify callers derive
	// their paths from this same plan.
	plan := ResourcePlanFor(persona)
	style, selectedStyle := plan.OutputStyle()
	if adapter.Agent() != model.AgentOpenClaw && adapter.SupportsOutputStyles() {
		stylePaths := plan.OutputStylePaths(adapter.OutputStyleDir(homeDir))
		if selectedStyle && stylePaths.Write != "" {
			styleResult, err := filemerge.WriteFileAtomic(stylePaths.Write, []byte(assets.MustRead(style.AssetPath)), 0o644)
			if err != nil {
				return InjectionResult{}, err
			}
			changed = changed || styleResult.Changed
			files = append(files, stylePaths.Write)
		}

		settingsPath := adapter.SettingsPath(homeDir)
		if selectedStyle && settingsPath != "" {
			var settingsResult filemerge.WriteResult
			var err error
			if persona == model.PersonaNeutral {
				settingsResult, err = mergeJSONFileToleratingMalformed(settingsPath, outputStyleOverlayJSON(style.Name))
			} else {
				settingsResult, err = mergeJSONFile(settingsPath, outputStyleOverlayJSON(style.Name))
			}
			if err != nil {
				return InjectionResult{}, err
			}
			changed = changed || settingsResult.Changed
			files = append(files, settingsPath)
		} else if settingsPath != "" {
			removed, err := removeJSONKeyIfValue(settingsPath, "outputStyle", "Gentleman")
			if err != nil {
				return InjectionResult{}, fmt.Errorf("clean outputStyle from settings: %w", err)
			}
			if removed {
				changed = true
				files = append(files, settingsPath)
			}
		}

		for _, retiredPath := range stylePaths.Remove {
			styleRemoved, err := removeFileAtomic(retiredPath)
			if err != nil {
				return InjectionResult{}, fmt.Errorf("remove retired output style: %w", err)
			}
			if styleRemoved {
				changed = true
				files = append(files, retiredPath)
			}
		}
	}

	return InjectionResult{Changed: changed, Files: files}, nil
}

func validateOpenClawWorkspacePath(workspaceDir string, adapter agents.Adapter) error {
	if adapter.Agent() == model.AgentOpenClaw && strings.TrimSpace(workspaceDir) == "" {
		return fmt.Errorf("openclaw workspace path is required for workspace-first injection")
	}
	return nil
}

func injectOpenClawSoulPersona(workspaceDir, content string) (InjectionResult, error) {
	soulPath := filepath.Join(workspaceDir, "SOUL.md")
	existing, err := readFileOrEmpty(soulPath)
	if err != nil {
		return InjectionResult{}, err
	}

	healed := filemerge.StripLegacyPersonaBlock(existing)
	healed = filemerge.StripLegacyATLBlock(healed)
	updated := filemerge.InjectMarkdownSection(healed, "persona", content)

	writeResult, err := filemerge.WriteFileAtomic(soulPath, []byte(updated), 0o644)
	if err != nil {
		return InjectionResult{}, err
	}

	return InjectionResult{Changed: writeResult.Changed, Files: []string{soulPath}}, nil
}

// shouldStripManagedLegacyPersona returns true ONLY when the existing file
// already contains a <!-- gentle-ai:persona --> section. That is the strongest
// evidence that the pre-marker persona content is stale legacy text written by
// an older installer, not user-authored content that happens to share headings.
//
// We intentionally do NOT trigger on ATL markers, engram markers, sdd markers,
// or any other managed marker — their presence does not prove that the
// pre-marker content is installer-owned.
// isExactLegacyPersonaAsset returns true when the file content is an exact
// match of one of the known persona assets (gentleman or neutral). This handles
// the case where an old installer wrote the asset as the entire file with no
// markers — we can safely replace it because there is zero user content.
func isExactLegacyPersonaAsset(existing string) bool {
	trimmed := strings.TrimSpace(existing)
	if trimmed == "" {
		return false
	}
	for _, assetPath := range []string{
		"generic/persona-gentleman.md",
		"generic/persona-neutral.md",
	} {
		asset := strings.TrimSpace(assets.MustRead(assetPath))
		if trimmed == asset {
			return true
		}
	}
	return false
}

// stripExactLegacyPersonaAsset removes installer-owned legacy persona content
// from existing when it is byte-for-byte one of the known legacy persona
// assets. Two safe cases are handled:
//
//  1. The whole file is the legacy asset (no markers at all) — the old
//     installer owned the entire file, so it can be replaced wholesale.
//  2. The pre-marker zone (content before the first <!-- gentle-ai: -->
//     marker) is exactly the legacy asset, followed by managed sections that
//     later install steps appended (engram, SDD). Only the installer-owned
//     zone is removed; every marker section is preserved. This migrates the
//     markerless persona prose written by installers up to v1.43.2
//     (issue #981).
//
// Anything else — including user content that merely resembles the persona —
// is returned unchanged.
func stripExactLegacyPersonaAsset(existing string) string {
	if strings.TrimSpace(existing) == "" {
		return existing
	}
	firstMarkerIdx := strings.Index(existing, "<!-- gentle-ai:")
	if firstMarkerIdx < 0 {
		if isExactLegacyPersonaAsset(existing) {
			return ""
		}
		return existing
	}
	if isExactLegacyPersonaAsset(existing[:firstMarkerIdx]) {
		return existing[firstMarkerIdx:]
	}
	return existing
}

func injectPersonaBeforeManagedSections(existing, content string) string {
	if strings.Contains(existing, "<!-- gentle-ai:persona -->") {
		return filemerge.InjectMarkdownSection(existing, "persona", content)
	}

	firstMarkerIdx := strings.Index(existing, "<!-- gentle-ai:")
	if firstMarkerIdx < 0 {
		return filemerge.InjectMarkdownSection(existing, "persona", content)
	}

	section := filemerge.InjectMarkdownSection("", "persona", content)
	before := strings.TrimRight(existing[:firstMarkerIdx], "\r\n")
	after := strings.TrimLeft(existing[firstMarkerIdx:], "\r\n")
	if before == "" {
		return section + "\n" + after
	}
	return before + "\n\n" + section + "\n" + after
}

// isGentlemanConversationPersona reports whether the persona keeps the voseo
// conversation tone. The gentleman-neutral-artifacts legacy alias is remapped
// to neutral (cli.normalizePersona in internal/cli/validate.go, mirrored at
// the injectInternal entry) and is intentionally NOT gentleman here.
func isGentlemanConversationPersona(persona model.PersonaID) bool {
	return persona == model.PersonaGentleman
}

// residualChannel reports whether the adapter already delivers tone/language/
// philosophy content through an active output-style channel that loads every
// session, making the system-prompt persona section redundant for that
// content. Claude Code is gated on SupportsOutputStyles().
func residualChannel(adapter agents.Adapter) bool {
	return adapter.SupportsOutputStyles()
}

// personaContent returns the persona asset for the given agent and persona.
func personaContent(agent model.AgentID, persona model.PersonaID, residualContentAvailable bool) string {
	persona = canonicalPersona(persona)
	switch persona {
	case model.PersonaNeutral:
		return neutralPersonaContent(agent, residualContentAvailable)
	case model.PersonaCustom:
		return ""
	default:
		return gentlemanPersonaContent(agent)
	}
}

func neutralPersonaContent(agent model.AgentID, residualContentAvailable bool) string {
	if residualContentAvailable && agent == model.AgentClaudeCode {
		return assets.MustRead("claude/persona-neutral-residual.md")
	}
	return assets.MustRead("generic/persona-neutral.md")
}

func gentlemanPersonaContent(agent model.AgentID) string {
	// Claude Gentleman assets are already residual/slim in place; unlike
	// Neutral, there is no separate residual variant to choose here.
	switch agent {
	case model.AgentClaudeCode:
		return assets.MustRead("claude/persona-gentleman.md")
	default:
		return assets.MustRead("generic/persona-gentleman.md")
	}
}

// mergeJSONFile merges overlay into the settings at path with the shared
// merge, which normalizes the document to strict JSON.
func mergeJSONFile(path string, overlay []byte) (filemerge.WriteResult, error) {
	baseJSON, err := osReadFile(path)
	if err != nil {
		return filemerge.WriteResult{}, err
	}
	merged, err := filemerge.MergeJSONObjectsForPath(path, baseJSON, overlay)
	if err != nil {
		return filemerge.WriteResult{}, err
	}
	return filemerge.WriteFileAtomic(path, merged, filemerge.ExistingFileMode(path, 0o644))
}

func mergeJSONFileToleratingMalformed(path string, overlay []byte) (filemerge.WriteResult, error) {
	result, err := mergeJSONFile(path, overlay)
	if err == nil {
		return result, nil
	}
	if strings.Contains(err.Error(), "invalid character") || strings.Contains(err.Error(), "unexpected end of JSON") {
		return filemerge.WriteResult{}, nil
	}
	return filemerge.WriteResult{}, err
}

var osReadFile = func(path string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read json file %q: %w", path, err)
	}

	return content, nil
}

// preserveManagedSections checks whether the existing file content has
// gentle-ai managed sections (SDD orchestrator, engram protocol, etc.) and
// returns new content that preserves those sections while replacing only the
// persona text before them. Returns ("", false) when no preservation is needed
// (empty file, Gentleman persona, or no managed markers found).
func preserveManagedSections(existing, newPersona string, persona model.PersonaID) (string, bool) {
	if existing == "" || isGentlemanConversationPersona(persona) {
		return "", false
	}

	idx := strings.Index(existing, "<!-- gentle-ai:")
	if idx < 0 {
		return "", false
	}

	managedSuffix := existing[idx:]
	updated := newPersona
	if !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	if idx > 0 {
		// There was persona content before the markers — add a blank line separator.
		updated += "\n"
	}
	updated += managedSuffix

	return updated, true
}

func readFileOrEmpty(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read file %q: %w", path, err)
	}
	return string(data), nil
}

func wrapInstructionsFile(content string) string {
	frontmatter := "---\n" +
		"name: Gentle AI Persona\n" +
		"description: Teaching-oriented persona with ODD orchestration and Engram protocol\n" +
		"applyTo: \"**\"\n" +
		"---\n\n"

	return frontmatter + content
}

func wrapSteeringFile(content string) string {
	frontmatter := "---\n" +
		"inclusion: always\n" +
		"---\n\n"

	return frontmatter + content
}

// isLegacyUnwrappedPersona reports whether content is a Gentleman persona
// file written by an older installer version without YAML frontmatter.
// Requires ALL fingerprints to match (not just one) to reduce false positives.
// This is only used for legacy path cleanup (e.g. ~/.github/copilot-instructions.md)
// where the file is at a known old installer path — the combination of legacy
// path + all fingerprints is strong enough evidence of installer ownership.
func isLegacyUnwrappedPersona(content string) bool {
	if strings.HasPrefix(content, "---\n") {
		// Already has YAML frontmatter — not a legacy file.
		return false
	}
	// Require ALL fingerprints — a user is unlikely to have all of these
	// exact strings in a hand-written file at the old legacy path.
	personaFingerprints := []string{
		"## Personality",
		"Senior Architect",
	}
	for _, fp := range personaFingerprints {
		if !strings.Contains(content, fp) {
			return false
		}
	}
	return true
}

// legacyVSCodePersonaPaths returns the old VS Code persona file paths that may
// contain stale Gentleman persona content from older installer versions.
// These paths are no longer written by the current installer but may still
// be read by VS Code, causing conflicting instructions.
func legacyVSCodePersonaPaths(homeDir string) []string {
	return []string{
		// v1 path: wrote raw persona to ~/.github/copilot-instructions.md
		filepath.Join(homeDir, ".github", "copilot-instructions.md"),
	}
}

// removeFileAtomic removes path if it exists. Returns true when the file was
// present and successfully deleted, false when it did not exist. Any other
// OS-level error is returned as-is.
func removeFileAtomic(path string) (bool, error) {
	err := os.Remove(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// removeJSONKeyIfValue reads the JSON object at path, removes the top-level key
// only when its current string value equals wantValue, and writes the result
// back atomically. Returns true when the key was actually removed.
// If the file does not exist, the key is absent, or the value differs, it is
// a no-op and returns false.
func removeJSONKeyIfValue(path, key, wantValue string) (bool, error) {
	raw, err := osReadFile(path)
	if err != nil {
		return false, err
	}
	if len(raw) == 0 {
		return false, nil
	}

	root := map[string]any{}
	if err := json.Unmarshal(raw, &root); err != nil {
		// Malformed settings — leave untouched to avoid data loss.
		return false, nil
	}

	current, ok := root[key]
	if !ok {
		return false, nil
	}
	if current != wantValue {
		// User has a different value — do not touch it.
		return false, nil
	}

	delete(root, key)

	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal settings after cleanup: %w", err)
	}
	encoded = append(encoded, '\n')

	if _, err := filemerge.WriteFileAtomic(path, encoded, filemerge.ExistingFileMode(path, 0o644)); err != nil {
		return false, err
	}
	return true, nil
}

// cleanLegacyVSCodePersona removes Gentleman persona content from any old VS Code
// persona file paths that are no longer written by the current installer.
// Only files that contain clear Gentleman persona fingerprints are removed —
// files with user-written content are left untouched.
// Returns true if at least one file was cleaned.
func cleanLegacyVSCodePersona(homeDir string) (bool, error) {
	cleaned := false
	for _, oldPath := range legacyVSCodePersonaPaths(homeDir) {
		data, err := os.ReadFile(oldPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return cleaned, fmt.Errorf("read legacy vscode persona %q: %w", oldPath, err)
		}

		if !isLegacyUnwrappedPersona(string(data)) {
			// File exists but doesn't look like a Gentleman persona — leave it alone.
			continue
		}

		if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
			return cleaned, fmt.Errorf("remove legacy vscode persona %q: %w", oldPath, err)
		}
		cleaned = true
	}
	return cleaned, nil
}
