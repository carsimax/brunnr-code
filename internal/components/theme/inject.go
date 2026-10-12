package theme

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/components/filemerge"
	"github.com/carsimax/brunnr-code/internal/model"
)

type InjectionResult struct {
	Changed bool
	Files   []string
}

var themeOverlayJSON = []byte("{\n  \"theme\": \"brunnr\"\n}\n")

type claudeTheme struct {
	Name      string            `json:"name"`
	Base      string            `json:"base"`
	Overrides map[string]string `json:"overrides"`
}

func palette(pairs ...string) map[string]string {
	colors := make(map[string]string, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		colors[pairs[i]] = pairs[i+1]
	}
	return colors
}

var brunnrClaudeTheme = claudeTheme{
	Name: "brunnr", Base: "dark",
	Overrides: palette(
		"diffAdded", "#3F4A2D", "diffRemoved", "#5C3838", "diffAddedWord", "#76946A", "diffRemovedWord", "#C34043",
		"chromeYellow", "#DCA561", "briefLabelYou", "#DCA561", "rainbow_yellow", "#DCA561", "yellow_FOR_SUBAGENTS_ONLY", "#DCA561",
	),
}

var brunnrCuteClaudeTheme = claudeTheme{
	Name: "Brunnr Cute", Base: "dark",
	Overrides: palette(
		"claude", "#F095C8", "claudeShimmer", "#FFB1DD", "text", "#F6EFF3", "inactive", "#A78E9B", "subtle", "#76616B", "suggestion", "#FFB1DD",
		"permission", "#F095C8", "promptBorder", "#F095C8", "planMode", "#A9C7EE", "autoAccept", "#FF81CC", "bashBorder", "#E0C27A",
		"remember", "#E0C27A", "success", "#B4E7C7", "merged", "#B4E7C7", "error", "#FF718F", "warning", "#F2B86D",
		"diffAdded", "#1A2420", "diffRemoved", "#2D151F", "diffAddedWord", "#2D5A45", "diffRemovedWord", "#7A2948",
		"userMessageBackground", "#241822", "userMessageBackgroundHover", "#342230", "selectionBg", "#563040", "memoryBackgroundColor", "#1A1218", "bashMessageBackgroundColor", "#151316",
	),
}

func Inject(homeDir string, adapter agents.Adapter) (InjectionResult, error) {
	return injectSettings(agents.JSONSettingsPath(homeDir, adapter))
}

func injectSettings(settingsPath string) (InjectionResult, error) {
	if settingsPath == "" {
		return InjectionResult{}, nil
	}

	writeResult, err := mergeJSONFile(settingsPath, themeOverlayJSON)
	if err != nil {
		return InjectionResult{}, err
	}

	return InjectionResult{Changed: writeResult.Changed, Files: []string{settingsPath}}, nil
}

// InjectVisualThemes writes the managed visual theme assets without selecting one
// in an agent's active settings.
func InjectVisualThemes(homeDir string, adapter agents.Adapter) (InjectionResult, error) {
	paths := VisualThemePaths(homeDir, adapter)
	if len(paths) == 0 {
		return InjectionResult{}, nil
	}

	var values []any
	switch adapter.Agent() {
	case model.AgentClaudeCode:
		values = []any{brunnrClaudeTheme, brunnrCuteClaudeTheme}
	}

	result := InjectionResult{Files: make([]string, 0, len(paths))}
	for i, path := range paths {
		content, err := json.MarshalIndent(values[i], "", "  ")
		if err != nil {
			return InjectionResult{}, fmt.Errorf("marshal visual theme %q: %w", filepath.Base(path), err)
		}
		content = append(content, '\n')
		writeResult, err := filemerge.WriteFileAtomic(path, content, 0o644)
		if err != nil {
			return InjectionResult{}, err
		}
		result.Changed = result.Changed || writeResult.Changed
		result.Files = append(result.Files, path)
	}
	return result, nil
}

// VisualThemePaths returns the installer-owned visual theme assets for an adapter.
func VisualThemePaths(homeDir string, adapter agents.Adapter) []string {
	var root string
	switch adapter.Agent() {
	case model.AgentClaudeCode:
		root = filepath.Join(adapter.GlobalConfigDir(homeDir), "themes")
	default:
		return nil
	}
	return []string{filepath.Join(root, "brunnr.json"), filepath.Join(root, "brunnr-cute.json")}
}

func mergeJSONFile(path string, overlay []byte) (filemerge.WriteResult, error) {
	mode := filemerge.ExistingFileMode(path, 0o644)

	baseJSON, err := osReadFile(path)
	if err != nil {
		return filemerge.WriteResult{}, err
	}

	merged, err := filemerge.MergeJSONObjectsForPath(path, baseJSON, overlay)
	if err != nil {
		return filemerge.WriteResult{}, err
	}

	return filemerge.WriteFileAtomic(path, merged, mode)
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
