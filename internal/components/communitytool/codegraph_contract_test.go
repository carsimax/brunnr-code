package communitytool

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"testing"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/model"
)

func TestCodeGraphCompatibilityTableIsExhaustive(t *testing.T) {
	reg, err := agents.NewDefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	want := reg.SupportedAgents()
	got := make([]model.AgentID, 0, len(codeGraphCompatibilityTable))
	for id, compatibility := range codeGraphCompatibilityTable {
		got = append(got, id)
		if compatibility.Agent != id {
			t.Errorf("entry %q owns agent %q", id, compatibility.Agent)
		}
		if compatibility.Strategy == codeGraphExcluded {
			if compatibility.Target != "" || compatibility.OwnedPaths != nil || compatibility.Postcondition != nil {
				t.Errorf("excluded agent %q owns CodeGraph behavior", id)
			}
		} else if compatibility.OwnedPaths == nil || compatibility.Postcondition == nil {
			t.Errorf("compatible agent %q lacks owned path/postcondition semantics", id)
		}
	}
	slices.Sort(want)
	slices.Sort(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("compatibility agents = %v, want registry agents %v", got, want)
	}
	if codeGraphUpstreamVersion != "1.6.2" {
		t.Fatalf("upstream contract = %q", codeGraphUpstreamVersion)
	}
}

func TestCodeGraphCompatibilityStrategies(t *testing.T) {
	tests := []struct {
		strategy codeGraphStrategy
		agents   []model.AgentID
	}{}
	for _, tt := range tests {
		for _, id := range tt.agents {
			if got := codeGraphCompatibilityTable[id].Strategy; got != tt.strategy {
				t.Errorf("%s strategy = %s, want %s", id, got, tt.strategy)
			}
		}
	}
}

// Upstream v1.6.2 writes `{"type":"stdio","command":"codegraph","args":["serve","--mcp"],"alwaysLoad":true}`
// (src/installer/targets/shared.ts getMcpServerConfig, claude.ts). The command is always the bare
// name, so only that spelling counts; alwaysLoad and type stay user-editable.
func TestClaudeGlobalConfigRequiresUpstreamServeMCPEntry(t *testing.T) {
	home := t.TempDir()
	reg, _ := agents.NewDefaultRegistry()
	adapter, _ := reg.Get(model.AgentClaudeCode)
	path := filepath.Join(home, ".claude.json")
	for _, tt := range []struct {
		name, data string
		want       bool
	}{
		{"upstream entry", `{"theme":"dark","mcpServers":{"other":{"command":"x"},"codegraph":{"type":"stdio","command":"codegraph","args":["serve","--mcp"],"alwaysLoad":true}}}`, true},
		{"alwaysLoad turned off", `{"mcpServers":{"codegraph":{"command":"codegraph","args":["serve","--mcp"],"alwaysLoad":false}}}`, true},
		{"extra trailing args", `{"mcpServers":{"codegraph":{"command":"codegraph","args":["serve","--mcp","--verbose"]}}}`, true},
		{"missing args", `{"mcpServers":{"codegraph":{"command":"codegraph"}}}`, false},
		{"serve without mcp flag", `{"mcpServers":{"codegraph":{"command":"codegraph","args":["serve"]}}}`, false},
		{"flags out of order", `{"mcpServers":{"codegraph":{"command":"codegraph","args":["--mcp","serve"]}}}`, false},
		{"flags not adjacent", `{"mcpServers":{"codegraph":{"command":"codegraph","args":["serve","--stdio","--mcp"]}}}`, false},
		{"legacy subcommand", `{"mcpServers":{"codegraph":{"command":"codegraph","args":["mcp"]}}}`, false},
		{"args not an array", `{"mcpServers":{"codegraph":{"command":"codegraph","args":"serve --mcp"}}}`, false},
		{"absolute command path", `{"mcpServers":{"codegraph":{"command":"/usr/local/bin/codegraph","args":["serve","--mcp"]}}}`, false},
		{"missing key", `{"mcpServers":{"other":{"command":"codegraph","args":["serve","--mcp"]}}}`, false},
		{"null entry", `{"mcpServers":{"codegraph":null}}`, false},
		{"malformed entry", `{"mcpServers":{"codegraph":"codegraph"}}`, false},
		{"wrong command", `{"mcpServers":{"codegraph":{"command":"other","args":["serve","--mcp"]}}}`, false},
		{"invalid JSON", `{`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mustWrite(t, path, tt.data)
			_, got := hasCodeGraphToolWiring(home, adapter)
			if got != tt.want {
				t.Fatalf("configured = %v, want %v", got, tt.want)
			}
		})
	}
}

// Upstream v1.6.2 writes the `[mcp_servers.codegraph]` table into
// $CODEX_HOME/config.toml (src/installer/targets/codex.ts, toml.ts).
func TestCodexConfigRequiresUpstreamServeMCPTable(t *testing.T) {
	home := t.TempDir()
	reg, _ := agents.NewDefaultRegistry()
	adapter, _ := reg.Get(model.AgentCodex)
	path := filepath.Join(home, ".codex", "config.toml")
	for _, tt := range []struct {
		name, data string
		want       bool
	}{
		{"upstream table", "model = \"o3\"\n\n[mcp_servers.codegraph]\ncommand = \"codegraph\"\nargs = [\"serve\", \"--mcp\"]\n", true},
		{"inline table form", "[mcp_servers]\ncodegraph = { command = \"codegraph\", args = [\"serve\", \"--mcp\"] }\n", true},
		{"extra keys", "[mcp_servers.codegraph]\ncommand = \"codegraph\"\nargs = [\"serve\", \"--mcp\"]\nstartup_timeout_sec = 20\n", true},
		{"missing args", "[mcp_servers.codegraph]\ncommand = \"codegraph\"\n", false},
		{"wrong args", "[mcp_servers.codegraph]\ncommand = \"codegraph\"\nargs = [\"mcp\"]\n", false},
		{"wrong command", "[mcp_servers.codegraph]\ncommand = \"npx\"\nargs = [\"serve\", \"--mcp\"]\n", false},
		{"header only", "[mcp_servers.codegraph]\n", false},
		{"comment mentions codegraph", "# [mcp_servers.codegraph]\n# command = \"codegraph\"\n# args = [\"serve\", \"--mcp\"]\n", false},
		{"unrelated table mentions codegraph", "[mcp_servers.other]\ncommand = \"codegraph\"\nargs = [\"serve\", \"--mcp\"]\n", false},
		{"string value mentions codegraph", "notes = \"install codegraph serve --mcp\"\n", false},
		{"malformed TOML", "[mcp_servers.codegraph\ncommand = \"codegraph\"\nargs = [\"serve\", \"--mcp\"]\n", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mustWrite(t, path, tt.data)
			gotPath, got := hasCodeGraphToolWiring(home, adapter)
			if got != tt.want {
				t.Fatalf("configured = %v, want %v", got, tt.want)
			}
			if got && gotPath != path {
				t.Fatalf("path = %q, want %q", gotPath, path)
			}
		})
	}
	t.Run("missing file", func(t *testing.T) {
		if _, got := hasCodeGraphToolWiring(t.TempDir(), adapter); got {
			t.Fatal("configured without a config.toml")
		}
	})
}

// An isolated home must never read the ambient CODEX_HOME; upstream only
// honors it for the real user, and Brunnr redirects it into the isolated home.
func TestCodexWiringIgnoresAmbientCodexHomeForIsolatedHomes(t *testing.T) {
	ambient := t.TempDir()
	mustWrite(t, filepath.Join(ambient, "config.toml"), "[mcp_servers.codegraph]\ncommand = \"codegraph\"\nargs = [\"serve\", \"--mcp\"]\n")
	t.Setenv("CODEX_HOME", ambient)
	reg, _ := agents.NewDefaultRegistry()
	adapter, _ := reg.Get(model.AgentCodex)
	home := t.TempDir()
	if _, got := hasCodeGraphToolWiring(home, adapter); got {
		t.Fatal("isolated home adopted the ambient CODEX_HOME config")
	}
	if got, want := codeGraphToolWiringPaths(home, adapter), []string{filepath.Join(home, ".codex", "config.toml")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("codex wiring paths = %v, want %v", got, want)
	}
}

func TestCodeGraphNativeOwnedPaths(t *testing.T) {
	home := t.TempDir()
	reg, _ := agents.NewDefaultRegistry()
	tests := map[model.AgentID][]string{
		model.AgentClaudeCode: {filepath.Join(home, ".claude.json")},
	}
	for id, want := range tests {
		adapter, _ := reg.Get(id)
		got := codeGraphToolWiringPaths(home, adapter)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s paths = %v, want %v", id, got, want)
		}
	}
}

func TestCodeGraphCommandsUseExplicitMultipleTargets(t *testing.T) {
	commands, err := CodeGraphCommandsForDetectorAndTargets(DetectorFunc(func(name string) (string, error) {
		if name == "npm" {
			return "/bin/npm", nil
		}
		return "", errors.New("missing")
	}), []string{"claude", "codex", "cursor"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"codegraph", "install", "--target", "claude,codex,cursor", "--location", "global", "--yes"}
	if !reflect.DeepEqual(commands[1], want) {
		t.Fatalf("install command = %v, want %v", commands[1], want)
	}
}

func TestCodeGraphRollbackRestoresBytesModesAndRemovesCreatedFiles(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(home, ".claude.json")
	prompt := filepath.Join(home, ".claude", "CLAUDE.md")
	mustWrite(t, global, `{"theme":"dark"}`)
	if err := os.Chmod(global, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := InstallWithHome(model.CommunityToolCodeGraph, "", home, RunnerFunc(func(string, ...string) error {
		mustWrite(t, global, `{"mcpServers":{"codegraph":{"command":"codegraph"}}}`)
		mustWrite(t, prompt, "created")
		return errors.New("upstream failed")
	}), DetectorFunc(func(string) (string, error) { return "/bin/codegraph", nil }))
	if err == nil {
		t.Fatal("InstallWithHome() error = nil")
	}
	data, _ := os.ReadFile(global)
	info, _ := os.Stat(global)
	if string(data) != `{"theme":"dark"}` || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("rollback global = %q mode %o", data, info.Mode().Perm())
	}
	if _, err := os.Stat(prompt); !os.IsNotExist(err) {
		t.Fatalf("candidate-created prompt remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); err != nil {
		t.Fatalf("rollback deleted parent directory: %v", err)
	}
}

func TestCodeGraphRollbackAfterGuidanceAndPostconditionFailures(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(*testing.T, string)
		run   func(*testing.T, string, string, ...string) error
	}{
		{
			name: "guidance",
			setup: func(t *testing.T, home string) {
				if err := os.MkdirAll(filepath.Join(home, ".claude", "CLAUDE.md"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			run: func(t *testing.T, home, _ string, _ ...string) error {
				mustWrite(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"codegraph":{"command":"codegraph"}}}`)
				return nil
			},
		},
		{
			name:  "postcondition",
			setup: func(*testing.T, string) {},
			run:   func(*testing.T, string, string, ...string) error { return nil },
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
				t.Fatal(err)
			}
			global := filepath.Join(home, ".claude.json")
			mustWrite(t, global, `{"sibling":true}`)
			tt.setup(t, home)
			_, err := InstallWithHome(model.CommunityToolCodeGraph, "", home, RunnerFunc(func(name string, args ...string) error {
				return tt.run(t, home, name, args...)
			}), DetectorFunc(func(string) (string, error) { return "/bin/codegraph", nil }))
			if err == nil {
				t.Fatal("InstallWithHome() error = nil")
			}
			data, _ := os.ReadFile(global)
			if string(data) != `{"sibling":true}` {
				t.Fatalf("rollback bytes = %q", data)
			}
		})
	}
}

func TestCodeGraphSnapshotRoundTripIsIdempotent(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude", "settings", "mcp.json")
	mustWrite(t, path, `{"mcpServers":{"sibling":{"command":"x"}}}`)
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	snapshots, err := snapshotCodeGraphPaths([]string{path, filepath.Join(home, "new.json")})
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, path, `{}`)
	mustWrite(t, filepath.Join(home, "new.json"), `{}`)
	if err := restoreCodeGraphPaths(snapshots); err != nil {
		t.Fatal(err)
	}
	if err := restoreCodeGraphPaths(snapshots); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(data) != `{"mcpServers":{"sibling":{"command":"x"}}}` || runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Fatalf("round trip = %q mode %o", data, info.Mode().Perm())
	}
}

func filesBelow(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func TestCodexWiringIgnoresOtherServerShapes(t *testing.T) {
	home := t.TempDir()
	reg, _ := agents.NewDefaultRegistry()
	adapter, _ := reg.Get(model.AgentCodex)
	mustWrite(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.other]\ncommand = 42\n\n[mcp_servers.codegraph]\ncommand = \"codegraph\"\nargs = [\"serve\", \"--mcp\"]\n")
	if _, got := hasCodeGraphToolWiring(home, adapter); !got {
		t.Fatal("an unrelated server's shape hid a valid codegraph entry")
	}
}

// A pre-1.6 or hand-edited Claude entry is not configured, so install runs the
// targeted upstream install that rewrites it instead of reporting it healthy.
func TestInstallRewiresAnOutdatedClaudeEntry(t *testing.T) {
	stubCodeGraphVersionSequence(t, codeGraphUpstreamVersion)
	home := t.TempDir()
	mustWrite(t, filepath.Join(home, ".claude", "settings.json"), `{}`)
	mustWrite(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"codegraph":{"command":"codegraph","args":["mcp"]}}}`)
	if _, err := InjectCodeGraphGuidance(home); err != nil {
		t.Fatal(err)
	}

	result, err := InstallWithHome(model.CommunityToolCodeGraph, "/work/project", home, RunnerFunc(func(string, ...string) error {
		mustWrite(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"codegraph":{"type":"stdio","command":"codegraph","args":["serve","--mcp"],"alwaysLoad":true}}}`)
		return nil
	}), DetectorFunc(func(string) (string, error) { return "/bin/codegraph", nil }))
	if err != nil {
		t.Fatalf("InstallWithHome() error = %v", err)
	}
	if want := []string{"codegraph install --target claude --location global --yes"}; !reflect.DeepEqual(result.CommandsRun, want) {
		t.Fatalf("CommandsRun = %#v, want %#v", result.CommandsRun, want)
	}
}
