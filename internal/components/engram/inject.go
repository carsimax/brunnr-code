package engram

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/agents/claude"
	"github.com/carsimax/brunnr-code/internal/agents/codex"
	"github.com/carsimax/brunnr-code/internal/components/filemerge"
	"github.com/carsimax/brunnr-code/internal/model"
)

type InjectionResult struct {
	Changed bool
	Files   []string
	// CodexServiceTier is non-nil only when this injection wrote a new Codex
	// service_tier or retired the managed one; it is the tier Brunnr-Code owns
	// afterwards ("" = none). Callers record it as the managed tier; nil keeps
	// the previously recorded value.
	CodexServiceTier *string
}

// EngramLookPath is the function used to resolve the engram binary path.
// It is a package-level variable so it can be replaced in tests — both from
// within the engram package and from external test packages (e.g. golden_test.go).
// In production it is set to exec.LookPath.
var EngramLookPath = exec.LookPath

// SetLookPathForTest replaces EngramLookPath with a mock for the duration of
// a test and restores the original after the test completes. Exported so that
// external test packages (e.g. golden_test.go in components) can control the
// resolved engram path.
func SetLookPathForTest(t interface {
	Helper()
	Cleanup(func())
}, result, errMsg string) {
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

// engramServerJSONWithCmd returns the MCP server config bytes for a specific
// command.
func engramServerJSONWithCmd(cmd string) []byte {
	cfg := map[string]any{
		"command": cmd,
		"args":    []string{"mcp", "--tools=agent"},
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return append(b, '\n')
}

// engramOverlayJSON returns the Claude Code user-config overlay JSON with the
// resolved engram command.
func engramOverlayJSON(cmd string) []byte {
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"engram": map[string]any{
				"command": cmd,
				"args":    []string{"mcp", "--tools=agent"},
			},
		},
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return append(b, '\n')
}

// InjectOptions carries optional configuration for an Inject call.
// Zero value is always safe — all fields have documented defaults.
type InjectOptions struct {
	// CodexMultiAgent controls whether features.multi_agent is written as true
	// in ~/.codex/config.toml. Default (false) writes multi_agent = false, which
	// is the safe no-op value for the experimental Codex multi-agent tool set.
	// Set to true only when the user explicitly opts in via a CLI flag or TUI choice.
	CodexMultiAgent bool

	// CodexOrchestratorAssignment updates top-level model settings when non-nil.
	// nil preserves the user's existing main-session configuration.
	CodexOrchestratorAssignment *model.CodexOrchestratorAssignment

	// CodexServiceTier writes the top-level service_tier when it is a new,
	// non-empty selection (different from CodexManagedServiceTier).
	CodexServiceTier string
	// CodexManagedServiceTier is the service_tier Brunnr-Code previously wrote.
	// With no CodexServiceTier selected it is removed only while config.toml
	// still holds exactly that value; any other user value is left untouched.
	CodexManagedServiceTier string

	// CodexCarrilModelAssignments retains saved model choices for ODD workers,
	// keyed by ODD role.
	CodexCarrilModelAssignments map[string]string

	// CodexModelAssignments retains saved effort choices for ODD/RDD routing.
	CodexModelAssignments map[string]model.CodexEffort

	// Version carries the raw installed engram binary version string (e.g.
	// "engram 1.18.0"), as returned by VerifyVersion(). It feeds the
	// Decision 1 version-gate: the Claude Code CLAUDE.md section only
	// renders slim when Version parses to >= v1.4.0. Empty, unknown, or
	// unparseable values fall back to the full section (safe default). A
	// raw string (rather than a bool) is required to support the inclusive
	// at-floor v1.4.0 boundary comparison.
	Version string
}

func Inject(homeDir string, adapter agents.Adapter) (InjectionResult, error) {
	return injectWithOptions(homeDir, homeDir, adapter, InjectOptions{}, true)
}

// InjectWithOptions is like Inject but accepts additional options such as the
// Codex multi-agent opt-in flag. Use this when the caller has a model.Selection
// and needs to forward user-chosen configuration into the injection pass.
func InjectWithOptions(homeDir string, adapter agents.Adapter, opts InjectOptions) (InjectionResult, error) {
	return injectWithOptions(homeDir, homeDir, adapter, opts, true)
}

// InjectWorkspaceWithOptions preserves the existing workspace-scoped delivery.
// Claude's user registry migration is intentionally limited to user scope.
func InjectWorkspaceWithOptions(workspaceDir string, adapter agents.Adapter, opts InjectOptions) (InjectionResult, error) {
	return injectWithOptions(workspaceDir, workspaceDir, adapter, opts, false)
}

func injectWithOptions(configHomeDir, promptDir string, adapter agents.Adapter, opts InjectOptions, userScope bool) (InjectionResult, error) {
	if !adapter.SupportsMCP() {
		return InjectionResult{}, nil
	}

	files := make([]string, 0, 2)
	changed := false
	var codexServiceTier *string

	// 1. Write MCP server config using the adapter's strategy.
	switch adapter.MCPStrategy() {
	case model.StrategySeparateMCPFiles:
		if adapter.Agent() == model.AgentClaudeCode && userScope {
			result, err := injectClaudeUserConfig(configHomeDir, adapter)
			if err != nil {
				return InjectionResult{}, err
			}
			changed = changed || result.Changed
			files = append(files, result.Files...)
			break
		}
		// Engram v1.10.3+ writes an absolute path for the command field when
		// `engram setup <agent>` is invoked. brunnr-code's Inject() runs after
		// engram setup, so we must preserve any absolute command path already
		// present instead of silently overwriting it with the relative "engram".
		// See: https://github.com/carsimax/brunnr-code/issues (engram absolute path regression)
		mcpPath := adapter.MCPConfigPath(configHomeDir, "engram")
		cmd := stableEngramCommandForMergedConfig(mcpPath, adapter.Agent())
		content := buildSeparateMCPContent(mcpPath, engramServerJSONWithCmd(cmd))
		mcpWrite, err := filemerge.WriteFileAtomic(mcpPath, content, 0o644)
		if err != nil {
			return InjectionResult{}, err
		}
		changed = changed || mcpWrite.Changed
		files = append(files, mcpPath)

	case model.StrategyTOMLFile:
		// Codex: upsert [mcp_servers.engram] block and instruction-file keys
		// in ~/.codex/config.toml, then write instruction files.
		// All TOML mutations are composed in a single pass before writing to
		// ensure idempotency (no intermediate states that differ on re-run).
		configPath := adapter.MCPConfigPath(configHomeDir, "engram")
		if configPath == "" {
			break
		}
		runtimeErr := codex.ValidateGPT56Runtime()
		if runtimeErr != nil && !codex.IsGPT56RuntimeUnavailable(runtimeErr) {
			return InjectionResult{}, runtimeErr
		}

		// Determine instruction file paths before mutating the config.
		instructionsPath, compactPath, instructionsChanged, instructionFiles, instrErr := writeCodexInstructionFiles(configHomeDir)
		if instrErr != nil {
			return InjectionResult{}, instrErr
		}
		changed = changed || instructionsChanged
		files = append(files, instructionFiles...)

		// Read existing config and apply all mutations in a single pass.
		//
		// Mutation order (matters for idempotency):
		//   1. UpsertTOMLTableKey for [features] and [agents] — these are
		//      applied FIRST so that the section headers are present before
		//      UpsertTopLevelTOMLString and UpsertCodexEngramBlock run. When
		//      the sections already exist, their keys are replaced in place.
		//   2. UpsertTopLevelTOMLString for instruction-file keys — places
		//      top-level keys before the first [section] header (i.e., before
		//      [features]). Running model→experimental in this order is
		//      self-correcting on re-runs (the pair of calls always produces
		//      the same final ordering).
		//   3. UpsertCodexEngramBlock — strips the [mcp_servers.engram] block
		//      and re-appends it at EOF on every call. Running last ensures
		//      engram always ends up at the bottom of the file. On re-runs,
		//      UpsertCodexEngramBlock stops at [features] (the next section
		//      header after engram on disk), so [features] and [agents] are
		//      preserved above engram — the stable layout is:
		//        model_instructions_file / experimental_compact_prompt_file
		//        [features] / [agents]
		//        [mcp_servers.engram]
		existing, err := readFileOrEmpty(configPath)
		if err != nil {
			return InjectionResult{}, err
		}

		// Step 1 — multi-agent ODD delegation keys ([features] and [agents]).
		// features.multi_agent is enabled by default for bounded ODD workers. The
		// orchestrator asset gracefully falls back to solo execution if the multi-agent
		// tools are unavailable in the session. agents.max_threads/max_depth carry
		// conservative defaults.
		withFeatures := upsertCodexTableKeyBeforeMCPServers(existing, "features", "multi_agent", "true")
		withMaxThreads := upsertCodexTableKeyBeforeMCPServers(withFeatures, "agents", "max_threads", "4")
		withMaxDepth := upsertCodexTableKeyBeforeMCPServers(withMaxThreads, "agents", "max_depth", "2")

		// Step 2 — top-level instruction-file keys (before the first section header).
		withInstr := filemerge.UpsertTopLevelTOMLString(withMaxDepth, "model_instructions_file", instructionsPath)
		withCompact := filemerge.UpsertTopLevelTOMLString(withInstr, "experimental_compact_prompt_file", compactPath)
		if opts.CodexOrchestratorAssignment != nil {
			withCompact = filemerge.UpsertTopLevelTOMLString(withCompact, "model", opts.CodexOrchestratorAssignment.Model)
			withCompact = filemerge.UpsertTopLevelTOMLString(withCompact, "model_reasoning_effort", string(opts.CodexOrchestratorAssignment.Effort))
		}
		// Invalid tiers are dropped, and an unchanged selection leaves
		// service_tier exactly as the user keeps it.
		desiredTier, managedTier := validServiceTier(opts.CodexServiceTier), validServiceTier(opts.CodexManagedServiceTier)
		var changedTier *string
		if desiredTier != managedTier {
			if desiredTier != "" {
				withCompact = filemerge.UpsertTopLevelTOMLString(withCompact, "service_tier", desiredTier)
			} else if line, ok := rootCodexServiceTierLine(withCompact, managedTier); ok {
				lines := strings.Split(withCompact, "\n")
				withCompact = strings.Join(append(lines[:line], lines[line+1:]...), "\n")
			}
			changedTier = &desiredTier
		}

		// Step 3 — [mcp_servers.engram] block (always last; strip+re-append at EOF).
		engramCmd := stableEngramCommandForMergedConfig(configPath, adapter.Agent())
		withMCP := filemerge.UpsertCodexEngramBlock(withCompact, engramCmd)

		tomlWrite, err := filemerge.WriteFileAtomic(configPath, []byte(withMCP), 0o644)
		if err != nil {
			return InjectionResult{}, err
		}
		changed = changed || tomlWrite.Changed
		files = append(files, configPath)
		codexServiceTier = changedTier
	}

	// 2. Inject Engram memory protocol into system prompt (if supported).
	if adapter.SupportsSystemPrompt() {
		switch adapter.SystemPromptStrategy() {
		case model.StrategyMarkdownSections:
			promptPath := adapter.SystemPromptFile(promptDir)
			protocolContent := protocolFor(adapter.Agent(), opts)

			existing, err := readFileOrEmpty(promptPath)
			if err != nil {
				return InjectionResult{}, err
			}

			updated := filemerge.InjectMarkdownSection(existing, "engram-protocol", protocolContent)

			mdWrite, err := filemerge.WriteFileAtomic(promptPath, []byte(updated), 0o644)
			if err != nil {
				return InjectionResult{}, err
			}
			changed = changed || mdWrite.Changed
			files = append(files, promptPath)

		default:
			promptPath := adapter.SystemPromptFile(promptDir)
			protocolContent := protocolFor(adapter.Agent(), opts)

			existing, err := readFileOrEmpty(promptPath)
			if err != nil {
				return InjectionResult{}, err
			}

			updated := filemerge.InjectMarkdownSection(existing, "engram-protocol", protocolContent)

			mdWrite, err := filemerge.WriteFileAtomic(promptPath, []byte(updated), 0o644)
			if err != nil {
				return InjectionResult{}, err
			}
			changed = changed || mdWrite.Changed
			files = append(files, promptPath)
		}
	}

	return InjectionResult{Changed: changed, Files: files, CodexServiceTier: codexServiceTier}, nil
}

// upsertCodexTableKeyBeforeMCPServers behaves like filemerge.UpsertTOMLTableKey,
// except that a missing table is created before the first [mcp_servers.*]
// table instead of at EOF. Context7 and Engram both strip and re-append their
// MCP block at EOF, so MCP servers must stay contiguous at the end of the file:
// a table appended after an existing MCP block would be reordered by the next
// Context7 upsert, and install and sync would never produce the same bytes.
func upsertCodexTableKeyBeforeMCPServers(content, section, key, rawValue string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	firstMCP := -1
	// Table headers are only recognized outside TOML multiline strings, so a
	// developer_instructions value that mentions "[mcp_servers.x]" is text.
	var multiline byte
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		inString := multiline != 0
		multiline = filemerge.ScanTOMLMultilineString(line, multiline)
		if inString {
			continue
		}
		if trimmed == "["+section+"]" {
			return filemerge.UpsertTOMLTableKey(content, section, key, rawValue)
		}
		if firstMCP < 0 && strings.HasPrefix(trimmed, "[mcp_servers.") {
			firstMCP = i
		}
	}
	if firstMCP < 0 {
		return filemerge.UpsertTOMLTableKey(content, section, key, rawValue)
	}
	head := filemerge.UpsertTOMLTableKey(strings.Join(lines[:firstMCP], "\n"), section, key, rawValue)
	return head + "\n" + strings.Join(lines[firstMCP:], "\n")
}

func injectClaudeUserConfig(homeDir string, adapter agents.Adapter) (InjectionResult, error) {
	// The plugin and direct MCP entry expose the same Engram tools. When the
	// plugin is enabled, suppress direct registration without deleting any
	// existing entry: matching config shape is not proof that brunnr-code owns it.
	if claudeEngramPluginEnabled(homeDir) {
		return InjectionResult{}, nil
	}

	legacyPath := adapter.MCPConfigPath(homeDir, "engram")
	command := stableEngramCommandForMergedConfig(claude.UserConfigPath(homeDir), model.AgentClaudeCode)
	legacyManaged := false
	if raw, err := os.ReadFile(legacyPath); err == nil {
		if legacyCommand, ok := managedLegacyClaudeEngramCommand(raw); ok {
			command = stableEngramCommandForExisting(legacyCommand, model.AgentClaudeCode)
			legacyManaged = true
		}
	} else if !os.IsNotExist(err) {
		return InjectionResult{}, fmt.Errorf("read legacy Claude Engram config %q: %w", legacyPath, err)
	}

	writeResult, registryPath, err := claude.MergeUserConfig(homeDir, engramOverlayJSON(command))
	if err != nil {
		return InjectionResult{}, err
	}
	result := InjectionResult{Changed: writeResult.Changed, Files: []string{registryPath}}
	if !legacyManaged {
		return result, nil
	}
	removed, err := RemoveManagedLegacyClaudeConfig(legacyPath)
	if err != nil {
		return InjectionResult{}, err
	}
	if !removed {
		return result, nil
	}
	result.Changed = true
	result.Files = append(result.Files, legacyPath)
	return result, nil
}

func claudeEngramPluginEnabled(homeDir string) bool {
	settingsPath := filepath.Join(homeDir, ".claude", "settings.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		return false
	}

	var settings struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return false
	}
	return settings.EnabledPlugins["engram@engram"]
}

// writeCodexInstructionFiles writes the Engram memory protocol and compact prompt
// files to ~/.codex/ and returns their paths and write results.
func writeCodexInstructionFiles(homeDir string) (instructionsPath, compactPath string, changed bool, files []string, err error) {
	codexDir := filepath.Join(homeDir, ".codex")
	instructionsPath = filepath.Join(codexDir, "engram-instructions.md")
	compactPath = filepath.Join(codexDir, "engram-compact-prompt.md")

	instrContent := codexInstructions()
	instrWrite, err := filemerge.WriteFileAtomic(instructionsPath, []byte(instrContent), 0o644)
	if err != nil {
		return "", "", false, nil, fmt.Errorf("write codex engram-instructions.md: %w", err)
	}
	changed = instrWrite.Changed
	files = append(files, instructionsPath)

	compactContent := codexCompact()
	compactWrite, err := filemerge.WriteFileAtomic(compactPath, []byte(compactContent), 0o644)
	if err != nil {
		return "", "", false, nil, fmt.Errorf("write codex engram-compact-prompt.md: %w", err)
	}
	changed = changed || compactWrite.Changed
	files = append(files, compactPath)

	return instructionsPath, compactPath, changed, files, nil
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

func stableEngramCommandForMergedConfig(path string, agentID model.AgentID) string {
	raw, err := osReadFile(path)
	if err == nil {
		if cmd, ok := existingMergedEngramCommand(raw, agentID); ok {
			return stableEngramCommandForExisting(cmd, agentID)
		}
	}

	return preferredStableEngramCommand()
}

func stableEngramCommandForExisting(cmd string, _ model.AgentID) string {
	if isVersionedHomebrewCellarPath(cmd) {
		if stable := preferredStableEngramCommand(); stable != "" {
			return stable
		}
		return "engram"
	}

	return cmd
}

func preferredStableEngramCommand() string {
	if bin, ok := CallerBinary(); ok {
		return bin
	}
	p, err := EngramLookPath("engram")
	if err == nil && isStableHomebrewEngramPath(p) {
		return p
	}
	return "engram"
}

func existingMergedEngramCommand(raw []byte, agentID model.AgentID) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}

	normalized, err := filemerge.MergeJSONObjects(raw, []byte("{}"))
	if err != nil {
		return "", false
	}

	var root map[string]any
	if err := json.Unmarshal(normalized, &root); err != nil {
		return "", false
	}

	mcpServers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		return "", false
	}
	serverMap, ok := mcpServers["engram"].(map[string]any)
	if !ok {
		return "", false
	}

	return executableFromCommandValue(serverMap["command"])
}

func executableFromCommandValue(command any) (string, bool) {
	switch value := command.(type) {
	case string:
		if value == "" {
			return "", false
		}
		return value, true
	case []any:
		if len(value) == 0 {
			return "", false
		}
		first, ok := value[0].(string)
		if !ok || first == "" {
			return "", false
		}
		return first, true
	default:
		return "", false
	}
}

// buildSeparateMCPContent returns the content to write to the MCP server JSON
// file for agents that use the StrategySeparateMCPFiles strategy (e.g. Claude
// Code).
//
// Engram v1.10.3+ writes an absolute command path when `engram setup` is run.
// brunnr-code runs Inject() after setup, so we must not overwrite that absolute
// path with the relative "engram" string from defaultEngramServerJSON.
//
// Logic:
//   - If the file does not exist yet, return defaultContent unchanged.
//   - If the file exists but cannot be parsed as JSON, return defaultContent.
//   - If the parsed JSON has a "command" value that is an absolute path to the
//     engram binary, rebuild the config using that command and the canonical
//     args (["mcp", "--tools=agent"]) so that the absolute path is preserved
//     and the correct flags are always present.
//   - Otherwise (relative command or other value), return defaultContent.
func buildSeparateMCPContent(mcpPath string, defaultContent []byte) []byte {
	raw, err := os.ReadFile(mcpPath)
	if err != nil {
		// File does not exist or is not readable — use the default.
		return defaultContent
	}

	var existing map[string]any
	if err := json.Unmarshal(raw, &existing); err != nil {
		// Malformed JSON — use the default.
		return defaultContent
	}

	cmd, ok := executableFromCommandValue(existing["command"])
	if !ok || !isEngramCommand(cmd) {
		// No command, or not an engram command — use the default.
		return defaultContent
	}
	cmd = stableEngramCommandForExisting(cmd, "")

	// Rebuild with the preserved command and the canonical args (["mcp", "--tools=agent"]).
	rebuilt := map[string]any{
		"command": cmd,
		"args":    []string{"mcp", "--tools=agent"},
	}
	encoded, err := json.MarshalIndent(rebuilt, "", "  ")
	if err != nil {
		// Should be impossible with a plain map — use the default as fallback.
		return defaultContent
	}
	return append(encoded, '\n')
}

func managedLegacyClaudeEngramCommand(content []byte) (string, bool) {
	var server map[string]any
	if err := json.Unmarshal(content, &server); err != nil || len(server) != 2 {
		return "", false
	}
	command, ok := server["command"].(string)
	if !ok || !isEngramCommand(command) {
		return "", false
	}
	args, ok := server["args"].([]any)
	if !ok || len(args) != 2 || args[0] != "mcp" || args[1] != "--tools=agent" {
		return "", false
	}
	return command, true
}

// IsManagedLegacyClaudeConfig reports whether content has the exact legacy
// standalone Engram server shape emitted by Brunnr-Code.
func IsManagedLegacyClaudeConfig(content []byte) bool {
	_, ok := managedLegacyClaudeEngramCommand(content)
	return ok
}

// RemoveManagedLegacyClaudeConfig removes only the exact standalone shape
// emitted by Brunnr-Code. Its parent is removed only when the same real directory
// remains empty after that managed file is deleted; symlinks are never unlinked.
func RemoveManagedLegacyClaudeConfig(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspect managed legacy Claude Engram config %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read managed legacy Claude Engram config %q: %w", path, err)
	}
	if !IsManagedLegacyClaudeConfig(content) {
		return false, nil
	}
	if err := os.Remove(path); err != nil {
		return false, fmt.Errorf("remove managed legacy Claude Engram config %q: %w", path, err)
	}
	if err := removeManagedLegacyClaudeParent(filepath.Dir(path)); err != nil {
		return true, err
	}
	return true, nil
}

func removeManagedLegacyClaudeParent(parent string) error {
	info, err := os.Lstat(parent)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect managed legacy Claude directory %q: %w", parent, err)
	}
	if !info.IsDir() {
		return nil
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return fmt.Errorf("read managed legacy Claude directory %q: %w", parent, err)
	}
	if len(entries) != 0 {
		return nil
	}
	current, err := os.Lstat(parent)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reinspect managed legacy Claude directory %q: %w", parent, err)
	}
	if !current.IsDir() || !os.SameFile(info, current) {
		return nil
	}
	if err := os.Remove(parent); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove empty managed legacy Claude directory %q: %w", parent, err)
	}
	return nil
}

// isEngramCommand reports whether cmd is either a relative "engram" command
// or an absolute path pointing to an engram binary.
func isEngramCommand(cmd string) bool {
	if cmd == "" {
		return false
	}
	base := filepath.Base(cmd)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(base, "engram.exe") || strings.EqualFold(base, "engram")
	}
	return base == "engram"
}

func isVersionedHomebrewCellarPath(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	return strings.Contains(clean, "/Cellar/engram/") && isEngramCommand(clean)
}

func isStableHomebrewEngramPath(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	switch clean {
	case "/opt/homebrew/bin/engram", "/usr/local/bin/engram", "/home/linuxbrew/.linuxbrew/bin/engram":
		return isEngramCommand(clean)
	default:
		return false
	}
}

func validServiceTier(tier string) string {
	if !model.ValidCodexServiceTier(tier) {
		return ""
	}
	return tier
}

// rootCodexServiceTierLine finds the root-scope line that is byte-for-byte the
// service_tier assignment Brunnr-Code writes for tier. Lines inside multiline
// strings are content, not assignments, and never match.
func rootCodexServiceTierLine(content, tier string) (int, bool) {
	if tier == "" {
		return 0, false
	}
	want := fmt.Sprintf("service_tier = %q", tier)
	var multilineQuote byte
	for i, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if multilineQuote == 0 {
			if strings.HasPrefix(trimmed, "[") {
				return 0, false
			}
			if trimmed == want {
				return i, true
			}
		}
		multilineQuote = filemerge.ScanTOMLMultilineString(line, multilineQuote)
	}
	return 0, false
}
