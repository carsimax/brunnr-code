package theme

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/agents/claude"
)

func claudeAdapter() agents.Adapter { return claude.NewAdapter() }

func TestInjectMergesThemeOverlayIntoAdapterSettings(t *testing.T) {
	home := t.TempDir()
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(settings dir) error = %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte("{\n  \"permissions\": {\n    \"allow\": [\"Bash(go test ./...)\"]\n  },\n  \"theme\": \"existing-theme\"\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(settings) error = %v", err)
	}

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

	if len(first.Files) != 1 || first.Files[0] != settingsPath {
		t.Fatalf("files = %#v, want only %q", first.Files, settingsPath)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile(settings) error = %v", err)
	}
	var root struct {
		Permissions map[string][]string `json:"permissions"`
		Theme       string              `json:"theme"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("Unmarshal(settings) error = %v", err)
	}
	if root.Theme != "brunnr" {
		t.Fatalf("theme = %q, want brunnr", root.Theme)
	}
	if got := root.Permissions["allow"]; len(got) != 1 || got[0] != "Bash(go test ./...)" {
		t.Fatalf("permissions.allow = %#v, want preserved existing permission", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "themes", "brunnr.json")); !os.IsNotExist(err) {
		t.Fatalf("Inject() should not write Claude custom theme file; stat error = %v", err)
	}
}

func TestInjectVisualThemesIsIdempotentForClaude(t *testing.T) {
	home := t.TempDir()

	first, err := InjectVisualThemes(home, claudeAdapter())
	if err != nil {
		t.Fatalf("InjectVisualThemes() first error = %v", err)
	}
	if !first.Changed {
		t.Fatalf("InjectVisualThemes() first changed = false")
	}

	second, err := InjectVisualThemes(home, claudeAdapter())
	if err != nil {
		t.Fatalf("InjectVisualThemes() second error = %v", err)
	}
	if second.Changed {
		t.Fatalf("InjectVisualThemes() second changed = true")
	}

	path := filepath.Join(home, ".claude", "themes", "brunnr.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected Claude theme file %q: %v", path, err)
	}
}

func TestInjectVisualThemesPreservesBrunnrClaudeTheme(t *testing.T) {
	home := t.TempDir()

	result, err := InjectVisualThemes(home, claudeAdapter())
	if err != nil {
		t.Fatalf("InjectVisualThemes() error = %v", err)
	}

	themePath := filepath.Join(home, ".claude", "themes", "brunnr.json")
	if len(result.Files) != 2 || result.Files[0] != themePath {
		t.Fatalf("files = %#v, want Brunnr first at %q", result.Files, themePath)
	}

	data, err := os.ReadFile(themePath)
	if err != nil {
		t.Fatalf("ReadFile(theme) error = %v", err)
	}

	var root struct {
		Name      string            `json:"name"`
		Base      string            `json:"base"`
		Overrides map[string]string `json:"overrides"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("Unmarshal(theme) error = %v", err)
	}

	if root.Name != "brunnr" || root.Base != "dark" {
		t.Fatalf("theme identity = %q/%q, want brunnr/dark", root.Name, root.Base)
	}
	expected := map[string]string{
		"diffAdded":                 "#3F4A2D",
		"diffRemoved":               "#5C3838",
		"diffAddedWord":             "#76946A",
		"diffRemovedWord":           "#C34043",
		"chromeYellow":              "#DCA561",
		"briefLabelYou":             "#DCA561",
		"rainbow_yellow":            "#DCA561",
		"yellow_FOR_SUBAGENTS_ONLY": "#DCA561",
	}
	for key, want := range expected {
		if root.Overrides[key] != want {
			t.Fatalf("override %s = %q, want %q", key, root.Overrides[key], want)
		}
	}
	for _, forbidden := range []string{"markdown", "syntax", "keyword", "string"} {
		if _, ok := root.Overrides[forbidden]; ok {
			t.Fatalf("theme contains forbidden non-Claude theme key %q", forbidden)
		}
	}
}

const (
	brunnrClaudeFixture = `{"name":"brunnr","base":"dark","overrides":{"diffAdded":"#3F4A2D","diffRemoved":"#5C3838","diffAddedWord":"#76946A","diffRemovedWord":"#C34043","chromeYellow":"#DCA561","briefLabelYou":"#DCA561","rainbow_yellow":"#DCA561","yellow_FOR_SUBAGENTS_ONLY":"#DCA561"}}`
	cuteClaudeFixture   = `{"name":"Brunnr Cute","base":"dark","overrides":{"claude":"#F095C8","claudeShimmer":"#FFB1DD","text":"#F6EFF3","inactive":"#A78E9B","subtle":"#76616B","suggestion":"#FFB1DD","permission":"#F095C8","promptBorder":"#F095C8","planMode":"#A9C7EE","autoAccept":"#FF81CC","bashBorder":"#E0C27A","remember":"#E0C27A","success":"#B4E7C7","merged":"#B4E7C7","error":"#FF718F","warning":"#F2B86D","diffAdded":"#1A2420","diffRemoved":"#2D151F","diffAddedWord":"#2D5A45","diffRemovedWord":"#7A2948","userMessageBackground":"#241822","userMessageBackgroundHover":"#342230","selectionBg":"#563040","memoryBackgroundColor":"#1A1218","bashMessageBackgroundColor":"#151316"}}`
)

func TestInjectVisualThemesWritesExpectedAssets(t *testing.T) {
	home, xdg := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	for _, adapter := range []agents.Adapter{claudeAdapter()} {
		settings := adapter.SettingsPath(home)
		if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(settings, []byte(`{"theme":"user-selected"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		first, err := InjectVisualThemes(home, adapter)
		if err != nil || !first.Changed {
			t.Fatalf("first injection = %#v, %v", first, err)
		}
		if second, err := InjectVisualThemes(home, adapter); err != nil || second.Changed {
			t.Fatalf("second injection = %#v, %v", second, err)
		}
		if got, err := os.ReadFile(settings); err != nil || string(got) != `{"theme":"user-selected"}` {
			t.Fatalf("settings changed = %q, %v", got, err)
		}
	}
	for _, tt := range []struct {
		path, fixture string
		value         any
	}{
		{filepath.Join(home, ".claude", "themes", "brunnr.json"), brunnrClaudeFixture, &claudeTheme{}},
		{filepath.Join(home, ".claude", "themes", "brunnr-cute.json"), cuteClaudeFixture, &claudeTheme{}},
	} {
		got, err := os.ReadFile(tt.path)
		if err != nil || !bytes.Equal(got, indentedJSON(t, tt.fixture, tt.value)) {
			t.Fatalf("%s bytes mismatch: %v", tt.path, err)
		}
	}
	cute := brunnrCuteClaudeTheme
	for _, key := range []string{"background", "backgroundFullscreen", "backgroundUser", "backgroundHover", "backgroundSelection", "backgroundMemory", "backgroundBash", "shimmer"} {
		if _, exists := cute.Overrides[key]; exists {
			t.Fatalf("Claude Cute contains invalid override %q", key)
		}
	}
}

func indentedJSON(t *testing.T, fixture string, value any) []byte {
	t.Helper()
	if err := json.Unmarshal([]byte(fixture), value); err != nil {
		t.Fatal(err)
	}
	result, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(result, '\n')
}
