package permissions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/agents/claude"
	"github.com/carsimax/brunnr-code/internal/agents/codex"
)

func claudeAdapter() agents.Adapter { return claude.NewAdapter() }
func codexAdapter() agents.Adapter  { return codex.NewAdapter() }

// codexInjectedLegacyConfig mirrors a config.toml produced by the retired
// example-profile permission profile injection, wrapped in user-authored content.
const codexInjectedLegacyConfig = `model = "gpt-5.5"
approval_policy = "on-request"
default_permissions = "example-profile"

[permissions.example-profile]
description = "Comfortable local development profile with workspace writes, network access, and read-only access to Git and Nix/Home Manager metadata."

[permissions.example-profile.network]
enabled = true

[permissions.example-profile.network.domains]
"*" = "allow"

[permissions.example-profile.filesystem]
glob_scan_max_depth = 6
":minimal" = "read"
"~/.config/git" = "read"
"~/.gitconfig" = "read"
"~/.local/state/nix/profiles/home-manager/home-path" = "read"
"~/.nix-profile" = "read"
"/nix/store" = "read"
":tmpdir" = "write"
":slash_tmp" = "write"

[permissions.example-profile.filesystem.":root"]
"." = "read"

[permissions.example-profile.filesystem.":workspace_roots"]
"." = "write"
".git/**" = "write"
"**/.env" = "deny"
"**/*.pem" = "deny"
"**/*.key" = "deny"

[permissions.example-profile.workspace_roots]
"~" = true

[mcp_servers.engram]
command = "engram"
args = ["mcp", "--tools=agent"]
`

func writeCodexConfig(t *testing.T, home, content string) string {
	t.Helper()
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return configPath
}

func TestInjectAddsEnvToDenyList(t *testing.T) {
	home := t.TempDir()

	if _, err := Inject(home, claudeAdapter()); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings file %q: %v", settingsPath, err)
	}

	var settings map[string]any
	if err := json.Unmarshal(content, &settings); err != nil {
		t.Fatalf("unmarshal settings json: %v", err)
	}

	permissionsNode, ok := settings["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("permissions node missing or invalid: %#v", settings["permissions"])
	}

	denyList, ok := permissionsNode["deny"].([]any)
	if !ok {
		t.Fatalf("deny list missing or invalid: %#v", permissionsNode["deny"])
	}

	for _, entry := range denyList {
		if value, ok := entry.(string); ok && value == "Read(.env)" {
			return
		}
	}

	t.Fatalf("deny list missing explicit .env rule: %#v", denyList)
}

func TestInjectClaudeCodeUsesBypassPermissions(t *testing.T) {
	home := t.TempDir()

	if _, err := Inject(home, claudeAdapter()); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(content, &settings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	perms, ok := settings["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("permissions node missing")
	}

	mode, ok := perms["defaultMode"].(string)
	if !ok || mode != "bypassPermissions" {
		t.Fatalf("expected defaultMode=bypassPermissions, got %q", mode)
	}
}

// TestInjectCodexNeverWritesConfig pins the decision that brunnr-code writes
// nothing to Codex's permissions configuration — neither a profile nor the
// legacy migration that used to strip one. Codex refuses to load a config that
// defines a [permissions.*] profile without default_permissions, so a cleanup
// that removed the pointer while a profile survived stopped Codex from starting
// at all (#1794). Whoever still carries an old example-profile profile keeps it.
//
// Byte equality alone is not the assertion: an atomic rewrite that happens to
// produce identical bytes is still a write. Every fixture is backdated so the
// modification time proves the file was left alone.
func TestInjectCodexNeverWritesConfig(t *testing.T) {
	backdated := time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC)

	tests := []struct {
		name    string
		content string
	}{
		{name: "user reported blocker", content: "approval_policy = \"on-request\"\ndefault_permissions = \"example-profile\"\n\n[permissions.example-profile]\nnetwork.enabled = true\n"},
		{name: "fully injected legacy profile", content: codexInjectedLegacyConfig},
		{name: "residual generated profile", content: "model = \"gpt-5.5\"\n\n[permissions.example-profile]\n\n[permissions.example-profile.workspace_roots]\n"},
		{name: "user owned example-profile content", content: "approval_policy = \"never\"\ndefault_permissions = \"example-profile\"\n\n[permissions.custom]\ndescription = \"user profile\"\n\n[permissions.example-profile] # user-owned\nuser_note = \"keep\"\n"},
		{name: "quoted example-profile forms", content: "permissions.\"example-profile\".workspace_roots.\"~\" = true\n"},
		{name: "dotted user value", content: "default_permissions = \"example-profile\"\npermissions.example-profile.custom_flag = true\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			configPath := writeCodexConfig(t, home, tt.content)
			if err := os.Chtimes(configPath, backdated, backdated); err != nil {
				t.Fatalf("Chtimes() error = %v", err)
			}

			result, err := Inject(home, codexAdapter())
			if err != nil {
				t.Fatalf("Inject() error = %v", err)
			}
			if result.Changed {
				t.Error("Inject(codex) changed = true, want false")
			}
			if len(result.Files) != 0 {
				t.Errorf("Inject(codex) files = %v, want none", result.Files)
			}

			content, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatalf("read config.toml: %v", err)
			}
			if string(content) != tt.content {
				t.Errorf("config.toml = %q, want byte-identical %q", content, tt.content)
			}

			info, err := os.Stat(configPath)
			if err != nil {
				t.Fatalf("Stat(config.toml) error = %v", err)
			}
			if !info.ModTime().UTC().Equal(backdated) {
				t.Errorf("config.toml mtime = %v, want %v — the file was written, not left alone", info.ModTime().UTC(), backdated)
			}
		})
	}
}

func TestInjectCodexMissingConfigDoesNothing(t *testing.T) {
	home := t.TempDir()

	result, err := Inject(home, codexAdapter())
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	if result.Changed {
		t.Fatal("Inject() changed = true, want false for missing config")
	}
	if len(result.Files) != 0 {
		t.Fatalf("Inject() files = %v, want none for missing config", result.Files)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex")); !os.IsNotExist(err) {
		t.Fatalf("Inject() created ~/.codex (stat err = %v), want no file creation", err)
	}
}

func TestTargetPathCodexHasNoInjectionTarget(t *testing.T) {
	home := t.TempDir()
	if got := TargetPath(home, codexAdapter()); got != "" {
		t.Fatalf("TargetPath(codex) = %q, want empty (Codex relies on built-in defaults)", got)
	}
}

// TestInjectClaudeCodeSensitivePathsDenied verifies that the default sensitive-path
// deny list is present in the Claude Code permissions block.
func TestInjectClaudeCodeSensitivePathsDenied(t *testing.T) {
	sensitivePatterns := []string{
		"Read(.ssh/*)",
		"Edit(.ssh/*)",
		"Read(.credentials/*)",
		"Edit(.credentials/*)",
		"Read(Library/Keychains/*)",
		"Edit(Library/Keychains/*)",
		"Read(.aws/credentials)",
		"Edit(.aws/credentials)",
		"Read(.config/gh/hosts.yml)",
		"Edit(.config/gh/hosts.yml)",
		"Read(**/*.pem)",
		"Edit(**/*.pem)",
		"Read(**/*.key)",
		"Edit(**/*.key)",
		"Read(**/secrets/*)",
		"Edit(**/secrets/*)",
	}

	home := t.TempDir()
	if _, err := Inject(home, claudeAdapter()); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings file %q: %v", settingsPath, err)
	}

	var settings map[string]any
	if err := json.Unmarshal(content, &settings); err != nil {
		t.Fatalf("unmarshal settings json: %v", err)
	}

	permissionsNode, ok := settings["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("permissions node missing or invalid: %#v", settings["permissions"])
	}

	denyList, ok := permissionsNode["deny"].([]any)
	if !ok {
		t.Fatalf("deny list missing or invalid: %#v", permissionsNode["deny"])
	}

	denySet := make(map[string]bool, len(denyList))
	for _, entry := range denyList {
		if v, ok := entry.(string); ok {
			denySet[v] = true
		}
	}

	for _, pattern := range sensitivePatterns {
		t.Run(pattern, func(t *testing.T) {
			if !denySet[pattern] {
				t.Errorf("deny list missing pattern %q; got: %v", pattern, denyList)
			}
		})
	}
}

// TestInjectClaudeCodeDefaultDenyRulesApplied ensures that the default deny
// rules (including sensitive paths) are written into settings.json even when
// a pre-existing permissions block is already present with other top-level keys.
func TestInjectClaudeCodeDefaultDenyRulesApplied(t *testing.T) {
	home := t.TempDir()
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	// Pre-existing settings with a sibling key under permissions (not deny).
	existing := `{
  "permissions": {
    "defaultMode": "default"
  }
}`
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := Inject(home, claudeAdapter()); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(content, &settings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	perms, ok := settings["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("permissions node missing")
	}

	denyList, ok := perms["deny"].([]any)
	if !ok {
		t.Fatalf("deny list missing")
	}

	denySet := make(map[string]bool, len(denyList))
	for _, entry := range denyList {
		if v, ok := entry.(string); ok {
			denySet[v] = true
		}
	}

	// Sensitive-path rules must be present after overlay application.
	for _, rule := range []string{"Read(.ssh/*)", "Read(**/*.pem)", "Read(**/*.key)"} {
		if !denySet[rule] {
			t.Errorf("default deny rule %q was not present; got: %v", rule, denyList)
		}
	}

	// The overlay wins for defaultMode because arrays replace but maps deep-merge.
	mode, _ := perms["defaultMode"].(string)
	if mode != "bypassPermissions" {
		t.Errorf("expected defaultMode=bypassPermissions after overlay, got %q", mode)
	}
}

func TestInjectClaudeCodeRefusesMalformedSettings(t *testing.T) {
	home := t.TempDir()
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	// Missing comma: the user's hooks and env must survive the failed merge.
	malformed := `{"hooks": {"Stop": []} "env": {"A": "1"}}`
	if err := os.WriteFile(settingsPath, []byte(malformed), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := Inject(home, claudeAdapter()); err == nil {
		t.Fatalf("Inject() error = nil; want refusal for malformed settings.json")
	}

	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}
	if string(content) != malformed {
		t.Fatalf("settings.json was rewritten:\n%s", content)
	}
}

func TestInjectClaudeCodePreservesSettingsComments(t *testing.T) {
	home := t.TempDir()
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	existing := "{\n  // keep me\n  \"env\": {\"A\": \"1\"}\n}\n"
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := Inject(home, claudeAdapter()); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}
	if !strings.Contains(string(content), "// keep me") {
		t.Fatalf("comment dropped from settings.json:\n%s", content)
	}
	if !strings.Contains(string(content), `"permissions"`) {
		t.Fatalf("permissions not written:\n%s", content)
	}
}
