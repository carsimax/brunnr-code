package persona

import (
	"fmt"
	"os"
	"strings"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/assets"
	"github.com/carsimax/brunnr-code/internal/components/filemerge"
	"github.com/carsimax/brunnr-code/internal/model"
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

// Inject performs a full Neutral persona injection: the marker-bound markdown
// block and the Claude Code output-style overlay. Used by `gentle-ai install`.
func Inject(homeDir string, adapter agents.Adapter) (InjectionResult, error) {
	return injectInternal(homeDir, adapter)
}

// InjectForSync regenerates the persona assets that `gentle-ai sync` is
// allowed to touch. It writes:
//   - The marker-bound persona block in the agent's prompt file (markdown).
//   - The Neutral output-style file + outputStyle settings overlay (Claude
//     Code only — no conflict with other components).
func InjectForSync(homeDir string, adapter agents.Adapter) (InjectionResult, error) {
	return injectInternal(homeDir, adapter)
}

func injectInternal(homeDir string, adapter agents.Adapter) (InjectionResult, error) {
	if !adapter.SupportsSystemPrompt() {
		return InjectionResult{}, nil
	}
	files := make([]string, 0, 3)
	changed := false

	content := neutralPersonaContent(adapter.Agent(), residualChannel(adapter))

	// 1. Inject persona content based on system prompt strategy.
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

		// Writing ONLY the persona content would destroy any SDD/engram
		// sections that are injected later in the pipeline. Instead, we write the
		// persona content as the base and let subsequent inject steps (SDD, engram)
		// append their sections.
		//
		// If the file already exists and has managed sections (SDD, engram), we
		// must preserve them — replace only the persona portion at the top.
		existing, readErr := readFileOrEmpty(promptPath)
		if readErr != nil {
			return InjectionResult{}, readErr
		}

		if preserved, ok := preserveManagedSections(existing, content); ok {
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
	}

	// 2. Write the managed output style and enable it in settings.
	if adapter.SupportsOutputStyles() {
		if stylePath := OutputStylePath(adapter.OutputStyleDir(homeDir)); stylePath != "" {
			styleResult, err := filemerge.WriteFileAtomic(stylePath, []byte(assets.MustRead(neutralOutputStyle.AssetPath)), 0o644)
			if err != nil {
				return InjectionResult{}, err
			}
			changed = changed || styleResult.Changed
			files = append(files, stylePath)

			if settingsPath := adapter.SettingsPath(homeDir); settingsPath != "" {
				settingsResult, err := mergeJSONFileToleratingMalformed(settingsPath, outputStyleOverlayJSON(neutralOutputStyle.Name))
				if err != nil {
					return InjectionResult{}, err
				}
				changed = changed || settingsResult.Changed
				files = append(files, settingsPath)
			}
		}
	}

	return InjectionResult{Changed: changed, Files: files}, nil
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
// match of the Neutral persona asset. This handles
// the case where an old installer wrote the asset as the entire file with no
// markers — we can safely replace it because there is zero user content.
func isExactLegacyPersonaAsset(existing string) bool {
	trimmed := strings.TrimSpace(existing)
	if trimmed == "" {
		return false
	}
	return trimmed == strings.TrimSpace(assets.MustRead("generic/persona-neutral.md"))
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

// residualChannel reports whether the adapter already delivers tone/language/
// philosophy content through an active output-style channel that loads every
// session, making the system-prompt persona section redundant for that
// content. Claude Code is gated on SupportsOutputStyles().
func residualChannel(adapter agents.Adapter) bool {
	return adapter.SupportsOutputStyles()
}

func neutralPersonaContent(agent model.AgentID, residualContentAvailable bool) string {
	if residualContentAvailable && agent == model.AgentClaudeCode {
		return assets.MustRead("claude/persona-neutral-residual.md")
	}
	return assets.MustRead("generic/persona-neutral.md")
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
// (empty file or no managed markers found).
func preserveManagedSections(existing, newPersona string) (string, bool) {
	if existing == "" {
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
