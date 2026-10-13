package communitytool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/BurntSushi/toml"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/system"
)

// codeGraphUpstreamVersion is the minimum CodeGraph release Brunnr-Code is
// written against. An installed CLI below it is upgraded with `codegraph
// upgrade` before any targeted `codegraph install`.
const codeGraphUpstreamVersion = "1.6.2"

type codeGraphStrategy string

const (
	codeGraphNative   codeGraphStrategy = "native"
	codeGraphExcluded codeGraphStrategy = "excluded"
)

type codeGraphCompatibility struct {
	Agent         model.AgentID
	Strategy      codeGraphStrategy
	Target        string
	OwnedPaths    func(string, agents.Adapter) []string
	Postcondition func(string, agents.Adapter) (string, bool)
}

// codeGraphCompatibilityTable is deliberately exhaustive. Tests compare its
// keys with the registry so a newly added agent cannot silently enter setup.
var codeGraphCompatibilityTable = map[model.AgentID]codeGraphCompatibility{
	model.AgentClaudeCode: nativeCompatibility(model.AgentClaudeCode, "claude"),
	model.AgentCodex:      nativeCompatibility(model.AgentCodex, "codex"),
}

func nativeCompatibility(id model.AgentID, target string) codeGraphCompatibility {
	return codeGraphCompatibility{Agent: id, Strategy: codeGraphNative, Target: target, OwnedPaths: codeGraphOwnedPaths, Postcondition: codeGraphPostcondition}
}

func codeGraphCompatibilityFor(id model.AgentID) (codeGraphCompatibility, bool) {
	compatibility, ok := codeGraphCompatibilityTable[id]
	return compatibility, ok
}

func isCodeGraphCompatibleAgent(id model.AgentID) bool {
	compatibility, ok := codeGraphCompatibilityFor(id)
	return ok && compatibility.Strategy != codeGraphExcluded
}

func codeGraphOwnedPaths(homeDir string, adapter agents.Adapter) []string {
	paths := codeGraphToolWiringPaths(homeDir, adapter)
	if adapter.SupportsSystemPrompt() {
		paths = append(paths, adapter.SystemPromptFile(homeDir))
	}
	return paths
}

func codeGraphPostcondition(homeDir string, adapter agents.Adapter) (string, bool) {
	return hasCodeGraphToolWiring(homeDir, adapter)
}

func codeGraphToolWiringPaths(homeDir string, adapter agents.Adapter) []string {
	switch adapter.Agent() {
	case model.AgentClaudeCode:
		return []string{claudeCodeGraphConfigPath(homeDir)}
	case model.AgentCodex:
		return []string{codexCodeGraphConfigPath(homeDir)}
	default:
		return nil
	}
}

func claudeCodeGraphConfigPath(homeDir string) string {
	return filepath.Join(homeDir, ".claude.json")
}

// codexCodeGraphConfigPath follows upstream `codegraph install --target codex
// --location global`, which writes $CODEX_HOME/config.toml and falls back to
// ~/.codex. CODEX_HOME only applies to the real user home, matching the
// environment Brunnr hands the upstream CLI for isolated homes.
func codexCodeGraphConfigPath(homeDir string) string {
	return filepath.Join(system.CodexConfigDir(homeDir), "config.toml")
}

// hasCodeGraphToolWiring reports whether the agent's global config holds the
// MCP server entry upstream v1.6.2 writes. An outdated or hand-edited entry is
// reported as not configured, so install re-runs the targeted upstream install.
func hasCodeGraphToolWiring(homeDir string, adapter agents.Adapter) (string, bool) {
	switch adapter.Agent() {
	case model.AgentClaudeCode:
		return readCodeGraphWiring(claudeCodeGraphConfigPath(homeDir), hasCanonicalCodeGraphServer)
	case model.AgentCodex:
		return readCodeGraphWiring(codexCodeGraphConfigPath(homeDir), hasCanonicalCodexCodeGraphServer)
	}
	if detector, ok := adapter.(agents.EffectiveCodeGraphWiringDetector); ok {
		return detector.EffectiveCodeGraphWiring(homeDir)
	}
	return "", false
}

func readCodeGraphWiring(path string, valid func([]byte) bool) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil || !valid(data) {
		return "", false
	}
	return path, true
}

// codeGraphMCPServer is the server entry upstream writes for every agent
// (src/installer/targets/shared.ts getMcpServerConfig): the bare command
// `codegraph` with args `serve --mcp`. Upstream never writes an absolute
// command path, so only the bare name counts. Agent-specific extras such as
// Claude's alwaysLoad stay user-editable and are not checked.
type codeGraphMCPServer struct {
	Command string   `json:"command" toml:"command"`
	Args    []string `json:"args" toml:"args"`
}

func (s codeGraphMCPServer) canonical() bool {
	if s.Command != "codegraph" {
		return false
	}
	for i := 0; i+1 < len(s.Args); i++ {
		if s.Args[i] == "serve" && s.Args[i+1] == "--mcp" {
			return true
		}
	}
	return false
}

func hasCanonicalCodeGraphServer(data []byte) bool {
	var config struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(data, &config) != nil {
		return false
	}
	raw, ok := config.MCPServers["codegraph"]
	var server *codeGraphMCPServer
	return ok && json.Unmarshal(raw, &server) == nil && server != nil && server.canonical()
}

// hasCanonicalCodexCodeGraphServer parses config.toml, so a comment, a string
// value or another table that merely mentions codegraph never counts.
func hasCanonicalCodexCodeGraphServer(data []byte) bool {
	var config struct {
		MCPServers map[string]toml.Primitive `toml:"mcp_servers"`
	}
	meta, err := toml.Decode(string(data), &config)
	if err != nil {
		return false
	}
	// Decode only the codegraph entry, so another server's unusual shape
	// cannot hide a valid one.
	raw, ok := config.MCPServers["codegraph"]
	var server codeGraphMCPServer
	return ok && meta.PrimitiveDecode(raw, &server) == nil && server.canonical()
}

func detectedCodeGraphTargets(homeDir string) []string {
	reg, err := agents.NewDefaultRegistry()
	if err != nil {
		return nil
	}
	targets := make([]string, 0)
	for _, installed := range agents.DiscoverSelected(reg, homeDir) {
		compatibility, ok := codeGraphCompatibilityFor(installed.ID)
		if ok && compatibility.Strategy == codeGraphNative && compatibility.Target != "" {
			targets = append(targets, compatibility.Target)
		}
	}
	slices.Sort(targets)
	return slices.Compact(targets)
}

type codeGraphSnapshot struct {
	path   string
	data   []byte
	mode   os.FileMode
	exists bool
}

func snapshotCodeGraphPaths(paths []string) ([]codeGraphSnapshot, error) {
	snapshots := make([]codeGraphSnapshot, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			snapshots = append(snapshots, codeGraphSnapshot{path: path})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("snapshot CodeGraph path %q: %w", path, err)
		}
		if info.IsDir() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("snapshot CodeGraph path %q: %w", path, err)
		}
		snapshots = append(snapshots, codeGraphSnapshot{path: path, data: data, mode: info.Mode(), exists: true})
	}
	return snapshots, nil
}

func restoreCodeGraphPaths(snapshots []codeGraphSnapshot) error {
	var restoreErr error
	for _, snapshot := range snapshots {
		if !snapshot.exists {
			if err := os.Remove(snapshot.path); err != nil && !os.IsNotExist(err) {
				restoreErr = fmt.Errorf("remove candidate-created CodeGraph file %q: %w", snapshot.path, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(snapshot.path), 0o755); err != nil {
			restoreErr = err
			continue
		}
		if err := os.WriteFile(snapshot.path, snapshot.data, snapshot.mode.Perm()); err != nil {
			restoreErr = err
			continue
		}
		if err := os.Chmod(snapshot.path, snapshot.mode); err != nil {
			restoreErr = err
		}
	}
	return restoreErr
}
