package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/agents"
	"github.com/gentleman-programming/gentle-ai/v4/internal/agents/claude"
	"github.com/gentleman-programming/gentle-ai/v4/internal/agents/codex"
	"github.com/gentleman-programming/gentle-ai/v4/internal/agents/opencode"
	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
	"github.com/gentleman-programming/gentle-ai/v4/internal/versions"
)

func TestContext7SelectedSettingsRefuseNestedCommentsAndLockedMode(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		mode          os.FileMode
	}{
		{"nested comments", "{\"mcp\":{\"other\":{/* keep */\"type\":\"remote\"}}}\n", 0o600},
		{"locked mode", "{\"mcp\":{}}\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if runtime.GOOS == "windows" && tc.mode != 0o600 {
				t.Skip("file permission bits are not supported on Windows")
			}
			path := filepath.Join(t.TempDir(), "opencode.jsonc")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, tc.mode); err != nil {
				t.Fatal(err)
			}
			_, err := injectOpenCodeMergeIntoSettings(path, model.AgentOpenCode)
			if err == nil || !strings.Contains(err.Error(), "refuse") {
				t.Fatalf("want actionable refusal, got %v", err)
			}
			info, statErr := os.Stat(path)
			if statErr != nil {
				t.Fatal(statErr)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != tc.mode {
				t.Fatalf("settings mode changed: %04o", info.Mode().Perm())
			}
			if err := os.Chmod(path, 0o600); err != nil {
				t.Fatal(err)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(got) != tc.content {
				t.Fatalf("settings bytes changed: %q", got)
			}
		})
	}
}

func TestContext7SelectedSettingsPreservePrivateMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.jsonc")
	if err := os.WriteFile(path, []byte("{\"mcp\":{}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := injectOpenCodeMergeIntoSettings(path, model.AgentOpenCode); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return // POSIX permission bits are not preserved on Windows.
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("settings mode = %v, error = %v; want 0600", info, err)
	}
}

func claudeAdapter() agents.Adapter   { return claude.NewAdapter() }
func opencodeAdapter() agents.Adapter { return opencode.NewAdapter() }

func assertOnlyKeys(t *testing.T, path string, object map[string]any, keys ...string) {
	t.Helper()

	want := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		want[key] = struct{}{}
	}

	for _, key := range keys {
		if _, exists := object[key]; !exists {
			t.Fatalf("%q missing expected key %q; got %#v", path, key, object)
		}
	}

	for key := range object {
		if _, expected := want[key]; !expected {
			t.Fatalf("%q contains unexpected key %q; want only %v; got %#v", path, key, keys, object)
		}
	}
}

// readOpenCodeContext7Entry reads the mcp.context7 object from an OpenCode/KiloCode
// opencode.json config file. Navigates parsed["mcp"]["context7"].
func readOpenCodeContext7Entry(t *testing.T, path string) map[string]any {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", path, err)
	}

	mcp, ok := parsed["mcp"].(map[string]any)
	if !ok {
		t.Fatalf("%q missing object key mcp; got %#v", path, parsed["mcp"])
	}

	context7, ok := mcp["context7"].(map[string]any)
	if !ok {
		t.Fatalf("%q missing object key mcp.context7; got %#v", path, mcp["context7"])
	}

	return context7
}

// assertOpenCodeRemoteContext7Schema asserts the mcp.context7 entry in an
// OpenCode/KiloCode opencode.json is a valid remote entry with no legacy local keys.
func assertOpenCodeRemoteContext7Schema(t *testing.T, path string) {
	t.Helper()

	context7 := readOpenCodeContext7Entry(t, path)

	if got := context7["type"]; got != "remote" {
		t.Fatalf("%q mcp.context7.type = %#v; want %q", path, got, "remote")
	}
	if got := context7["url"]; got != "https://mcp.context7.com/mcp" {
		t.Fatalf("%q mcp.context7.url = %#v; want context7 remote URL", path, got)
	}
	if got := context7["enabled"]; got != true {
		t.Fatalf("%q mcp.context7.enabled = %#v; want true", path, got)
	}

	for _, key := range []string{"command", "args", "env", "environment"} {
		if _, exists := context7[key]; exists {
			t.Fatalf("%q mcp.context7 contains legacy local key %q; got %#v", path, key, context7)
		}
	}
}

// readMCPServersContext7Entry reads the mcpServers.context7 object used by
// agents that store Context7 under an mcpServers-based config file.
func readMCPServersContext7Entry(t *testing.T, path string) map[string]any {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", path, err)
	}

	mcpServers, ok := parsed["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("%q missing object key mcpServers; got %#v", path, parsed["mcpServers"])
	}

	context7, ok := mcpServers["context7"].(map[string]any)
	if !ok {
		t.Fatalf("%q missing object key mcpServers.context7; got %#v", path, mcpServers["context7"])
	}

	return context7
}

// assertAntigravityContext7Schema asserts the mcpServers.context7 entry in an
// Antigravity mcp_config.json is a valid remote entry with no legacy local keys.
func assertAntigravityContext7Schema(t *testing.T, path string) {
	t.Helper()

	context7 := readMCPServersContext7Entry(t, path)

	if got := context7["serverUrl"]; got != "https://mcp.context7.com/mcp" {
		t.Fatalf("%q mcpServers.context7.serverUrl = %#v; want context7 remote URL", path, got)
	}

	assertOnlyKeys(t, path, context7, "serverUrl")
}

// assertKimiContext7Schema asserts the mcpServers.context7 entry in a Kimi
// mcp.json is the documented remote HTTP config with no legacy local keys.
func assertKimiContext7Schema(t *testing.T, path string) {
	t.Helper()

	context7 := readMCPServersContext7Entry(t, path)

	if got := context7["transport"]; got != "http" {
		t.Fatalf("%q mcpServers.context7.transport = %#v; want %q", path, got, "http")
	}
	if got := context7["url"]; got != "https://mcp.context7.com/mcp" {
		t.Fatalf("%q mcpServers.context7.url = %#v; want context7 remote URL", path, got)
	}

	assertOnlyKeys(t, path, context7, "transport", "url")
}

func TestInjectOpenCodeMergesContext7AndIsIdempotent(t *testing.T) {
	home := t.TempDir()

	first, err := Inject(home, home, opencodeAdapter())
	if err != nil {
		t.Fatalf("Inject() first error = %v", err)
	}
	if !first.Changed {
		t.Fatalf("Inject() first changed = false")
	}

	second, err := Inject(home, home, opencodeAdapter())
	if err != nil {
		t.Fatalf("Inject() second error = %v", err)
	}
	if second.Changed {
		t.Fatalf("Inject() second changed = true")
	}

	configPath := filepath.Join(home, ".config", "opencode", "opencode.json")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(opencode.json) error = %v", err)
	}

	if len(config) == 0 {
		t.Fatalf("opencode.json is empty")
	}

	assertOpenCodeRemoteContext7Schema(t, configPath)

	text := string(config)
	if !strings.Contains(text, `"mcp"`) {
		t.Fatal("opencode.json missing mcp key")
	}
	if !strings.Contains(text, `"type": "remote"`) {
		t.Fatal("opencode.json context7 missing type: remote")
	}
	if strings.Contains(text, `"mcpServers"`) {
		t.Fatal("opencode.json should use 'mcp' key, not 'mcpServers'")
	}
}

func TestInjectOpenCodeRejectsMalformedJSONCSettingsWithoutReplacingBytes(t *testing.T) {
	home := t.TempDir()
	adapter := opencodeAdapter()
	settingsPath := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	original := []byte("// interrupted user edit\n{\n  \"mcp\": {\n")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(settings dir) error = %v", err)
	}
	if err := os.WriteFile(settingsPath, original, 0o644); err != nil {
		t.Fatalf("WriteFile(opencode.jsonc) error = %v", err)
	}

	if _, err := Inject(home, home, adapter); err == nil {
		t.Fatal("Inject() error = nil, want refusal for malformed opencode.jsonc")
	}
	after, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile(opencode.jsonc) error = %v", err)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("malformed opencode.jsonc was replaced:\n got: %q\nwant: %q", after, original)
	}
}

func TestInjectOpenCodePreservesOtherMCPEntriesAlongsideContext7(t *testing.T) {
	home := t.TempDir()
	adapter := opencodeAdapter()
	configPath := adapter.SettingsPath(home)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}

	legacy := `{
	  "mcp": {
	    "context7": {
	      "type": "local",
	      "command": ["npx", "-y", "@upstash/context7-mcp"],
	      "args": ["legacy"],
	      "env": {"TOKEN": "x"},
	      "enabled": false
	    },
	    "engram": {
	      "type": "local",
	      "command": ["engram-server"],
	      "args": ["--port", "9000"]
	    }
	  }
	}`
	if err := os.WriteFile(configPath, []byte(legacy), 0o644); err != nil {
		t.Fatalf("WriteFile(opencode.json) error = %v", err)
	}

	_, err := Inject(home, home, adapter)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	assertContext7FileUnchanged(t, configPath, legacy)

	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(opencode.json) error = %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatalf("Unmarshal(opencode.json) error = %v", err)
	}

	mcp, ok := parsed["mcp"].(map[string]any)
	if !ok {
		t.Fatalf("opencode.json missing object key mcp; got %#v", parsed["mcp"])
	}

	engram, ok := mcp["engram"].(map[string]any)
	if !ok {
		t.Fatalf("opencode.json mcp.engram missing after inject; got %#v", mcp["engram"])
	}
	if engram["type"] != "local" {
		t.Fatalf("mcp.engram.type = %#v; want %q", engram["type"], "local")
	}
	cmd, _ := engram["command"].([]any)
	if len(cmd) == 0 || cmd[0] != "engram-server" {
		t.Fatalf("mcp.engram.command = %#v; want [engram-server ...]", engram["command"])
	}
}

func TestInjectClaudeWritesUserConfigAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	userConfigPath := filepath.Join(home, ".claude.json")
	userConfig := `{"oauthAccount":{"emailAddress":"user@example.com"},"projects":{"/repo":{"allowedTools":[]}},"mcpServers":{"codegraph":{"command":"codegraph","args":["serve","--mcp"]}}}`
	// Seeded intentionally loose: injection must tighten it to 0600.
	if err := os.WriteFile(userConfigPath, []byte(userConfig), 0o644); err != nil {
		t.Fatalf("WriteFile(user config) error = %v", err)
	}

	first, err := Inject(home, home, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject() first error = %v", err)
	}
	if !first.Changed {
		t.Fatalf("Inject() first changed = false")
	}

	afterFirst, err := os.ReadFile(userConfigPath)
	if err != nil {
		t.Fatalf("ReadFile(user config after first) error = %v", err)
	}

	// Loosen the mode: the byte-identical run must still re-tighten 0600, and
	// that mode-only repair is a change (#5022).
	loosened := false
	if runtime.GOOS != "windows" {
		if err := os.Chmod(userConfigPath, 0o644); err != nil {
			t.Fatalf("Chmod(loosen) error = %v", err)
		}
		loosened = true
	}
	second, err := Inject(home, home, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject() second error = %v", err)
	}
	if second.Changed != loosened {
		t.Fatalf("Inject() second changed = %v; want %v (mode-only repair)", second.Changed, loosened)
	}

	// Same bytes and same mode: a true no-op.
	third, err := Inject(home, home, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject() third error = %v", err)
	}
	if third.Changed {
		t.Fatalf("Inject() third changed = true")
	}

	raw, err := os.ReadFile(userConfigPath)
	if err != nil {
		t.Fatalf("ReadFile(user config) error = %v", err)
	}
	if string(raw) != string(afterFirst) {
		t.Fatalf("second Inject() must leave ~/.claude.json byte-identical; got diff")
	}
	if info, statErr := os.Stat(userConfigPath); statErr != nil {
		t.Fatalf("Stat(user config) error = %v", statErr)
	} else if mode := info.Mode().Perm(); runtime.GOOS != "windows" && mode != 0o600 {
		t.Fatalf("~/.claude.json mode = %o; want 0600 (holds the OAuth session)", mode)
	}
	root := map[string]any{}
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("Unmarshal(user config) error = %v", err)
	}
	if _, ok := root["oauthAccount"].(map[string]any); !ok {
		t.Fatalf("oauthAccount must be preserved; got %s", raw)
	}
	if _, ok := root["projects"].(map[string]any); !ok {
		t.Fatalf("projects must be preserved; got %s", raw)
	}
	servers, _ := root["mcpServers"].(map[string]any)
	if codegraph, _ := servers["codegraph"].(map[string]any); codegraph["command"] != "codegraph" {
		t.Fatalf("existing codegraph registration must be preserved; got %#v", servers["codegraph"])
	}
	context7, _ := servers["context7"].(map[string]any)
	if context7["command"] != "npx" {
		t.Fatalf("mcpServers.context7.command = %#v; want npx", context7["command"])
	}
	if args := fmt.Sprintf("%v", context7["args"]); !strings.Contains(args, versions.Context7MCP) {
		t.Fatalf("context7.args = %s; want pinned version %s", args, versions.Context7MCP)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "mcp", "context7.json")); !os.IsNotExist(err) {
		t.Fatalf("Claude Context7 must not be written to ~/.claude/mcp/context7.json; stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".mcp.json")); !os.IsNotExist(err) {
		t.Fatalf("user scope must not create <home>/.mcp.json; stat err = %v", err)
	}
}

// TestInjectClaudeSettingsInertBlockCleanup: the inert settings.json block is
// removed when it only holds the managed context7 entry, and left untouched
// when it carries servers gentle-ai does not manage.
func TestInjectClaudeSettingsInertBlockCleanup(t *testing.T) {
	cases := []struct {
		name           string
		settings       string
		wantKeyRemoved bool
	}{
		{"managed-only block is removed", `{"theme":"dark","mcpServers":{"context7":` + string(DefaultContext7ServerJSON()) + `}}`, true},
		{"foreign block is left alone", `{"theme":"dark","mcpServers":{"context7":{"command":"npx","args":["--","context7-mcp"]},"stranded":{"command":"stranded-server"}}}`, false},
		{"user-authored context7 is left alone", `{"theme":"dark","mcpServers":{"context7":{"command":"my-own-proxy"}}}`, false},
		{"empty block is left alone", `{"theme":"dark","mcpServers":{}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			settingsPath := filepath.Join(home, ".claude", "settings.json")
			if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
				t.Fatalf("MkdirAll(settings dir) error = %v", err)
			}
			if err := os.WriteFile(settingsPath, []byte(tc.settings), 0o644); err != nil {
				t.Fatalf("WriteFile(settings) error = %v", err)
			}

			if _, err := Inject(home, home, claudeAdapter()); err != nil {
				t.Fatalf("Inject() error = %v", err)
			}

			readMCPServersContext7Entry(t, filepath.Join(home, ".claude.json"))

			settingsRaw, err := os.ReadFile(settingsPath)
			if err != nil {
				t.Fatalf("ReadFile(settings) error = %v", err)
			}
			settings := map[string]any{}
			if err := json.Unmarshal(settingsRaw, &settings); err != nil {
				t.Fatalf("Unmarshal(settings) error = %v", err)
			}
			_, hasKey := settings["mcpServers"]
			if tc.wantKeyRemoved && hasKey {
				t.Fatalf("inert mcpServers key must be removed; got %s", settingsRaw)
			}
			if !tc.wantKeyRemoved && !hasKey {
				t.Fatalf("foreign mcpServers block must be left untouched; got %s", settingsRaw)
			}
			if settings["theme"] != "dark" {
				t.Fatalf("settings.theme = %#v; want dark preserved", settings["theme"])
			}
		})
	}
}

// TestInjectClaudeWorkspaceWritesMCPJSONAndIsIdempotent verifies that workspace
// scope (home != workspace) writes Context7 into <project-root>/.mcp.json — the
// file Claude Code loads project-scoped MCP servers from — rather than the
// inert .claude/settings.json key Claude ignores (issue #2213).
func TestInjectClaudeWorkspaceWritesMCPJSONAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	mcpPath := filepath.Join(workspace, ".mcp.json")

	first, err := Inject(home, workspace, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject() first error = %v", err)
	}
	if !first.Changed {
		t.Fatalf("Inject() first changed = false")
	}

	context7 := readMCPServersContext7Entry(t, mcpPath)
	if context7["command"] != "npx" {
		t.Fatalf(".mcp.json mcpServers.context7.command = %#v; want npx", context7["command"])
	}
	if args := fmt.Sprintf("%v", context7["args"]); !strings.Contains(args, versions.Context7MCP) {
		t.Fatalf(".mcp.json context7.args = %s; want pinned version %s", args, versions.Context7MCP)
	}

	second, err := Inject(home, workspace, claudeAdapter())
	if err != nil {
		t.Fatalf("Inject() second error = %v", err)
	}
	if second.Changed {
		t.Fatalf("Inject() second changed = true; want idempotent")
	}

	// The inert settings.json key must never be (re)written for workspace scope.
	if _, err := os.Stat(filepath.Join(workspace, ".claude", "settings.json")); err == nil {
		raw, readErr := os.ReadFile(filepath.Join(workspace, ".claude", "settings.json"))
		if readErr != nil {
			t.Fatalf("ReadFile(settings.json) error = %v", readErr)
		}
		if strings.Contains(string(raw), `"mcpServers"`) {
			t.Fatalf("workspace .claude/settings.json must not carry mcpServers; got %s", raw)
		}
	}
}

// claudePendingApprovalReportingFloor is the first Claude Code CLI release
// whose piped `claude mcp list`/`get` output reports unapproved project
// .mcp.json servers as "Pending approval" (upstream v2.1.154; issue #3969).
var claudePendingApprovalReportingFloor = [3]int{2, 1, 154}

// claudeReportingGate classifies whether the ambient Claude Code CLI's piped
// `mcp list` output can carry the pending-approval reporting contract.
type claudeReportingGate int

const (
	// claudeReportingGateProceed runs the native discovery assertions.
	claudeReportingGateProceed claudeReportingGate = iota
	// claudeReportingGateBelowFloor marks a recognized CLI older than
	// v2.1.154, whose piped output omits unapproved project servers.
	claudeReportingGateBelowFloor
	// claudeReportingGateUnrecognized marks malformed or unavailable
	// `claude --version` output.
	claudeReportingGateUnrecognized
)

func (gate claudeReportingGate) skipReason() string {
	switch gate {
	case claudeReportingGateBelowFloor:
		return "claude CLI predates v2.1.154; piped `claude mcp list` omits unapproved project servers (unsupported reporting contract)"
	case claudeReportingGateUnrecognized:
		return "claude --version output not recognized; cannot confirm the pending-approval reporting contract"
	}
	return ""
}

// parseClaudeVersion extracts the first numeric x.y.z token from
// `claude --version` output, e.g. "2.1.114 (Claude Code)".
func parseClaudeVersion(versionOutput string) ([3]int, bool) {
	for field := range strings.FieldsSeq(versionOutput) {
		parts := strings.Split(field, ".")
		if len(parts) != 3 {
			continue
		}
		version := [3]int{}
		ok := true
		for i, part := range parts {
			number, err := strconv.Atoi(part)
			if err != nil {
				ok = false
				break
			}
			version[i] = number
		}
		if ok {
			return version, true
		}
	}
	return [3]int{}, false
}

// versionAtLeast compares two x.y.z tuples lexicographically.
func versionAtLeast(version, floor [3]int) bool {
	for i := range floor {
		if version[i] != floor[i] {
			return version[i] > floor[i]
		}
	}
	return true
}

// classifyClaudeReportingGate decides from `claude --version` output whether
// the CLI's piped mcp list output can report unapproved project .mcp.json
// servers ("Pending approval", upstream v2.1.154+).
func classifyClaudeReportingGate(versionOutput string) claudeReportingGate {
	version, ok := parseClaudeVersion(versionOutput)
	if !ok {
		return claudeReportingGateUnrecognized
	}
	if !versionAtLeast(version, claudePendingApprovalReportingFloor) {
		return claudeReportingGateBelowFloor
	}
	return claudeReportingGateProceed
}

// TestClassifyClaudeReportingGate proves the deterministic version gate for
// the native discovery test: only Claude Code CLI v2.1.154 and newer report
// unapproved project .mcp.json servers as "Pending approval" in piped
// `claude mcp list` output (issue #3969).
func TestClassifyClaudeReportingGate(t *testing.T) {
	tests := []struct {
		name        string
		versionOut  string
		want        claudeReportingGate
		wantSkipMsg bool
	}{
		{
			name:        "below floor omits unapproved project servers",
			versionOut:  "2.1.114 (Claude Code)\n",
			want:        claudeReportingGateBelowFloor,
			wantSkipMsg: true,
		},
		{
			name:        "older major below floor",
			versionOut:  "1.0.87 (Claude Code)",
			want:        claudeReportingGateBelowFloor,
			wantSkipMsg: true,
		},
		{
			name:       "exact floor reports pending approval",
			versionOut: "2.1.154 (Claude Code)",
			want:       claudeReportingGateProceed,
		},
		{
			name:       "above floor reports pending approval",
			versionOut: "2.2.0 (Claude Code)",
			want:       claudeReportingGateProceed,
		},
		{
			name:        "two-component version is unrecognized",
			versionOut:  "2.1 (Claude Code)",
			want:        claudeReportingGateUnrecognized,
			wantSkipMsg: true,
		},
		{
			name:        "malformed output has no version token",
			versionOut:  "Claude Code (unknown)",
			want:        claudeReportingGateUnrecognized,
			wantSkipMsg: true,
		},
		{
			name:        "unavailable output is unrecognized",
			versionOut:  "",
			want:        claudeReportingGateUnrecognized,
			wantSkipMsg: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyClaudeReportingGate(tt.versionOut)
			if got != tt.want {
				t.Fatalf("classifyClaudeReportingGate(%q) = %v; want %v", tt.versionOut, got, tt.want)
			}
			if tt.wantSkipMsg && !strings.Contains(got.skipReason(), "reporting contract") {
				t.Fatalf("skipReason() for %q must name the unsupported reporting contract; got %q", tt.versionOut, got.skipReason())
			}
		})
	}
}

// TestInjectClaudeWorkspaceIsDiscoveredByNativeClaudeMCPList proves the
// project-scope contract through Claude Code itself, without approving the
// pending project server.
func TestInjectClaudeWorkspaceIsDiscoveredByNativeClaudeMCPList(t *testing.T) {
	if testing.Short() {
		t.Skip("requires the native Claude Code CLI")
	}

	claudePath, err := exec.LookPath("claude")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			t.Skip("native Claude Code CLI is not installed")
		}
		t.Fatalf("LookPath(claude) error = %v", err)
	}

	// The pending-approval reporting contract only exists since Claude Code
	// v2.1.154 (issue #3969): older CLIs silently omit unapproved project
	// servers from piped `claude mcp list` output.
	versionOutput, versionErr := exec.Command(claudePath, "--version").CombinedOutput()
	if versionErr != nil {
		t.Skipf("claude --version error = %v; %s", versionErr, claudeReportingGateUnrecognized.skipReason())
	}
	if gate := classifyClaudeReportingGate(string(versionOutput)); gate != claudeReportingGateProceed {
		t.Skipf("claude --version = %q: %s", strings.TrimSpace(string(versionOutput)), gate.skipReason())
	}

	home := t.TempDir()
	workspace := t.TempDir()
	if _, err := Inject(home, workspace, claudeAdapter()); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	cmd := exec.Command(claudePath, "mcp", "list")
	cmd.Dir = workspace
	env := os.Environ()
	homeReplaced := false
	for index, value := range env {
		if strings.HasPrefix(value, "HOME=") {
			env[index] = "HOME=" + home
			homeReplaced = true
			break
		}
	}
	if !homeReplaced {
		env = append(env, "HOME="+home)
	}
	cmd.Env = env

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude mcp list error = %v\noutput:\n%s", err, output)
	}
	t.Logf("native Claude project-scope discovery:\n%s", output)
	lowerOutput := strings.ToLower(string(output))
	if !strings.Contains(lowerOutput, "context7") {
		t.Fatalf("claude mcp list did not discover context7 from %q\noutput:\n%s", filepath.Join(workspace, ".mcp.json"), output)
	}
	if !strings.Contains(lowerOutput, "pending") && !strings.Contains(lowerOutput, "approval") {
		t.Fatalf("claude mcp list did not report the unapproved project server\noutput:\n%s", output)
	}
}

func TestInjectClaudeWorkspacePreservesMCPReadErrors(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	mcpPath := filepath.Join(workspace, ".mcp.json")
	if err := os.Mkdir(mcpPath, 0o755); err != nil {
		t.Fatalf("Mkdir(.mcp.json) error = %v", err)
	}

	_, err := Inject(home, workspace, claudeAdapter())
	if err == nil {
		t.Fatal("Inject() error = nil; want non-not-exist read error")
	}
	if !strings.Contains(err.Error(), mcpPath) {
		t.Fatalf("Inject() error = %v; want path %q", err, mcpPath)
	}
}

// TestInjectClaudeWorkspacePreservesUnrelatedServersAndConfig verifies that
// merging context7 into <project-root>/.mcp.json preserves user-authored
// servers and top-level config outside the managed entry (issue #2213).
func TestInjectClaudeWorkspacePreservesUnrelatedServersAndConfig(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	mcpPath := filepath.Join(workspace, ".mcp.json")
	if err := os.MkdirAll(filepath.Dir(mcpPath), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}

	existing := `{
  "mcpServers": {
    "codegraph": {
      "command": "codegraph",
      "args": ["serve", "--mcp"]
    }
  },
  "enableAllProjectMcpServers": true
}`
	if err := os.WriteFile(mcpPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("WriteFile(.mcp.json) error = %v", err)
	}

	if _, err := Inject(home, workspace, claudeAdapter()); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	raw, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("ReadFile(.mcp.json) error = %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("Unmarshal(.mcp.json) error = %v", err)
	}
	servers, _ := parsed["mcpServers"].(map[string]any)
	if codegraph, _ := servers["codegraph"].(map[string]any); codegraph["command"] != "codegraph" {
		t.Fatalf("unrelated codegraph server must be preserved; got %#v", servers["codegraph"])
	}
	if context7, _ := servers["context7"].(map[string]any); context7["command"] != "npx" {
		t.Fatalf("context7 must be merged; got %#v", servers["context7"])
	}
	if parsed["enableAllProjectMcpServers"] != true {
		t.Fatalf("unrelated top-level config must be preserved; got %#v", parsed["enableAllProjectMcpServers"])
	}
}

// TestInjectClaudeWorkspaceCleansInertSettingsBlock verifies workspace cleanup.
func TestInjectClaudeWorkspaceCleansInertSettingsBlock(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	settingsPath := filepath.Join(workspace, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	for _, tc := range []struct {
		name, settings string
		wantKey        bool
	}{
		{"managed-only", `{"theme":"dark","mcpServers":{"context7":` + string(DefaultContext7ServerJSON()) + `}}`, false},
		{"foreign", `{"theme":"dark","mcpServers":{"context7":{"command":"npx","args":["--","context7-mcp"]},"stranded":{"command":"stranded-server"}}}`, true},
		{"empty", `{"theme":"dark","mcpServers":{}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(settingsPath, []byte(tc.settings), 0o644); err != nil {
				t.Fatalf("WriteFile(settings) error = %v", err)
			}
			if _, err := Inject(home, workspace, claudeAdapter()); err != nil {
				t.Fatalf("Inject() error = %v", err)
			}
			readMCPServersContext7Entry(t, filepath.Join(workspace, ".mcp.json"))
			raw, err := os.ReadFile(settingsPath)
			if err != nil {
				t.Fatalf("ReadFile(settings) error = %v", err)
			}
			if got := strings.Contains(string(raw), `"mcpServers"`); got != tc.wantKey {
				t.Fatalf("mcpServers presence = %t, want %t; got %s", got, tc.wantKey, raw)
			}
		})
	}
}

func TestInjectClaudeRefusesCorruptUserConfig(t *testing.T) {
	home := t.TempDir()
	userConfigPath := filepath.Join(home, ".claude.json")
	corrupt := []byte("{ this is not json")
	if err := os.WriteFile(userConfigPath, corrupt, 0o600); err != nil {
		t.Fatalf("WriteFile(corrupt user config) error = %v", err)
	}
	if _, err := Inject(home, home, claudeAdapter()); err == nil {
		t.Fatalf("Inject() error = nil; want refusal on corrupt ~/.claude.json")
	}
	after, err := os.ReadFile(userConfigPath)
	if err != nil {
		t.Fatalf("ReadFile(user config) error = %v", err)
	}
	if string(after) != string(corrupt) {
		t.Fatalf("corrupt ~/.claude.json must be left byte-identical; got %s", after)
	}
}

// TestInjectCodexContext7TOML verifies that Context7 injection for Codex
// (StrategyTOMLFile) creates config.toml with [mcp_servers.context7] block.
func TestInjectCodexContext7TOML(t *testing.T) {
	home := t.TempDir()

	result, err := Inject(home, home, codex.NewAdapter())
	if err != nil {
		t.Fatalf("Inject(codex) error = %v", err)
	}
	if !result.Changed {
		t.Fatal("Inject(codex) first call changed = false; want true")
	}
	if len(result.Files) == 0 {
		t.Fatal("Inject(codex) files is empty; want config.toml path")
	}

	configTOML := filepath.Join(home, ".codex", "config.toml")
	if result.Files[0] != configTOML {
		t.Fatalf("Inject(codex) files[0] = %q; want %q", result.Files[0], configTOML)
	}

	content, err := os.ReadFile(configTOML)
	if err != nil {
		t.Fatalf("ReadFile(config.toml) error = %v", err)
	}
	text := string(content)

	if !strings.Contains(text, "[mcp_servers.context7]") {
		t.Fatalf("config.toml missing [mcp_servers.context7]; got:\n%s", text)
	}
	if !strings.Contains(text, `url = "https://mcp.context7.com/mcp"`) {
		t.Fatalf("config.toml missing Context7 remote URL; got:\n%s", text)
	}
	if strings.Contains(text, `command = "npx"`) || strings.Contains(text, "context7-mcp") {
		t.Fatalf("config.toml should use remote Context7 MCP, not local npx; got:\n%s", text)
	}
}

// TestInjectCodexContext7Idempotent verifies that a second Inject call with
// the same pinned version is a no-op (Changed == false, single block).
func TestInjectCodexContext7Idempotent(t *testing.T) {
	home := t.TempDir()

	first, err := Inject(home, home, codex.NewAdapter())
	if err != nil {
		t.Fatalf("Inject(codex) first error = %v", err)
	}
	if !first.Changed {
		t.Fatal("Inject(codex) first changed = false; want true")
	}

	second, err := Inject(home, home, codex.NewAdapter())
	if err != nil {
		t.Fatalf("Inject(codex) second error = %v", err)
	}
	if second.Changed {
		t.Fatal("Inject(codex) second changed = true; want false (idempotent)")
	}

	configTOML := filepath.Join(home, ".codex", "config.toml")
	content, err := os.ReadFile(configTOML)
	if err != nil {
		t.Fatalf("ReadFile(config.toml) error = %v", err)
	}
	count := strings.Count(string(content), "[mcp_servers.context7]")
	if count != 1 {
		t.Fatalf("config.toml has %d [mcp_servers.context7] blocks; want exactly 1", count)
	}
}

// TestInjectCodexContext7CoexistsWithEngram verifies that injecting context7
// into a config.toml that already has [mcp_servers.engram] preserves both
// blocks and does not duplicate either.
func TestInjectCodexContext7CoexistsWithEngram(t *testing.T) {
	home := t.TempDir()

	// Pre-seed config.toml with an engram block.
	configTOML := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configTOML), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	existing := `[mcp_servers.engram]
command = "engram"
args = ["mcp", "--tools=agent"]
`
	if err := os.WriteFile(configTOML, []byte(existing), 0o644); err != nil {
		t.Fatalf("WriteFile(config.toml) error = %v", err)
	}

	_, err := Inject(home, home, codex.NewAdapter())
	if err != nil {
		t.Fatalf("Inject(codex) error = %v", err)
	}

	content, err := os.ReadFile(configTOML)
	if err != nil {
		t.Fatalf("ReadFile(config.toml) error = %v", err)
	}
	text := string(content)

	engramCount := strings.Count(text, "[mcp_servers.engram]")
	if engramCount != 1 {
		t.Fatalf("expected 1 [mcp_servers.engram], got %d; result:\n%s", engramCount, text)
	}

	context7Count := strings.Count(text, "[mcp_servers.context7]")
	if context7Count != 1 {
		t.Fatalf("expected 1 [mcp_servers.context7], got %d; result:\n%s", context7Count, text)
	}
}

func TestInjectCodexContext7ReplacesLegacyLocalBlock(t *testing.T) {
	home := t.TempDir()
	configTOML := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configTOML), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	existing := `[mcp_servers.context7]
command = "npx"
args = ["-y", "--package=@upstash/context7-mcp@2.2.5", "--", "context7-mcp"]

[mcp_servers.engram]
command = "engram"
args = ["mcp", "--tools=agent"]
`
	if err := os.WriteFile(configTOML, []byte(existing), 0o644); err != nil {
		t.Fatalf("WriteFile(config.toml) error = %v", err)
	}

	result, err := Inject(home, home, codex.NewAdapter())
	if err != nil {
		t.Fatalf("Inject(codex) error = %v", err)
	}
	if !result.Changed {
		t.Fatal("Inject(codex) changed = false; expected legacy local block migration")
	}

	content, err := os.ReadFile(configTOML)
	if err != nil {
		t.Fatalf("ReadFile(config.toml) error = %v", err)
	}
	text := string(content)

	if count := strings.Count(text, "[mcp_servers.context7]"); count != 1 {
		t.Fatalf("expected 1 [mcp_servers.context7], got %d; result:\n%s", count, text)
	}
	if !strings.Contains(text, `url = "https://mcp.context7.com/mcp"`) {
		t.Fatalf("config.toml missing remote Context7 URL after migration; got:\n%s", text)
	}
	if strings.Contains(text, `command = "npx"`) || strings.Contains(text, "context7-mcp") {
		t.Fatalf("legacy local Context7 config survived migration; got:\n%s", text)
	}
	if !strings.Contains(text, "[mcp_servers.engram]") {
		t.Fatalf("engram block was not preserved; got:\n%s", text)
	}
}

func TestInjectCodexContext7PreservesHeaderInsideMultilineString(t *testing.T) {
	home := t.TempDir()
	configTOML := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configTOML), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	instructions := `developer_instructions = '''
Example config:
[mcp_servers.context7]
command = "fake"
Keep this text.
'''`
	existing := instructions + `

[mcp_servers.context7]
command = "npx"
args = ["-y", "context7-mcp"]
`
	if err := os.WriteFile(configTOML, []byte(existing), 0o644); err != nil {
		t.Fatalf("WriteFile(config.toml) error = %v", err)
	}

	if _, err := Inject(home, home, codex.NewAdapter()); err != nil {
		t.Fatalf("Inject(codex) first error = %v", err)
	}
	second, err := Inject(home, home, codex.NewAdapter())
	if err != nil {
		t.Fatalf("Inject(codex) second error = %v", err)
	}
	if second.Changed {
		t.Fatal("Inject(codex) second changed = true (should be idempotent)")
	}

	content, err := os.ReadFile(configTOML)
	if err != nil {
		t.Fatalf("ReadFile(config.toml) error = %v", err)
	}
	want := existing
	if got := string(content); got != want {
		t.Fatalf("config.toml mismatch:\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestMergeJSONFilePreservesExistingModeOnRewrite is the representative
// end-to-end regression test for gentle-ai#5006(F5): mergeJSONFile is the
// settings-merge codepath shared by every non-OpenCode/OpenClaw MCP
// injection, and rewriting a pre-seeded private settings file must never
// widen it.
func TestMergeJSONFilePreservesExistingModeOnRewrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"already":"set"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := mergeJSONFile(path, DefaultContext7OverlayJSON()); err != nil {
		t.Fatalf("mergeJSONFile() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode after mergeJSONFile = %v, want 0600 preserved", got)
	}
}
