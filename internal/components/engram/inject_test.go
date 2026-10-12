package engram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/agents/claude"
	"github.com/carsimax/brunnr-code/internal/agents/codex"
	"github.com/carsimax/brunnr-code/internal/model"
)

func claudeAdapter() agents.Adapter { return claude.NewAdapter() }
func codexAdapter() agents.Adapter  { return codex.NewAdapter() }

func validCodexRuntime(t *testing.T) {
	t.Helper()
	t.Cleanup(codex.SetRuntimeVersionCommandForTest("codex-cli 0.144.0", nil))
}

// assertArgsHaveToolsAgent is a shared helper that validates a JSON file
// contains the MCP "engram" entry with --tools=agent in args.
func assertArgsHaveToolsAgent(t *testing.T, path string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	text := string(content)
	if !strings.Contains(text, `"--tools=agent"`) {
		t.Fatalf("file %q missing --tools=agent in args; got:\n%s", path, text)
	}
}

func TestInjectClaudeWritesUserRegistryPreservingUnrelatedData(t *testing.T) {
	home := t.TempDir()
	mockEngramLookPath(t, "/opt/homebrew/bin/engram", "")
	registryPath := claude.UserConfigPath(home)
	seed := []byte(`{"oauthAccount":{"emailAddress":"user@example.com"},"projects":{"/repo":{"allowedTools":[]}},"mcpServers":{"codegraph":{"command":"codegraph"}}}`)
	if err := os.WriteFile(registryPath, seed, 0o644); err != nil {
		t.Fatalf("WriteFile(user registry) error = %v", err)
	}

	result, err := Inject(home, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	if !result.Changed {
		t.Fatalf("Inject() changed = false")
	}

	registry := readJSONFile(t, registryPath)
	assertNestedString(t, registry, "user@example.com", "oauthAccount", "emailAddress")
	assertNestedString(t, registry, "codegraph", "mcpServers", "codegraph", "command")
	assertNestedString(t, registry, "/opt/homebrew/bin/engram", "mcpServers", "engram", "command")
	assertNestedStrings(t, registry, []string{"mcp", "--tools=agent"}, "mcpServers", "engram", "args")
	if info, statErr := os.Stat(registryPath); statErr != nil {
		t.Fatalf("Stat(user registry) error = %v", statErr)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("user registry mode = %o; want 0600", info.Mode().Perm())
	}
	legacyPath := filepath.Join(home, ".claude", "mcp", "engram.json")
	if _, statErr := os.Stat(legacyPath); !os.IsNotExist(statErr) {
		t.Fatalf("fresh injection must not create legacy config %q; stat error = %v", legacyPath, statErr)
	}
}

func TestInjectClaudeRefusesCorruptUserRegistryWithoutMutation(t *testing.T) {
	home := t.TempDir()
	registryPath := claude.UserConfigPath(home)
	corrupt := []byte("{ not json")
	if err := os.WriteFile(registryPath, corrupt, 0o600); err != nil {
		t.Fatalf("WriteFile(corrupt registry) error = %v", err)
	}

	if _, err := Inject(home, claudeAdapter()); err == nil {
		t.Fatal("Inject() error = nil; want corrupt registry refusal")
	}
	after, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("ReadFile(corrupt registry) error = %v", err)
	}
	if !bytes.Equal(after, corrupt) {
		t.Fatalf("corrupt registry mutated: got %q, want %q", after, corrupt)
	}
}

func TestInjectClaudeWritesProtocolSection(t *testing.T) {
	home := t.TempDir()

	_, err := Inject(home, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	claudeMDPath := filepath.Join(home, ".claude", "CLAUDE.md")
	content, err := os.ReadFile(claudeMDPath)
	if err != nil {
		t.Fatalf("ReadFile(CLAUDE.md) error = %v", err)
	}

	text := string(content)
	if !strings.Contains(text, "<!-- brunnr-code:engram-protocol -->") {
		t.Fatal("CLAUDE.md missing open marker for engram-protocol")
	}
	if !strings.Contains(text, "<!-- /brunnr-code:engram-protocol -->") {
		t.Fatal("CLAUDE.md missing close marker for engram-protocol")
	}
	// Real content check.
	if !strings.Contains(text, "mem_save") {
		t.Fatal("CLAUDE.md missing real engram protocol content (expected 'mem_save')")
	}
	if !strings.Contains(text, "needs_review") {
		t.Fatal("CLAUDE.md missing memory lifecycle stale-context rule (expected 'needs_review')")
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
	registryPath := claude.UserConfigPath(home)
	afterFirst, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("ReadFile(user registry after first injection) error = %v", err)
	}

	second, err := Inject(home, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject() second error = %v", err)
	}
	if second.Changed {
		t.Fatalf("Inject() second changed = true")
	}
	afterSecond, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("ReadFile(user registry after second injection) error = %v", err)
	}
	if !bytes.Equal(afterFirst, afterSecond) {
		t.Fatal("second injection must leave the user registry byte-identical")
	}
}

// ─── Gemini tests ─────────────────────────────────────────────────────────────

func TestStableEngramCommandRecognizesLinuxbrewPath(t *testing.T) {
	SetLookPathForTest(t, "/home/linuxbrew/.linuxbrew/bin/engram", "")

	if !isStableHomebrewEngramPath("/home/linuxbrew/.linuxbrew/bin/engram") {
		t.Fatalf("Linuxbrew stable path must be recognized as a stable Homebrew engram path")
	}
	if got := preferredStableEngramCommand(); got != "/home/linuxbrew/.linuxbrew/bin/engram" {
		t.Fatalf("preferredStableEngramCommand() = %q, want the Linuxbrew stable path", got)
	}

	// Agents resolve the stable command through
	// stableEngramCommandForMergedConfig when no prior config exists — the
	// Linuxbrew stable path must be preserved there too.
	home := t.TempDir()
	got := stableEngramCommandForMergedConfig(filepath.Join(home, "missing", "mcp.json"), model.AgentClaudeCode)
	if got != "/home/linuxbrew/.linuxbrew/bin/engram" {
		t.Fatalf("stableEngramCommandForMergedConfig(claude-code) = %q, want the Linuxbrew stable path", got)
	}
}

// ─── Codex tests ──────────────────────────────────────────────────────────────

func TestInjectCodexWritesTOMLMCP(t *testing.T) {
	validCodexRuntime(t)
	home := t.TempDir()

	result, err := Inject(home, codexAdapter())
	if err != nil {
		t.Fatalf("Inject(codex) error = %v", err)
	}
	if !result.Changed {
		t.Fatalf("Inject(codex) changed = false")
	}

	configPath := filepath.Join(home, ".codex", "config.toml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config.toml) error = %v", err)
	}
	text := string(content)
	if !strings.Contains(text, "[mcp_servers.engram]") {
		t.Fatalf("config.toml missing [mcp_servers.engram] block; got:\n%s", text)
	}
	// command must reference the engram binary — either relative ("engram") or an
	// absolute path (when engram is on PATH). Both are valid.
	if !strings.Contains(text, "command = ") {
		t.Fatalf("config.toml missing command field; got:\n%s", text)
	}
	cmdLine := ""
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "command = ") {
			cmdLine = strings.TrimSpace(line)
			break
		}
	}
	if cmdLine == "" {
		t.Fatalf("config.toml missing command line; got:\n%s", text)
	}
	// The command value must end with "engram" or "engram.exe".
	cmdVal := strings.TrimPrefix(cmdLine, "command = ")
	cmdVal = strings.Trim(cmdVal, `"`)
	base := filepath.Base(cmdVal)
	if base != "engram" && base != "engram.exe" {
		t.Fatalf("config.toml command %q does not reference engram binary; got:\n%s", cmdVal, text)
	}
	if !strings.Contains(text, `"--tools=agent"`) {
		t.Fatalf("config.toml missing --tools=agent; got:\n%s", text)
	}
}

func TestInjectCodexWritesInstructionFiles(t *testing.T) {
	validCodexRuntime(t)
	home := t.TempDir()

	_, err := Inject(home, codexAdapter())
	if err != nil {
		t.Fatalf("Inject(codex) error = %v", err)
	}

	instructionsPath := filepath.Join(home, ".codex", "engram-instructions.md")
	content, err := os.ReadFile(instructionsPath)
	if err != nil {
		t.Fatalf("ReadFile(engram-instructions.md) error = %v", err)
	}
	if !strings.Contains(string(content), "mem_save") {
		t.Fatal("engram-instructions.md missing expected content (mem_save)")
	}
	if !strings.Contains(string(content), "needs_review") {
		t.Fatal("engram-instructions.md missing memory lifecycle stale-context rule (needs_review)")
	}

	compactPath := filepath.Join(home, ".codex", "engram-compact-prompt.md")
	compactContent, err := os.ReadFile(compactPath)
	if err != nil {
		t.Fatalf("ReadFile(engram-compact-prompt.md) error = %v", err)
	}
	if !strings.Contains(string(compactContent), "FIRST ACTION REQUIRED") {
		t.Fatal("engram-compact-prompt.md missing expected content (FIRST ACTION REQUIRED)")
	}
}

func TestInjectCodexInjectsTOMLKeys(t *testing.T) {
	validCodexRuntime(t)
	home := t.TempDir()

	_, err := Inject(home, codexAdapter())
	if err != nil {
		t.Fatalf("Inject(codex) error = %v", err)
	}

	configPath := filepath.Join(home, ".codex", "config.toml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config.toml) error = %v", err)
	}
	text := string(content)

	instructionsPath := filepath.Join(home, ".codex", "engram-instructions.md")
	if !strings.Contains(text, `model_instructions_file`) {
		t.Fatalf("config.toml missing model_instructions_file key; got:\n%s", text)
	}
	normText := strings.ReplaceAll(strings.ReplaceAll(text, "\\\\", "/"), "\\", "/")
	normInstrPath := filepath.ToSlash(instructionsPath)
	if !strings.Contains(normText, normInstrPath) {
		t.Fatalf("config.toml model_instructions_file does not reference %q; got:\n%s", instructionsPath, text)
	}

	compactPath := filepath.Join(home, ".codex", "engram-compact-prompt.md")
	if !strings.Contains(text, `experimental_compact_prompt_file`) {
		t.Fatalf("config.toml missing experimental_compact_prompt_file key; got:\n%s", text)
	}
	normCompactPath := filepath.ToSlash(compactPath)
	if !strings.Contains(normText, normCompactPath) {
		t.Fatalf("config.toml experimental_compact_prompt_file does not reference %q; got:\n%s", compactPath, text)
	}
}

// ─── Engram setup absolute path preservation tests ────────────────────────────

// TestInjectClaudePreservesAbsoluteCommandFromEngramSetup verifies that when
// `engram setup claude-code` has already written an absolute-path command to
// the managed legacy file, Inject() migrates it into Claude's user registry.
func TestInjectClaudePreservesAbsoluteCommandFromEngramSetup(t *testing.T) {
	home := t.TempDir()

	// Simulate what `engram setup claude-code` writes on v1.10.3+:
	// an absolute path as the command value.
	absPath := "/opt/homebrew/bin/engram"
	mcpPath := filepath.Join(home, ".claude", "mcp", "engram.json")
	if err := os.MkdirAll(filepath.Dir(mcpPath), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	setupContent := []byte(`{
  "command": "/opt/homebrew/bin/engram",
  "args": ["mcp", "--tools=agent"]
}
`)
	if err := os.WriteFile(mcpPath, setupContent, 0o644); err != nil {
		t.Fatalf("WriteFile(engram.json) error = %v", err)
	}

	// Now run Inject — should NOT overwrite the absolute command.
	_, err := Inject(home, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	registryPath := claude.UserConfigPath(home)
	content, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("ReadFile(user registry) error = %v", err)
	}

	text := string(content)
	if !strings.Contains(text, absPath) {
		t.Fatalf("Inject() overwrote absolute command path; want %q preserved, got:\n%s", absPath, text)
	}
	assertArgsHaveToolsAgent(t, registryPath)
	if _, statErr := os.Stat(mcpPath); !os.IsNotExist(statErr) {
		t.Fatalf("managed legacy file must be removed after migration; stat error = %v", statErr)
	}
	if _, statErr := os.Lstat(filepath.Dir(mcpPath)); !os.IsNotExist(statErr) {
		t.Fatalf("empty managed legacy directory must be removed; stat error = %v", statErr)
	}
}

// TestInjectClaudeSkipsMCPServersEngramWhenPluginEnabled reproduces issue
// #4188: brunnr-code sync must not add mcpServers.engram to ~/.claude.json
// when the Engram plugin is already enabled via
// ~/.claude/settings.json's enabledPlugins["engram@engram"], because the
// plugin already exposes the same 18 tools under a different prefix.
func TestInjectClaudeSkipsMCPServersEngramWhenPluginEnabled(t *testing.T) {
	home := t.TempDir()
	mockEngramLookPath(t, "/opt/homebrew/bin/engram", "")
	writeClaudeEngramPluginEnabled(t, home)

	registryPath := claude.UserConfigPath(home)
	registrySeed := []byte(`{"mcpServers":{"context7":{"command":"npx"}}}`)
	if err := os.WriteFile(registryPath, registrySeed, 0o644); err != nil {
		t.Fatal(err)
	}

	// Repeated syncs must leave the user registry byte-for-byte unchanged.
	for run := 1; run <= 2; run++ {
		if _, err := Inject(home, claudeAdapter()); err != nil {
			t.Fatalf("Inject() run %d error = %v", run, err)
		}
		if got, err := os.ReadFile(registryPath); err != nil || !bytes.Equal(got, registrySeed) {
			t.Fatalf("Inject() run %d changed registry: got=%q error=%v", run, got, err)
		}
	}

	registry := readJSONFile(t, registryPath)
	mcpServers, _ := registry["mcpServers"].(map[string]any)
	if _, exists := mcpServers["engram"]; exists {
		t.Fatalf("mcpServers.engram must not be added when the Engram plugin is enabled; registry = %#v", registry)
	}
	assertNestedString(t, registry, "npx", "mcpServers", "context7", "command")
}

// TestInjectClaudePreservesIdenticalManualRegistrationsWhenPluginEnabled
// verifies that sync never infers ownership from an Engram registration's
// shape. A user can author exactly the same registry and legacy entries that
// brunnr-code would write, so plugin detection must only suppress new writes.
func TestInjectClaudePreservesIdenticalManualRegistrationsWhenPluginEnabled(t *testing.T) {
	home := t.TempDir()
	mockEngramLookPath(t, "/opt/homebrew/bin/engram", "")
	writeClaudeEngramPluginEnabled(t, home)

	registryPath := claude.UserConfigPath(home)
	registrySeed := []byte(`{"mcpServers":{"context7":{"command":"npx"},"engram":{"command":"/opt/homebrew/bin/engram","args":["mcp","--tools=agent"]}}}`)
	if err := os.WriteFile(registryPath, registrySeed, 0o644); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(home, ".claude", "mcp", "engram.json")
	legacySeed := []byte(`{"command":"/opt/homebrew/bin/engram","args":["mcp","--tools=agent"]}`)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, legacySeed, 0o644); err != nil {
		t.Fatal(err)
	}

	for run := 1; run <= 2; run++ {
		if _, err := Inject(home, claudeAdapter()); err != nil {
			t.Fatalf("Inject() run %d error = %v", run, err)
		}
	}
	if got, err := os.ReadFile(registryPath); err != nil || !bytes.Equal(got, registrySeed) {
		t.Fatalf("manual registry changed: got=%q error=%v", got, err)
	}
	if got, err := os.ReadFile(legacyPath); err != nil || !bytes.Equal(got, legacySeed) {
		t.Fatalf("manual legacy config changed: got=%q error=%v", got, err)
	}
}

// TestInjectClaudePreservesUserAuthoredMCPServersEngramWhenPluginEnabled
// verifies that plugin detection only suppresses new direct registration and
// never alters an existing customized mcpServers.engram entry.
func TestInjectClaudePreservesUserAuthoredMCPServersEngramWhenPluginEnabled(t *testing.T) {
	home := t.TempDir()
	mockEngramLookPath(t, "/opt/homebrew/bin/engram", "")
	writeClaudeEngramPluginEnabled(t, home)

	registryPath := claude.UserConfigPath(home)
	seed := `{"mcpServers":{"engram":{"command":"/custom/path/engram","args":["mcp","--tools=agent"],"env":{"FOO":"bar"}}}}`
	if err := os.WriteFile(registryPath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	// Inject() also bootstraps CLAUDE.md's protocol section on a fresh
	// tempdir, so overall Changed being true here is expected and not the
	// behavior under test; only the registry content matters (issue #4188).
	if _, err := Inject(home, claudeAdapter()); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	registry := readJSONFile(t, registryPath)
	assertNestedString(t, registry, "/custom/path/engram", "mcpServers", "engram", "command")
	assertNestedString(t, registry, "bar", "mcpServers", "engram", "env", "FOO")
}

func TestInjectClaudeUsesDirectMCPWhenPluginIsNotEnabled(t *testing.T) {
	for _, tt := range []struct {
		name     string
		settings string
	}{
		{name: "plugin disabled", settings: `{"enabledPlugins":{"engram@engram":false}}`},
		{name: "malformed settings", settings: `{not-json`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			mockEngramLookPath(t, "/opt/homebrew/bin/engram", "")
			settingsPath := filepath.Join(home, ".claude", "settings.json")
			if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(settingsPath, []byte(tt.settings), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := Inject(home, claudeAdapter()); err != nil {
				t.Fatalf("Inject() error = %v", err)
			}
			registry := readJSONFile(t, claude.UserConfigPath(home))
			assertNestedString(t, registry, "/opt/homebrew/bin/engram", "mcpServers", "engram", "command")
		})
	}
}

func TestInjectClaudePreservesMalformedRegistryWhenPluginEnabled(t *testing.T) {
	home := t.TempDir()
	writeClaudeEngramPluginEnabled(t, home)
	registryPath := claude.UserConfigPath(home)
	registrySeed := []byte(`{not-json`)
	if err := os.WriteFile(registryPath, registrySeed, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Inject(home, claudeAdapter()); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	if got, err := os.ReadFile(registryPath); err != nil || !bytes.Equal(got, registrySeed) {
		t.Fatalf("malformed user registry changed: got=%q error=%v", got, err)
	}
}

func writeClaudeEngramPluginEnabled(t *testing.T, home string) {
	t.Helper()
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"enabledPlugins":{"engram@engram":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInjectClaudePreservesManagedLegacyParentLayouts(t *testing.T) {
	for _, symlinkParent := range []bool{true, false} {
		name := "non-empty real parent"
		if symlinkParent {
			name = "symlink parent"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			parent := filepath.Join(home, ".claude", "mcp")
			actualParent := parent
			if symlinkParent {
				actualParent = t.TempDir()
				if err := os.MkdirAll(filepath.Dir(parent), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(actualParent, parent); err != nil {
					t.Fatal(err)
				}
			} else if err := os.MkdirAll(parent, 0o755); err != nil {
				t.Fatal(err)
			}
			legacy := filepath.Join(actualParent, "engram.json")
			custom := filepath.Join(actualParent, "custom.json")
			customBytes := []byte(`{"command":"custom"}`)
			if err := os.WriteFile(legacy, []byte(`{"command":"/usr/local/bin/engram","args":["mcp","--tools=agent"]}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(custom, customBytes, 0o640); err != nil {
				t.Fatal(err)
			}

			if _, err := Inject(home, claudeAdapter()); err != nil {
				t.Fatalf("Inject() error = %v", err)
			}
			info, err := os.Lstat(parent)
			if err != nil || symlinkParent != (info.Mode()&os.ModeSymlink != 0) {
				t.Fatalf("legacy parent changed unsafely: info=%v error=%v", info, err)
			}
			if got, err := os.ReadFile(custom); err != nil || !bytes.Equal(got, customBytes) {
				t.Fatalf("custom sibling changed: got=%q error=%v", got, err)
			}
		})
	}
}

// TestInjectClaudePreservesAbsoluteCommandIsIdempotent verifies that calling
// Inject() twice when an absolute-path engram.json already exists does not
// cause repeated writes (idempotency).
func TestInjectClaudePreservesAbsoluteCommandIsIdempotent(t *testing.T) {
	home := t.TempDir()

	absPath := "/usr/local/bin/engram"
	mcpPath := filepath.Join(home, ".claude", "mcp", "engram.json")
	if err := os.MkdirAll(filepath.Dir(mcpPath), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	setupContent := []byte(`{
  "command": "/usr/local/bin/engram",
  "args": ["mcp", "--tools=agent"]
}
`)
	if err := os.WriteFile(mcpPath, setupContent, 0o644); err != nil {
		t.Fatalf("WriteFile(engram.json) error = %v", err)
	}

	first, err := Inject(home, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject() first error = %v", err)
	}

	second, err := Inject(home, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject() second error = %v", err)
	}
	if second.Changed {
		t.Fatalf("Inject() second changed = true after absolute-path setup; want idempotent (no change)")
	}

	// Absolute path must still be present in the supported registry.
	content, err := os.ReadFile(claude.UserConfigPath(home))
	if err != nil {
		t.Fatalf("ReadFile(user registry) error = %v", err)
	}
	if !strings.Contains(string(content), absPath) {
		t.Fatalf("absolute command path %q was lost after second Inject(); got:\n%s", absPath, string(content))
	}
	_ = first // first result not the focus of this test
}

func TestInjectClaudePreservesUnmanagedLegacyShapes(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
	}{
		{"command array", []byte(`{"command":["/usr/local/bin/engram"],"args":["mcp","--tools=agent"]}`)},
		{"bare args", []byte(`{"command":"/usr/local/bin/engram","args":["mcp"]}`)},
		{"extra semantics", []byte(`{"command":"/usr/local/bin/engram","args":["mcp","--tools=agent"],"env":{"CUSTOM":"1"}}`)},
		{"custom command", []byte(`{"command":"custom-engram","args":["mcp","--tools=agent"]}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			legacyPath := filepath.Join(home, ".claude", "mcp", "engram.json")
			if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(legacyPath, tt.content, 0o640); err != nil {
				t.Fatal(err)
			}
			if _, err := Inject(home, claudeAdapter()); err != nil {
				t.Fatalf("Inject() error = %v", err)
			}
			got, err := os.ReadFile(legacyPath)
			if err != nil || !bytes.Equal(got, tt.content) {
				t.Fatalf("unmanaged legacy config changed: got=%q error=%v", got, err)
			}
		})
	}
}

func TestInjectClaudeMigratesCellarCommandToStablePath(t *testing.T) {
	home := t.TempDir()

	mockEngramLookPath(t, "/usr/local/bin/engram", "")

	mcpPath := filepath.Join(home, ".claude", "mcp", "engram.json")
	if err := os.MkdirAll(filepath.Dir(mcpPath), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	setupContent := []byte(`{
  "command": "/usr/local/Cellar/engram/1.14.1/bin/engram",
  "args": ["mcp", "--tools=agent"]
}
`)
	if err := os.WriteFile(mcpPath, setupContent, 0o644); err != nil {
		t.Fatalf("WriteFile(engram.json) error = %v", err)
	}

	result, err := Inject(home, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	if !result.Changed {
		t.Fatalf("Inject() changed = false; expected Cellar command migration")
	}

	registryPath := claude.UserConfigPath(home)
	content, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("ReadFile(user registry) error = %v", err)
	}
	text := string(content)
	if strings.Contains(text, "/Cellar/") {
		t.Fatalf("engram.json still contains versioned Homebrew Cellar path; got:\n%s", text)
	}
	if !strings.Contains(text, "/usr/local/bin/engram") {
		t.Fatalf("engram.json did not migrate to stable Homebrew symlink; got:\n%s", text)
	}
	assertArgsHaveToolsAgent(t, registryPath)
}

func TestInjectCodexIsIdempotent(t *testing.T) {
	validCodexRuntime(t)
	home := t.TempDir()

	first, err := Inject(home, codexAdapter())
	if err != nil {
		t.Fatalf("Inject(codex) first error = %v", err)
	}
	if !first.Changed {
		t.Fatalf("Inject(codex) first changed = false")
	}

	second, err := Inject(home, codexAdapter())
	if err != nil {
		t.Fatalf("Inject(codex) second error = %v", err)
	}
	if second.Changed {
		t.Fatalf("Inject(codex) second changed = true (should be idempotent)")
	}

	// Verify only one [mcp_servers.engram] block.
	configPath := filepath.Join(home, ".codex", "config.toml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config.toml) error = %v", err)
	}
	count := strings.Count(string(content), "[mcp_servers.engram]")
	if count != 1 {
		t.Fatalf("config.toml has %d [mcp_servers.engram] blocks, want exactly 1; got:\n%s", count, string(content))
	}
}

func TestInjectCodexPreservesEngramHeaderInsideMultilineString(t *testing.T) {
	validCodexRuntime(t)
	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	instructions := `developer_instructions = """
Example config:
[mcp_servers.engram]
command = "fake"
Keep this text.
"""`
	if err := os.WriteFile(configPath, []byte(instructions+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(config.toml) error = %v", err)
	}

	if _, err := Inject(home, codexAdapter()); err != nil {
		t.Fatalf("Inject(codex) first error = %v", err)
	}
	second, err := Inject(home, codexAdapter())
	if err != nil {
		t.Fatalf("Inject(codex) second error = %v", err)
	}
	if second.Changed {
		t.Fatalf("Inject(codex) second changed = true (should be idempotent)")
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config.toml) error = %v", err)
	}
	text := string(content)
	if !strings.Contains(text, instructions) {
		t.Fatalf("developer_instructions was not preserved byte-for-byte; got:\n%s", text)
	}
	// One header lives in the preserved string, the other is the managed block.
	if count := strings.Count(text, "[mcp_servers.engram]"); count != 2 {
		t.Fatalf("config.toml has %d [mcp_servers.engram] lines, want 2; got:\n%s", count, text)
	}
	if !strings.Contains(text, `"--tools=agent"`) {
		t.Fatalf("config.toml missing managed engram block; got:\n%s", text)
	}
}

// ─── Codex shared config injection tests ─────────────────────────────────────

func TestInjectCodexWithoutCLIUpdatesSharedConfig(t *testing.T) {
	restore := codex.SetRuntimeVersionCommandForTest("", exec.ErrNotFound)
	t.Cleanup(restore)

	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	const configBefore = "custom = \"preserve\"\n[unrelated]\nkeep = true\n"
	if err := os.WriteFile(configPath, []byte(configBefore), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := Inject(home, codexAdapter())
	if err != nil {
		t.Fatalf("Inject(codex) with absent CLI error = %v", err)
	}
	if !first.Changed {
		t.Fatal("Inject(codex) with absent CLI changed = false")
	}
	configAfterFirst, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configAfterFirst), "custom = \"preserve\"") || !strings.Contains(string(configAfterFirst), "[mcp_servers.engram]") {
		t.Fatalf("shared config was not preserved and updated:\n%s", configAfterFirst)
	}

	second, err := Inject(home, codexAdapter())
	if err != nil {
		t.Fatalf("second Inject(codex) with absent CLI error = %v", err)
	}
	if second.Changed {
		t.Fatal("second Inject(codex) with absent CLI changed = true")
	}
	configAfterSecond, err := os.ReadFile(configPath)
	if err != nil || !bytes.Equal(configAfterSecond, configAfterFirst) {
		t.Fatalf("shared config was not idempotent: got=%q error=%v", configAfterSecond, err)
	}
}

func TestInjectCodexReportsInstructionOnlyRepairsWithoutCLI(t *testing.T) {
	restore := codex.SetRuntimeVersionCommandForTest("", exec.ErrNotFound)
	t.Cleanup(restore)

	for _, name := range []string{"engram-instructions.md", "engram-compact-prompt.md"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			if _, err := Inject(home, codexAdapter()); err != nil {
				t.Fatalf("initial Inject(codex) error = %v", err)
			}

			path := filepath.Join(home, ".codex", name)
			if err := os.WriteFile(path, []byte("stale instruction\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			result, err := Inject(home, codexAdapter())
			if err != nil {
				t.Fatalf("instruction-only repair error = %v", err)
			}
			if !result.Changed {
				t.Fatal("instruction-only repair changed = false")
			}
			found := false
			for _, file := range result.Files {
				if file == path {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("instruction-only repair files = %v, want %q", result.Files, path)
			}

			second, err := Inject(home, codexAdapter())
			if err != nil {
				t.Fatalf("idempotent Inject(codex) error = %v", err)
			}
			if second.Changed {
				t.Fatal("idempotent instruction-only repair changed = true")
			}
		})
	}
}

func TestInjectCodexWithoutCLIWritesSharedConfig(t *testing.T) {
	restore := codex.SetRuntimeVersionCommandForTest("", exec.ErrNotFound)
	t.Cleanup(restore)

	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	first, err := Inject(home, codexAdapter())
	if err != nil {
		t.Fatalf("Inject(codex) with absent CLI error = %v", err)
	}
	if !first.Changed {
		t.Fatal("Inject(codex) with absent CLI changed = false")
	}
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config.toml) error = %v", err)
	}
	if !strings.Contains(string(config), "[mcp_servers.engram]") ||
		!strings.Contains(string(config), "model_instructions_file") ||
		!strings.Contains(string(config), "experimental_compact_prompt_file") {
		t.Fatalf("shared MCP/instruction config was not written:\n%s", config)
	}
	for _, path := range []string{
		filepath.Join(home, ".codex", "engram-instructions.md"),
		filepath.Join(home, ".codex", "engram-compact-prompt.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("shared instruction file %q was not written: %v", path, err)
		}
	}

	second, err := Inject(home, codexAdapter())
	if err != nil {
		t.Fatalf("second Inject(codex) with absent CLI error = %v", err)
	}
	if second.Changed {
		t.Fatal("second Inject(codex) with absent CLI changed = true")
	}
}

func TestInjectCodexInvalidRuntimeDoesNotMutateFiles(t *testing.T) {
	tests := []struct {
		name   string
		output string
		err    error
	}{
		{name: "broken executable", err: &exec.Error{Name: "codex", Err: os.ErrPermission}},
		{name: "malformed version", output: "codex-cli development"},
		{name: "old version", output: "codex-cli 0.143.9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restore := codex.SetRuntimeVersionCommandForTest(tt.output, tt.err)
			t.Cleanup(restore)

			home := t.TempDir()
			configPath := filepath.Join(home, ".codex", "config.toml")
			const configBefore = "user_setting = \"keep\"\n"
			if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configPath, []byte(configBefore), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := Inject(home, codexAdapter()); err == nil {
				t.Fatal("Inject(codex) error = nil")
			}
			if got, err := os.ReadFile(configPath); err != nil || string(got) != configBefore {
				t.Fatalf("config changed after validation failure: got=%q error=%v", got, err)
			}
		})
	}
}

// ─── Codex multi-agent config injection tests ────────────────────────────────

// TestInjectCodexMultiAgentDefaultOn asserts that after a plain Inject call,
// config.toml contains [features] with multi_agent = true. Codex SDD enables
// multi-agent delegation by default so the per-phase reasoning_effort table applies.
func TestInjectCodexMultiAgentDefaultOn(t *testing.T) {
	validCodexRuntime(t)
	home := t.TempDir()

	if _, err := Inject(home, codexAdapter()); err != nil {
		t.Fatalf("Inject(codex) error = %v", err)
	}

	configPath := filepath.Join(home, ".codex", "config.toml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config.toml) error = %v", err)
	}
	text := string(content)
	if !strings.Contains(text, "[features]") {
		t.Fatalf("config.toml missing [features] section; got:\n%s", text)
	}
	if !strings.Contains(text, "multi_agent = true") {
		t.Fatalf("config.toml missing multi_agent = true (enabled by default); got:\n%s", text)
	}
	if strings.Contains(text, "multi_agent = false") {
		t.Fatalf("config.toml must NOT have multi_agent = false by default; got:\n%s", text)
	}
}

// TestInjectCodexMultiAgentOptIn asserts that InjectWithOptions with
// CodexMultiAgent=true writes multi_agent = true in [features].
func TestInjectCodexMultiAgentOptIn(t *testing.T) {
	validCodexRuntime(t)
	home := t.TempDir()

	opts := InjectOptions{CodexMultiAgent: true}
	if _, err := InjectWithOptions(home, codexAdapter(), opts); err != nil {
		t.Fatalf("InjectWithOptions(codex, multiAgent=true) error = %v", err)
	}

	configPath := filepath.Join(home, ".codex", "config.toml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config.toml) error = %v", err)
	}
	text := string(content)
	if !strings.Contains(text, "multi_agent = true") {
		t.Fatalf("config.toml missing multi_agent = true after opt-in; got:\n%s", text)
	}
}

// TestInjectCodexMultiAgentDefaults asserts that the [agents] section is always
// written with max_threads = 4 and max_depth = 2 regardless of the opt-in flag.
func TestInjectCodexMultiAgentDefaults(t *testing.T) {
	validCodexRuntime(t)
	home := t.TempDir()

	if _, err := Inject(home, codexAdapter()); err != nil {
		t.Fatalf("Inject(codex) error = %v", err)
	}

	configPath := filepath.Join(home, ".codex", "config.toml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config.toml) error = %v", err)
	}
	text := string(content)
	if !strings.Contains(text, "[agents]") {
		t.Fatalf("config.toml missing [agents] section; got:\n%s", text)
	}
	if !strings.Contains(text, "max_threads = 4") {
		t.Fatalf("config.toml missing max_threads = 4; got:\n%s", text)
	}
	if !strings.Contains(text, "max_depth = 2") {
		t.Fatalf("config.toml missing max_depth = 2; got:\n%s", text)
	}
}

// TestInjectCodexMultiAgentIdempotent asserts that running Inject twice
// produces exactly one [features] section and one [agents] section with no
// duplicate keys, and that the engram and context7 blocks are not disturbed.
func TestInjectCodexMultiAgentIdempotent(t *testing.T) {
	validCodexRuntime(t)
	home := t.TempDir()

	if _, err := Inject(home, codexAdapter()); err != nil {
		t.Fatalf("first Inject(codex) error = %v", err)
	}
	second, err := Inject(home, codexAdapter())
	if err != nil {
		t.Fatalf("second Inject(codex) error = %v", err)
	}
	if second.Changed {
		// Read content for diagnostics.
		content, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
		t.Fatalf("second Inject(codex) changed = true, want false (multi-agent keys are idempotent); config.toml:\n%s", string(content))
	}

	configPath := filepath.Join(home, ".codex", "config.toml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config.toml) error = %v", err)
	}
	text := string(content)

	if count := strings.Count(text, "[features]"); count != 1 {
		t.Fatalf("expected 1 [features] section, got %d; config.toml:\n%s", count, text)
	}
	if count := strings.Count(text, "[agents]"); count != 1 {
		t.Fatalf("expected 1 [agents] section, got %d; config.toml:\n%s", count, text)
	}
	if count := strings.Count(text, "multi_agent"); count != 1 {
		t.Fatalf("expected 1 multi_agent key, got %d; config.toml:\n%s", count, text)
	}
	if count := strings.Count(text, "max_threads"); count != 1 {
		t.Fatalf("expected 1 max_threads key, got %d; config.toml:\n%s", count, text)
	}
	if count := strings.Count(text, "max_depth"); count != 1 {
		t.Fatalf("expected 1 max_depth key, got %d; config.toml:\n%s", count, text)
	}
	// Engram MCP block must still be present.
	if !strings.Contains(text, "[mcp_servers.engram]") {
		t.Fatalf("config.toml missing [mcp_servers.engram] after idempotency run; got:\n%s", text)
	}
}

// ─── Absolute path resolution tests ──────────────────────────────────────────

// mockEngramLookPath sets EngramLookPath to a mock and restores it after the test.
func mockEngramLookPath(t *testing.T, result string, errMsg string) {
	t.Helper()
	orig := EngramLookPath
	EngramLookPath = func(string) (string, error) {
		if errMsg != "" {
			return "", fmt.Errorf("%s", errMsg)
		}
		return result, nil
	}
	t.Cleanup(func() { EngramLookPath = orig })
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func readJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v; content:\n%s", path, err, raw)
	}
	return parsed
}

func nestedValue(t *testing.T, root map[string]any, path ...string) (any, bool) {
	t.Helper()
	var current any = root
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[key]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func assertNestedString(t *testing.T, root map[string]any, want string, path ...string) {
	t.Helper()
	got, ok := nestedValue(t, root, path...)
	if !ok {
		t.Fatalf("missing JSON path %v in %#v", path, root)
	}
	if got != want {
		t.Fatalf("JSON path %v = %#v, want %q", path, got, want)
	}
}

func assertNestedStrings(t *testing.T, root map[string]any, want []string, path ...string) {
	t.Helper()
	got, ok := nestedValue(t, root, path...)
	if !ok {
		t.Fatalf("missing JSON path %v in %#v", path, root)
	}
	items, ok := got.([]any)
	if !ok {
		t.Fatalf("JSON path %v = %#v, want string array", path, got)
	}
	if len(items) != len(want) {
		t.Fatalf("JSON path %v length = %d, want %d (%#v)", path, len(items), len(want), got)
	}
	for i, wantItem := range want {
		if items[i] != wantItem {
			t.Fatalf("JSON path %v[%d] = %#v, want %q", path, i, items[i], wantItem)
		}
	}
}

func assertNestedMissing(t *testing.T, root map[string]any, path ...string) {
	t.Helper()
	if got, ok := nestedValue(t, root, path...); ok {
		t.Fatalf("JSON path %v present = %#v, want missing", path, got)
	}
}

func unmarshalObjectForTest(t *testing.T, content []byte) map[string]any {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(content, &root); err != nil {
		t.Fatalf("Unmarshal JSON error = %v; content:\n%s", err, content)
	}
	return root
}

func objectAtForTest(t *testing.T, root map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := root[key]
	if !ok {
		t.Fatalf("missing object key %q in %#v", key, root)
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("key %q has type %T, want object", key, value)
	}
	return object
}

// ---------------------------------------------------------------------------
// Decision 1 per-adapter slim/full selection matrix (16 adapters).
// ---------------------------------------------------------------------------

// aboveFloorVersion is a version comfortably above the v1.4.0 gate (matches
// the live evidence cited in design.md Decision 1: engram 1.18.0).
const aboveFloorVersion = "1.18.0"

// TestProtocolForSelectsSlimOrFullPerDecision1Matrix is the 16-row table test
// required by task 1.2: Claude Code -> slim (gated on engram >= v1.4.0), all
// other adapters with a setup slug or system-prompt surface -> full. Pi is
// covered separately below since it never renders protocol text at all
// (existing MCP-only precedent, unchanged by this change).
func TestProtocolForSelectsSlimOrFullPerDecision1Matrix(t *testing.T) {
	tests := []struct {
		agent    model.AgentID
		wantSlim bool
	}{
		{model.AgentClaudeCode, true},
		{model.AgentCodex, false},
	}

	if len(tests) != 2 {
		t.Fatalf("expected 2 adapters in the Decision 1 matrix, got %d", len(tests))
	}

	slim := protocolSlim()
	full := protocolFull()

	for _, tt := range tests {
		t.Run(string(tt.agent), func(t *testing.T) {
			got := protocolFor(tt.agent, InjectOptions{Version: aboveFloorVersion})
			want := full
			if tt.wantSlim {
				want = slim
			}
			if got != want {
				gotKind, wantKind := "full", "full"
				if got == slim {
					gotKind = "slim"
				}
				if tt.wantSlim {
					wantKind = "slim"
				}
				t.Fatalf("protocolFor(%s, above-floor version) = %s section, want %s section", tt.agent, gotKind, wantKind)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Decision 1 version-gate boundary (task 1.3).
// ---------------------------------------------------------------------------

func TestProtocolForVersionGateBoundary(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		wantSlim bool
	}{
		{"below floor", "1.3.9", false},
		{"unknown/unparseable version", "not-a-version", false},
		{"empty version (VerifyVersion failed)", "", false},
		{"exact floor v1.4.0 (inclusive boundary)", "1.4.0", true},
		{"above floor", aboveFloorVersion, true},
	}

	slim := protocolSlim()
	full := protocolFull()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := protocolFor(model.AgentClaudeCode, InjectOptions{Version: tt.version})
			want := full
			if tt.wantSlim {
				want = slim
			}
			if got != want {
				t.Fatalf("protocolFor(claude-code, version=%q) did not match expected section (wantSlim=%v)", tt.version, tt.wantSlim)
			}
		})
	}
}

// TestInjectWithOptionsThreadsVersionIntoClaudeSlimSelection is the
// integration-level counterpart of the boundary test above: it exercises the
// full InjectWithOptions -> CLAUDE.md write path and asserts the rendered
// section flips from full to slim once InjectOptions.Version crosses the
// v1.4.0 floor.
func TestInjectWithOptionsThreadsVersionIntoClaudeSlimSelection(t *testing.T) {
	belowFloorHome := t.TempDir()
	if _, err := InjectWithOptions(belowFloorHome, claudeAdapter(), InjectOptions{Version: "1.3.9"}); err != nil {
		t.Fatalf("InjectWithOptions(claude, below floor) error = %v", err)
	}
	belowContent, err := os.ReadFile(filepath.Join(belowFloorHome, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("ReadFile(CLAUDE.md) error = %v", err)
	}
	if !strings.Contains(string(belowContent), "needs_review") {
		t.Fatal("below-floor version must render the FULL section (expected 'needs_review' from full text)")
	}

	aboveFloorHome := t.TempDir()
	if _, err := InjectWithOptions(aboveFloorHome, claudeAdapter(), InjectOptions{Version: aboveFloorVersion}); err != nil {
		t.Fatalf("InjectWithOptions(claude, above floor) error = %v", err)
	}
	aboveContent, err := os.ReadFile(filepath.Join(aboveFloorHome, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("ReadFile(CLAUDE.md) error = %v", err)
	}
	if strings.Contains(string(aboveContent), "needs_review") {
		t.Fatal("above-floor version must render the SLIM section (must not contain full-only 'needs_review' text)")
	}
	if !strings.Contains(string(aboveContent), "SessionStart hook") {
		t.Fatal("above-floor version must render the SLIM section with its pointer to the full protocol location")
	}
}

// TestInjectWithOptionsReInjectConvergesFullToSlimAndBack is the spec
// scenario "Re-inject converges to target state" (Idempotent injection and
// clean uninstall across upgrades): a previously-injected full section MUST
// converge to slim (and vice versa) via the existing marker-based mechanism
// when the target verdict changes across two Inject calls, without
// duplicating or corrupting markers.
func TestInjectWithOptionsReInjectConvergesFullToSlimAndBack(t *testing.T) {
	home := t.TempDir()
	claudeMDPath := filepath.Join(home, ".claude", "CLAUDE.md")

	// 1. Full (below-floor / unknown version — safe default).
	if _, err := InjectWithOptions(home, claudeAdapter(), InjectOptions{}); err != nil {
		t.Fatalf("InjectWithOptions(full) error = %v", err)
	}
	afterFull, err := os.ReadFile(claudeMDPath)
	if err != nil {
		t.Fatalf("ReadFile(CLAUDE.md) error = %v", err)
	}
	if !strings.Contains(string(afterFull), "needs_review") {
		t.Fatalf("expected FULL section after first inject; got:\n%s", afterFull)
	}
	if n := strings.Count(string(afterFull), "<!-- brunnr-code:engram-protocol -->"); n != 1 {
		t.Fatalf("expected exactly 1 open marker after first inject, got %d", n)
	}

	// 2. Re-inject with an above-floor version — MUST converge to slim, in
	// place, no duplicate markers.
	if _, err := InjectWithOptions(home, claudeAdapter(), InjectOptions{Version: aboveFloorVersion}); err != nil {
		t.Fatalf("InjectWithOptions(slim) error = %v", err)
	}
	afterSlim, err := os.ReadFile(claudeMDPath)
	if err != nil {
		t.Fatalf("ReadFile(CLAUDE.md) error = %v", err)
	}
	if strings.Contains(string(afterSlim), "needs_review") {
		t.Fatalf("expected SLIM section after re-inject with above-floor version; got:\n%s", afterSlim)
	}
	if !strings.Contains(string(afterSlim), "SessionStart hook") {
		t.Fatalf("expected SLIM pointer content after re-inject; got:\n%s", afterSlim)
	}
	if n := strings.Count(string(afterSlim), "<!-- brunnr-code:engram-protocol -->"); n != 1 {
		t.Fatalf("expected exactly 1 open marker after re-inject to slim (no duplication), got %d", n)
	}
	if n := strings.Count(string(afterSlim), "<!-- /brunnr-code:engram-protocol -->"); n != 1 {
		t.Fatalf("expected exactly 1 close marker after re-inject to slim (no duplication), got %d", n)
	}

	// 3. Re-inject back to full — MUST converge back, still no duplication.
	if _, err := InjectWithOptions(home, claudeAdapter(), InjectOptions{}); err != nil {
		t.Fatalf("InjectWithOptions(back to full) error = %v", err)
	}
	afterBackToFull, err := os.ReadFile(claudeMDPath)
	if err != nil {
		t.Fatalf("ReadFile(CLAUDE.md) error = %v", err)
	}
	if !strings.Contains(string(afterBackToFull), "needs_review") {
		t.Fatalf("expected FULL section after re-inject back to full; got:\n%s", afterBackToFull)
	}
	if n := strings.Count(string(afterBackToFull), "<!-- brunnr-code:engram-protocol -->"); n != 1 {
		t.Fatalf("expected exactly 1 open marker after re-inject back to full (no duplication), got %d", n)
	}
}

func TestInjectCodexOrchestratorAssignmentWritesTopLevelModel(t *testing.T) {
	validCodexRuntime(t)
	home := t.TempDir()
	opts := InjectOptions{CodexOrchestratorAssignment: model.CodexPresetOrchestratorAssignment(string(model.CodexPresetRecommended))}
	if _, err := InjectWithOptions(home, codexAdapter(), opts); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if !strings.Contains(text, `model = "gpt-6.1-sol"`) || !strings.Contains(text, `model_reasoning_effort = "medium"`) {
		t.Fatalf("top-level orchestrator assignment missing:\n%s", text)
	}
}

func TestInjectCodexOrchestratorAssignmentPreservesNestedModelAssignments(t *testing.T) {
	validCodexRuntime(t)
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`model = "old-top-level-model"
model_reasoning_effort = "low"

[[profiles]] # user settings
model = "nested-model"
model_reasoning_effort = "nested-effort"
model_instructions_file = "nested-instructions.md"
experimental_compact_prompt_file = "nested-compact.md"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := InjectOptions{CodexOrchestratorAssignment: model.CodexPresetOrchestratorAssignment(string(model.CodexPresetRecommended))}
	if _, err := InjectWithOptions(home, codexAdapter(), opts); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if !strings.Contains(text, `model = "gpt-6.1-sol"`) || !strings.Contains(text, `model_reasoning_effort = "medium"`) {
		t.Fatalf("top-level orchestrator assignment missing:\n%s", text)
	}
	if !strings.Contains(text, `[[profiles]] # user settings
model = "nested-model"
model_reasoning_effort = "nested-effort"
model_instructions_file = "nested-instructions.md"
experimental_compact_prompt_file = "nested-compact.md"`) {
		t.Fatalf("nested model assignments were not preserved:\n%s", text)
	}
}

func TestInjectCodexNilOrchestratorAssignmentPreservesTopLevelModel(t *testing.T) {
	validCodexRuntime(t)
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("model = \"user-model\"\nmodel_reasoning_effort = \"high\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InjectWithOptions(home, codexAdapter(), InjectOptions{}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `model = "user-model"`) || !strings.Contains(string(content), `model_reasoning_effort = "high"`) {
		t.Fatalf("nil assignment clobbered top-level model:\n%s", content)
	}
}

func TestUpsertCodexTableKeyBeforeMCPServersKeepsMCPBlocksAtEOF(t *testing.T) {
	tests := []struct {
		name, content, want string
	}{
		{
			name:    "missing table goes before existing MCP block",
			content: "[mcp_servers.context7]\nurl = \"https://mcp.context7.com/mcp\"\n",
			want:    "[features]\nmulti_agent = true\n\n[mcp_servers.context7]\nurl = \"https://mcp.context7.com/mcp\"\n",
		},
		{
			name:    "missing table without MCP blocks is appended",
			content: "model = \"gpt\"\n",
			want:    "model = \"gpt\"\n\n[features]\nmulti_agent = true\n",
		},
		{
			name:    "existing table is updated in place",
			content: "[mcp_servers.context7]\nurl = \"u\"\n\n[features]\nmulti_agent = false\n",
			want:    "[mcp_servers.context7]\nurl = \"u\"\n\n[features]\nmulti_agent = true\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := upsertCodexTableKeyBeforeMCPServers(tt.content, "features", "multi_agent", "true"); got != tt.want {
				t.Fatalf("upsert =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

// TestUpsertCodexTableKeyBeforeMCPServersIgnoresMultilineStrings pins that an
// MCP table header written inside a TOML multiline string is text, not the
// first MCP block: the new table must land after the string and before the
// real MCP block instead of splitting the string.
func TestUpsertCodexTableKeyBeforeMCPServersIgnoresMultilineStrings(t *testing.T) {
	for _, delimiter := range []string{`"""`, "'''"} {
		content := "developer_instructions = " + delimiter + "\nExample:\n[mcp_servers.docs]\n" + delimiter + "\n\n[mcp_servers.context7]\ncommand = \"npx\"\n"
		got := upsertCodexTableKeyBeforeMCPServers(content, "features", "multi_agent", "true")
		stringEnd := strings.LastIndex(got, delimiter)
		table := strings.Index(got, "[features]\n")
		mcp := strings.Index(got, "[mcp_servers.context7]")
		if table < 0 || table < stringEnd || table > mcp {
			t.Fatalf("delimiter %s: [features] must be inserted after the multiline string and before the real MCP block:\n%s", delimiter, got)
		}
		if !strings.Contains(got, "Example:\n[mcp_servers.docs]\n"+delimiter) {
			t.Fatalf("delimiter %s: multiline string text was altered:\n%s", delimiter, got)
		}
	}
}

// TestUpsertCodexTableKeyBeforeMCPServersIgnoresTargetHeaderInMultilineStrings
// pins #5022: a [features] line inside developer_instructions is text, so the
// key must go to a real [features] table created outside the string, with or
// without an MCP block after it.
func TestUpsertCodexTableKeyBeforeMCPServersIgnoresTargetHeaderInMultilineStrings(t *testing.T) {
	for _, delimiter := range []string{`"""`, "'''"} {
		instructions := "developer_instructions = " + delimiter + "\n[features]\nmulti_agent = false\n" + delimiter + "\n"
		for _, tail := range []string{"", "\n[mcp_servers.context7]\ncommand = \"npx\"\n"} {
			got := upsertCodexTableKeyBeforeMCPServers(instructions+tail, "features", "multi_agent", "true")
			if !strings.HasPrefix(got, instructions) {
				t.Fatalf("delimiter %s tail %q: multiline string text was altered:\n%s", delimiter, tail, got)
			}
			table := strings.Index(got, delimiter+"\n\n[features]\nmulti_agent = true\n")
			if table < 0 {
				t.Fatalf("delimiter %s tail %q: [features] must be created after the multiline string:\n%s", delimiter, tail, got)
			}
			if mcp := strings.Index(got, "[mcp_servers.context7]"); tail != "" && (mcp < 0 || mcp < table) {
				t.Fatalf("delimiter %s: [features] must stay before the MCP block:\n%s", delimiter, got)
			}
		}
	}
}

// TestUpsertCodexTableKeyBeforeMCPServersIgnoresDelimitersInStringsAndComments
// pins that a triple-quote sequence inside an ordinary string or a comment
// does not open a multiline string, so the real MCP block is still found.
func TestUpsertCodexTableKeyBeforeMCPServersIgnoresDelimitersInStringsAndComments(t *testing.T) {
	for _, prefix := range []string{
		"note = '\"\"\"'\n",
		"# a comment with \"\"\" in it\n",
	} {
		content := prefix + "\n[mcp_servers.context7]\ncommand = \"npx\"\n"
		got := upsertCodexTableKeyBeforeMCPServers(content, "features", "multi_agent", "true")
		table := strings.Index(got, "[features]\n")
		mcp := strings.Index(got, "[mcp_servers.context7]")
		if table < 0 || table > mcp {
			t.Fatalf("prefix %q: [features] must be inserted before the MCP block:\n%s", prefix, got)
		}
	}
}
