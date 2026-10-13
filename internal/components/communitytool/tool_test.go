package communitytool

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/testenv"
)

// TestMain neutralizes ambient agent runtime-dir overrides (OPENCODE_CONFIG_DIR)
// before anything else: the codegraph subprocess re-exec path below runs
// before any per-test t.Setenv takes effect.
func TestMain(m *testing.M) {
	testenv.Isolate()
	if mode := os.Getenv("BRUNNR_CODE_CODEGRAPH_TEST_HELPER"); mode != "" {
		runCodeGraphTestHelper(mode)
		os.Exit(0)
	}
	codeGraphPackageLookPath = func(name string) (string, error) {
		if name == "npm" {
			return "/bin/npm", nil
		}
		return "", fmt.Errorf("not found")
	}
	codeGraphPnpmGlobalBin = func() (string, error) {
		return "/bin", nil
	}
	codeGraphCLIUsable = func(string) bool { return true }
	os.Exit(m.Run())
}

func runCodeGraphTestHelper(mode string) {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		request := scanner.Text()
		if mode == "stall-initialize" || mode == "stall-tools-list" && strings.Contains(request, `"id":2`) {
			time.Sleep(24 * time.Hour)
		}
		response := os.Getenv("BRUNNR_CODE_CODEGRAPH_TEST_RESPONSE")
		if response == "" && strings.Contains(request, `"id":1`) {
			response = `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","capabilities":{},"serverInfo":{"name":"fake","version":"1"}}}`
		} else if response == "" && strings.Contains(request, `"id":2`) {
			response = `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"codegraph_explore","inputSchema":{"type":"object","properties":{"query":{"type":"string"},"maxFiles":{"type":"integer"},"projectPath":{"type":"string"}},"required":["query"]}}]}}`
		}
		if response != "" {
			fmt.Fprintln(os.Stdout, response)
		}
	}
}

func TestDefinitionsIncludesCodeGraph(t *testing.T) {
	def, ok := DefinitionFor(model.CommunityToolCodeGraph)
	if !ok {
		t.Fatal("CodeGraph definition not found")
	}
	if def.PackageName != "@colbymchenry/codegraph@latest" || def.CommandName != "codegraph" {
		t.Fatalf("CodeGraph definition = %#v", def)
	}
}

func TestCodeGraphCommands(t *testing.T) {
	want := [][]string{
		{"npm", "install", "-g", "@colbymchenry/codegraph@latest"},
		{"codegraph", "install", "--yes"},
	}
	if got := CodeGraphCommands(); !reflect.DeepEqual(got, want) {
		t.Fatalf("CodeGraphCommands() = %#v, want %#v", got, want)
	}
	for _, command := range CodeGraphCommands() {
		if strings.Contains(strings.Join(command, " "), "codegraph init") {
			t.Fatalf("CodeGraphCommands() includes project init command: %#v", command)
		}
	}
}

func TestCodeGraphCommandsForDetectorUsesPnpmWhenNpmIsUnavailable(t *testing.T) {
	commands, err := CodeGraphCommandsForDetector(DetectorFunc(func(name string) (string, error) {
		if name == "pnpm" {
			return "/bin/pnpm", nil
		}
		return "", errors.New("not found")
	}))
	if err != nil {
		t.Fatalf("CodeGraphCommandsForDetector() error = %v", err)
	}
	want := [][]string{
		{"pnpm", "add", "-g", "@colbymchenry/codegraph@latest"},
		{"codegraph", "install", "--yes"},
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("CodeGraphCommandsForDetector() = %#v, want %#v", commands, want)
	}
}

func TestCodeGraphCommandsForDetectorReportsUnusablePnpmGlobalBin(t *testing.T) {
	previous := codeGraphPnpmGlobalBin
	codeGraphPnpmGlobalBin = func() (string, error) {
		return "", errors.New(`ERROR The configured global bin directory "/Users/example/Library/pnpm" is not in PATH`)
	}
	t.Cleanup(func() { codeGraphPnpmGlobalBin = previous })

	_, err := CodeGraphCommandsForDetector(DetectorFunc(func(name string) (string, error) {
		if name == "pnpm" {
			return "/bin/pnpm", nil
		}
		return "", errors.New("not found")
	}))
	if err == nil {
		t.Fatal("CodeGraphCommandsForDetector() error = nil, want unusable pnpm global bin error")
	}
	for _, want := range []string{"pnpm global installs are not ready", "pnpm setup", "not in PATH"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want %q", err, want)
		}
	}
}

func TestCodeGraphCommandsForDetectorPrefersNpmWhenBothExist(t *testing.T) {
	commands, err := CodeGraphCommandsForDetector(DetectorFunc(func(name string) (string, error) {
		if name == "npm" || name == "pnpm" {
			return "/bin/" + name, nil
		}
		return "", errors.New("not found")
	}))
	if err != nil {
		t.Fatalf("CodeGraphCommandsForDetector() error = %v", err)
	}
	if got := commands[0]; !reflect.DeepEqual(got, []string{"npm", "install", "-g", "@colbymchenry/codegraph@latest"}) {
		t.Fatalf("CodeGraphCommandsForDetector()[0] = %#v, want npm install", got)
	}
}

func TestCodeGraphCommandsForDetectorFailsWhenNpmAndPnpmAreMissing(t *testing.T) {
	_, err := CodeGraphCommandsForDetector(DetectorFunc(func(string) (string, error) {
		return "", errors.New("not found")
	}))
	if err == nil || !strings.Contains(err.Error(), "npm") || !strings.Contains(err.Error(), "pnpm") {
		t.Fatalf("CodeGraphCommandsForDetector() error = %v, want npm/pnpm requirement", err)
	}
}

func TestInstallUsesPnpmWhenNpmIsUnavailable(t *testing.T) {
	previous := codeGraphPackageLookPath
	codeGraphPackageLookPath = func(name string) (string, error) {
		if name == "pnpm" {
			return "/bin/pnpm", nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { codeGraphPackageLookPath = previous })

	var commands []string
	installed := false
	_, err := InstallWithHome(model.CommunityToolCodeGraph, "/work/project", t.TempDir(), RunnerFunc(func(name string, args ...string) error {
		commands = append(commands, strings.Join(append([]string{name}, args...), " "))
		installed = true
		return nil
	}), DetectorFunc(func(string) (string, error) {
		if installed {
			return "/bin/codegraph", nil
		}
		return "", errors.New("not found")
	}))
	if err != nil {
		t.Fatalf("InstallWithHome() error = %v", err)
	}
	want := []string{
		"pnpm add -g @colbymchenry/codegraph@latest",
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}

func TestCodeGraphGuidanceContainsLazyInitAndUsageRules(t *testing.T) {
	guidance := CodeGraphGuidanceMarkdown()
	for _, want := range []string{
		"use CodeGraph before broad filesystem searches",
		"hard ordering rule",
		"Create Git worktrees that may need CodeGraph under the user's home directory",
		"<repo-parent>/<repo-name>-worktrees/<worktree-name>",
		"Never place a CodeGraph-dependent worktree under `/tmp` or `/var/tmp`",
		"generic temporary-work guidance does not override this rule",
		"Every worktree needs its own `.codegraph/` index",
		"Never copy, symlink, or reuse another checkout's index",
		"git rev-parse --show-toplevel || pwd",
		"Do not ask the user before initializing CodeGraph in a real project.",
		"Do not initialize CodeGraph in `$HOME`, temporary directories, or non-project folders",
		"<project-root>/.codegraph/",
		"before any broad Read/Glob/Grep filesystem exploration",
		"immediately run `brunnr-code codegraph init --cwd <project-root>`",
		"brunnr-code codegraph init --cwd <project-root>",
		"codegraph_explore",
		"call paths, and blast-radius context",
		"invoke the upstream CLI directly",
		"`codegraph status`",
		"`codegraph query`",
		"`codegraph explore`",
		"`codegraph node`",
		"`codegraph files`",
		"`codegraph callers`",
		"`codegraph callees`",
		"`codegraph impact`",
		"`codegraph affected`",
		"Do not use `brunnr-code codegraph` as a general proxy",
		"Never run or recommend destructive or administrative lifecycle commands",
		"`codegraph uninit`",
		"`codegraph install`",
		"`codegraph uninstall`",
		"`codegraph upgrade`",
		"Reserve `codegraph index` for explicit index-corruption recovery, never routine use",
		"Missing .codegraph/ is the trigger to initialize, not a reason to skip CodeGraph.",
		"Do not fall back just because `.codegraph/` is missing",
		"missing index is the trigger to lazy-initialize",
		"read-only upstream CLI commands when MCP tools are absent",
		"Only fall back to normal filesystem tools after CodeGraph initialization or use fails",
		"Broad Read/Glob/Grep exploration before this CodeGraph check is explicitly discouraged",
	} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("CodeGraphGuidanceMarkdown() missing %q:\n%s", want, guidance)
		}
	}
}

func TestCodeGraphGuidanceRequiresFreshIndexForCLIReads(t *testing.T) {
	guidance := CodeGraphGuidanceMarkdown()
	for _, want := range []string{
		"MCP auto-sync requires a running daemon with an active file watcher",
		"CLI intelligence commands read the existing index as-is and do not auto-sync",
		"Before CLI intelligence reads, run `codegraph sync -q <project-root>`",
		"If sync fails, do not treat the existing index as current",
		"The presence of `.codegraph/` alone does not guarantee freshness",
	} {
		if !strings.Contains(guidance, want) {
			t.Errorf("CodeGraphGuidanceMarkdown() missing %q", want)
		}
	}
	for _, stale := range []string{
		"rely on watcher auto-sync by default",
		"Run `codegraph sync` only when",
	} {
		if strings.Contains(guidance, stale) {
			t.Errorf("CodeGraphGuidanceMarkdown() retains misleading freshness rule %q", stale)
		}
	}
}

func TestCodeGraphGuidanceSyncPrecedesCLIReads(t *testing.T) {
	guidance := CodeGraphGuidanceMarkdown()
	read := strings.Index(guidance, "Use `codegraph_explore` after initialization, or the read-only upstream CLI commands when MCP tools are absent")
	if read < 0 {
		t.Fatal("missing intelligence read instruction")
	}
	for _, prerequisite := range []string{
		"Before CLI intelligence reads, run `codegraph sync -q <project-root>`",
		"If sync fails, do not treat the existing index as current; explain the failure and fall back to normal filesystem tools",
	} {
		position := strings.Index(guidance, prerequisite)
		if position < 0 || position >= read {
			t.Errorf("prerequisite %q must precede the intelligence read instruction", prerequisite)
		}
	}
}

func TestCodeGraphGuidancePreservesExplicitInitializationWorkspace(t *testing.T) {
	guidance := CodeGraphGuidanceMarkdown()
	for _, want := range []string{
		"For project initialization, the explicit agent working directory is authoritative",
		"Check `<cwd>/.git` (directory or worktree file) and `<cwd>/.codegraph/` before ancestor discovery",
		"Never replace an initialization target with an enclosing repository",
		"Only for an already-selected existing repository",
		"git rev-parse --show-toplevel || pwd",
		"If the explicit initialization target is not a Git root",
		"git -C <cwd> init",
		"never retry initialization against the parent",
	} {
		if !strings.Contains(guidance, want) {
			t.Errorf("initialization workspace contract missing %q", want)
		}
	}
	if strings.Contains(guidance, "1. Resolve the project root with `git rev-parse --show-toplevel || pwd`.") {
		t.Error("guidance still unconditionally replaces the workspace with its ancestor Git root")
	}
}

func TestCodeGraphGuidanceInjectsForRepresentativeAgents(t *testing.T) {
	home := t.TempDir()
	mustWrite(t, filepath.Join(home, ".claude", "settings.json"), `{}`)
	mustWrite(t, filepath.Join(home, ".codex", "config.toml"), `[mcp_servers.codegraph]`)

	installed := false
	result, err := InstallWithHome(model.CommunityToolCodeGraph, "/work/project", home, RunnerFunc(func(string, ...string) error {
		installed = true
		mustWrite(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"codegraph":{"command":"codegraph","args":["serve","--mcp"]}}}`)
		return nil
	}), DetectorFunc(func(string) (string, error) {
		if installed {
			return "/bin/codegraph", nil
		}
		return "", errors.New("not found")
	}))
	if err != nil {
		t.Fatalf("InstallWithHome() error = %v", err)
	}
	if result.StatusAfter == nil {
		t.Fatal("StatusAfter = nil")
	}

	for _, path := range []string{
		filepath.Join(home, ".claude", "CLAUDE.md"),
		filepath.Join(home, ".codex", "AGENTS.md"),
	} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", path, err)
		}
		text := string(content)
		if !strings.Contains(text, "<!-- brunnr-code:codegraph-guidance -->") || !strings.Contains(text, "brunnr-code codegraph init --cwd <project-root>") {
			t.Fatalf("%q missing CodeGraph guidance:\n%s", path, text)
		}
	}
	for _, path := range CodeGraphGuidancePaths(home) {
		if strings.Contains(path, "node_modules") || strings.Contains(path, string(filepath.Separator)+"agents"+string(filepath.Separator)) || strings.Contains(path, string(filepath.Separator)+"chains"+string(filepath.Separator)) {
			t.Fatalf("guidance mutated forbidden package path %q", path)
		}
	}
}

func TestInstallRunsCommandsAndReturnsLazyProjectIndexManualAction(t *testing.T) {
	var ran []string
	installed := false
	result, err := InstallWithHome(model.CommunityToolCodeGraph, "/work/project", t.TempDir(), RunnerFunc(func(name string, args ...string) error {
		ran = append(ran, append([]string{name}, args...)...)
		installed = true
		return nil
	}), DetectorFunc(func(string) (string, error) {
		if installed {
			return "/bin/codegraph", nil
		}
		return "", errors.New("not found")
	}))
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if len(ran) == 0 {
		t.Fatal("expected commands to run")
	}
	if len(result.ManualActions) != 1 {
		t.Fatalf("ManualActions = %#v, want one lazy project index instruction", result.ManualActions)
	}
	action := result.ManualActions[0]
	for _, want := range []string{"CodeGraph CLI was installed", "agents were connected", "Project indexes will be created automatically"} {
		if !strings.Contains(action, want) {
			t.Fatalf("ManualActions[0] = %q, want %q", action, want)
		}
	}
	if strings.Contains(action, "codegraph init") {
		t.Fatalf("ManualActions[0] = %q, should not instruct immediate project init", action)
	}
}

func TestDetectStatusReportsCLIAndPerAgentWiring(t *testing.T) {
	home := t.TempDir()
	mustWrite(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"codegraph":{"command":"codegraph"}}}`)
	mustWrite(t, filepath.Join(home, ".claude", "CLAUDE.md"), strings.Join([]string{
		"existing Claude guidance",
		"<!-- brunnr-code:codegraph-guidance -->",
		"CodeGraph guidance with `brunnr-code codegraph init --cwd <project-root>`",
		"<!-- /brunnr-code:codegraph-guidance -->",
	}, "\n"))

	status := DetectStatus(model.CommunityToolCodeGraph, home, DetectorFunc(func(name string) (string, error) {
		if name != "codegraph" {
			t.Fatalf("LookPath(%q), want codegraph", name)
		}
		return "/bin/codegraph", nil
	}))

	if status.CLI != AvailabilityAvailable || status.CLIPath != "/bin/codegraph" {
		t.Fatalf("CLI = (%s, %q), want available /bin/codegraph", status.CLI, status.CLIPath)
	}
	claude := findAgentStatus(t, status, model.AgentClaudeCode)
	if !claude.Detected || !claude.Configured || claude.Status != AgentStatusConfigured {
		t.Fatalf("claude status = %#v, want detected configured", claude)
	}
}

func TestInstallRunsFullReconcileWhenAnotherAgentIsMissing(t *testing.T) {
	home := t.TempDir()
	mustWrite(t, filepath.Join(home, ".claude", "settings.json"), `{}`)

	result, err := InstallWithHome(model.CommunityToolCodeGraph, "/work/project", home, RunnerFunc(func(string, ...string) error {
		mustWrite(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"codegraph":{"command":"codegraph","args":["serve","--mcp"]}}}`)
		return nil
	}), DetectorFunc(func(string) (string, error) { return "/bin/codegraph", nil }))
	if err != nil {
		t.Fatalf("InstallWithHome() error = %v", err)
	}
	if !reflect.DeepEqual(result.CommandsRun, []string{"codegraph install --target claude --location global --yes"}) {
		t.Fatalf("CommandsRun = %#v, want full reconciliation", result.CommandsRun)
	}
}

func TestDetectStatusReportsCodexMissingWhenConfigHasCodeGraphButGuidanceIsMissing(t *testing.T) {
	home := t.TempDir()
	mustWrite(t, filepath.Join(home, ".codex", "config.toml"), strings.Join([]string{
		`[mcp_servers.codegraph]`,
		`command = "codegraph"`,
	}, "\n"))

	status := DetectStatus(model.CommunityToolCodeGraph, home, DetectorFunc(func(name string) (string, error) {
		if name != "codegraph" {
			t.Fatalf("LookPath(%q), want codegraph", name)
		}
		return "/bin/codegraph", nil
	}))

	codex := findAgentStatus(t, status, model.AgentCodex)
	if !codex.Detected || codex.Configured || codex.Status != AgentStatusMissing {
		t.Fatalf("codex status = %#v, want detected missing until AGENTS.md has CodeGraph guidance", codex)
	}
	wantPath := filepath.Join(home, ".codex", "AGENTS.md")
	if codex.Path != wantPath {
		t.Fatalf("codex path = %q, want guidance path %q", codex.Path, wantPath)
	}
}

func TestDetectStatusReportsMissingCLIThroughMock(t *testing.T) {
	status := DetectStatus(model.CommunityToolCodeGraph, t.TempDir(), DetectorFunc(func(string) (string, error) {
		return "", errors.New("not found")
	}))
	if status.CLI != AvailabilityMissing {
		t.Fatalf("CLI = %s, want missing", status.CLI)
	}
}

func TestInstallFailsWhenPostInstallContractStillMissing(t *testing.T) {
	result, err := InstallWithHome(model.CommunityToolCodeGraph, "/work/project", t.TempDir(), RunnerFunc(func(string, ...string) error {
		return nil
	}), DetectorFunc(func(string) (string, error) {
		return "", errors.New("not found")
	}))
	if err == nil || !strings.Contains(err.Error(), "CLI available") {
		t.Fatalf("InstallWithHome() error = %v, want missing CLI validation", err)
	}
	if result.StatusAfter == nil {
		t.Fatal("StatusAfter = nil, want partial result context")
	}
}

func TestValidateCodeGraphInstallStatusFailsForDetectedMissingAgent(t *testing.T) {
	status := Status{
		Tool: model.CommunityToolCodeGraph,
		CLI:  AvailabilityAvailable,
		Agents: []AgentStatus{
			{Agent: model.AgentCodex, Name: "Codex", Detected: true, Configured: false},
		},
	}
	err := validateCodeGraphInstallStatus(status)
	if err == nil || !strings.Contains(err.Error(), "Codex") {
		t.Fatalf("validateCodeGraphInstallStatus() error = %v, want missing Codex", err)
	}
}

func TestInstallSkipsWhenCodeGraphAlreadyReconciled(t *testing.T) {
	home := t.TempDir()
	mustWrite(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"codegraph":{"command":"codegraph"}}}`)

	calls := 0
	result, err := InstallWithHome(model.CommunityToolCodeGraph, "/work/project", home, RunnerFunc(func(string, ...string) error {
		calls++
		return nil
	}), DetectorFunc(func(string) (string, error) {
		return "/bin/codegraph", nil
	}))
	if err != nil {
		t.Fatalf("InstallWithHome() error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("runner calls = %d, want 0 for already reconciled install", calls)
	}
	if result.StatusAfter == nil || !result.StatusAfter.CodeGraphReconcileSatisfied() {
		t.Fatalf("StatusAfter = %#v, want reconciled", result.StatusAfter)
	}
}

func TestInstallAlreadyReconciledHealthyMessage(t *testing.T) {
	home := t.TempDir()
	mustWrite(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"codegraph":{"command":"codegraph"}}}`)
	mustWrite(t, filepath.Join(home, ".claude", "CLAUDE.md"), "user guidance\n")
	runner := RunnerFunc(func(string, ...string) error {
		t.Fatal("already reconciled healthy agent must not invoke installer")
		return nil
	})
	detector := DetectorFunc(func(string) (string, error) { return "/bin/codegraph", nil })
	first, err := InstallWithHome(model.CommunityToolCodeGraph, "", home, runner, detector)
	if err != nil || !slices.Contains(first.ManualActions, "CodeGraph is already available and MCP-configured. Agent guidance was updated so enabled agents lazily initialize project indexes when needed.") {
		t.Fatalf("healthy guidance refresh = %#v, err=%v", first, err)
	}
	second, err := InstallWithHome(model.CommunityToolCodeGraph, "", home, runner, detector)
	if err != nil || len(second.CommandsRun) != 0 || !slices.Contains(second.ManualActions, "CodeGraph is already available and configured for all detected supported agents. No changes were needed.") {
		t.Fatalf("healthy no-op = %#v, err=%v", second, err)
	}
}

func TestInstallRepairsMissingCLIWhenAgentMarkerExists(t *testing.T) {
	home := t.TempDir()
	mustWrite(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"codegraph":{"command":"codegraph"}}}`)

	var commands []string
	installed := false
	result, err := InstallWithHome(model.CommunityToolCodeGraph, "/work/project", home, RunnerFunc(func(name string, args ...string) error {
		commands = append(commands, strings.Join(append([]string{name}, args...), " "))
		installed = true
		return nil
	}), DetectorFunc(func(string) (string, error) {
		if installed {
			return "/bin/codegraph", nil
		}
		return "", errors.New("not found")
	}))
	if err != nil {
		t.Fatalf("InstallWithHome() error = %v", err)
	}
	want := []string{
		"npm install -g @colbymchenry/codegraph@latest",
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
	if result.StatusBefore == nil || result.StatusBefore.CLI != AvailabilityMissing {
		t.Fatalf("StatusBefore = %#v, want missing CLI", result.StatusBefore)
	}
}

func TestDetectStatusRejectsWSLWindowsNPMShimWithoutExecutingIt(t *testing.T) {
	useCodeGraphWSLAdmission(t, true)
	previousVersion := codeGraphInstalledVersion
	t.Cleanup(func() { codeGraphInstalledVersion = previousVersion })
	codeGraphInstalledVersion = func(string) (string, bool) {
		t.Fatal("DetectStatus must not execute a candidate CodeGraph CLI")
		return "", false
	}

	status := DetectStatus(model.CommunityToolCodeGraph, t.TempDir(), DetectorFunc(func(string) (string, error) {
		return "/mnt/c/Users/alan/AppData/Roaming/npm/codegraph", nil
	}))
	if status.CLI != AvailabilityMissing || status.CLIPath != "" {
		t.Fatalf("DetectStatus() = %#v, want WSL Windows npm shim rejected", status)
	}
}

func TestDetectStatusAdmitsLinuxCLIPathsWithoutRequiringFiles(t *testing.T) {
	useCodeGraphWSLAdmission(t, true)
	previousVersion := codeGraphInstalledVersion
	t.Cleanup(func() { codeGraphInstalledVersion = previousVersion })
	codeGraphInstalledVersion = func(string) (string, bool) {
		t.Fatal("DetectStatus must not execute a candidate CodeGraph CLI")
		return "", false
	}

	for _, path := range []string{
		"/bin/codegraph",
		"/home/alan/.local/bin/codegraph",
		"/home/alan/AppData/Roaming/npm/codegraph",
	} {
		t.Run(path, func(t *testing.T) {
			status := DetectStatus(model.CommunityToolCodeGraph, t.TempDir(), DetectorFunc(func(string) (string, error) {
				return path, nil
			}))
			if status.CLI != AvailabilityAvailable || status.CLIPath != path {
				t.Fatalf("DetectStatus() = %#v, want injected Linux path available", status)
			}
		})
	}
}

func TestInstallRejectsWSLWindowsNPMShimAfterPackageInstall(t *testing.T) {
	home := t.TempDir()
	mustWrite(t, filepath.Join(home, ".claude.json"), `{}`)
	shim := "/mnt/c/Users/alan/AppData/Roaming/npm/codegraph"

	previousPackageLookPath := codeGraphPackageLookPath
	t.Cleanup(func() { codeGraphPackageLookPath = previousPackageLookPath })
	codeGraphPackageLookPath = func(name string) (string, error) {
		if name == "npm" {
			return "/bin/npm", nil
		}
		return "", errors.New("not found")
	}
	useCodeGraphWSLAdmission(t, true)
	previousVersion := codeGraphInstalledVersion
	t.Cleanup(func() { codeGraphInstalledVersion = previousVersion })
	codeGraphInstalledVersion = func(string) (string, bool) {
		t.Fatal("CodeGraph admission must not execute the stale shim")
		return "", false
	}

	var runnerCalls []string
	result, err := InstallWithHome(model.CommunityToolCodeGraph, "/work/project", home, RunnerFunc(func(name string, args ...string) error {
		runnerCalls = append(runnerCalls, strings.Join(append([]string{name}, args...), " "))
		if name != "npm" {
			return errors.New("CodeGraph runner must not receive a rejected shim")
		}
		return nil
	}), DetectorFunc(func(string) (string, error) {
		return shim, nil
	}))
	if err == nil || !strings.Contains(err.Error(), "did not leave a runnable codegraph CLI available") {
		t.Fatalf("InstallWithHome() error = %v, want unavailable CodeGraph outcome after package install", err)
	}
	if !reflect.DeepEqual(runnerCalls, []string{"npm install -g @colbymchenry/codegraph@latest"}) {
		t.Fatalf("runner calls = %#v, want package install before stale-shim re-admission blocks CodeGraph", runnerCalls)
	}
	if result.StatusBefore == nil || result.StatusBefore.CLI != AvailabilityMissing || result.StatusBefore.CLIPath != "" {
		t.Fatalf("StatusBefore = %#v, want truthful absent CLI", result.StatusBefore)
	}
	if result.StatusAfter == nil || result.StatusAfter.CLI != AvailabilityMissing || result.StatusAfter.CLIPath != "" {
		t.Fatalf("StatusAfter = %#v, want rejected shim to remain unavailable", result.StatusAfter)
	}
}

func TestInstallFailurePaths(t *testing.T) {
	t.Run("nil runner", func(t *testing.T) {
		result, err := Install(model.CommunityToolCodeGraph, "/work/project", nil)
		if err == nil {
			t.Fatal("Install() error = nil, want configured runner error")
		}
		if result.Tool != "" || len(result.CommandsRun) != 0 {
			t.Fatalf("result = %#v, want empty result", result)
		}
	})

	t.Run("unknown tool", func(t *testing.T) {
		result, err := Install(model.CommunityToolID("missing-tool"), "/work/project", RunnerFunc(func(string, ...string) error { return nil }))
		if err == nil || !strings.Contains(err.Error(), "unknown community tool") {
			t.Fatalf("Install() error = %v, want unknown tool error", err)
		}
		if result.Tool != "" || len(result.CommandsRun) != 0 {
			t.Fatalf("result = %#v, want empty result", result)
		}
	})

	t.Run("command runner failure preserves attempted command", func(t *testing.T) {
		boom := errors.New("npm failed")
		result, err := InstallWithHome(model.CommunityToolCodeGraph, "/work/project", t.TempDir(), RunnerFunc(func(string, ...string) error { return boom }), DetectorFunc(func(string) (string, error) {
			return "", errors.New("not found")
		}))
		if !errors.Is(err, boom) {
			t.Fatalf("Install() error = %v, want wrapped runner error", err)
		}
		if result.Tool != model.CommunityToolCodeGraph {
			t.Fatalf("result tool = %q, want CodeGraph", result.Tool)
		}
		if len(result.CommandsRun) != 1 || !strings.Contains(result.CommandsRun[0], "npm install -g @colbymchenry/codegraph@latest") {
			t.Fatalf("CommandsRun = %#v, want failed CLI install command", result.CommandsRun)
		}
	})

	t.Run("agent wiring failure preserves attempted commands", func(t *testing.T) {
		boom := errors.New("install failed")
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		calls := 0
		result, err := InstallWithHome(model.CommunityToolCodeGraph, "/work/project", home, RunnerFunc(func(string, ...string) error {
			calls++
			if calls == 2 {
				return boom
			}
			return nil
		}), DetectorFunc(func(string) (string, error) {
			if calls > 0 {
				return "/bin/codegraph", nil
			}
			return "", errors.New("not found")
		}))
		if !errors.Is(err, boom) {
			t.Fatalf("Install() error = %v, want wrapped install error", err)
		}
		if calls != 2 {
			t.Fatalf("runner calls = %d, want 2", calls)
		}
		if len(result.CommandsRun) != 2 {
			t.Fatalf("CommandsRun = %#v, want CLI install and failed agent wiring command", result.CommandsRun)
		}
		got := strings.Join(result.CommandsRun, "\n")
		if !strings.Contains(got, "npm install -g @colbymchenry/codegraph@latest") || !strings.Contains(got, "codegraph install --target claude") || strings.Contains(got, "codegraph init") {
			t.Fatalf("CommandsRun = %#v, want CLI install and agent wiring commands only", result.CommandsRun)
		}
	})
}

func mustWrite(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func useCodeGraphWSLAdmission(t *testing.T, wsl bool) {
	t.Helper()
	previousCLIUsable := codeGraphCLIUsable
	previousWSL := codeGraphWSL
	t.Cleanup(func() {
		codeGraphCLIUsable = previousCLIUsable
		codeGraphWSL = previousWSL
	})
	codeGraphCLIUsable = defaultCodeGraphCLIUsable
	codeGraphWSL = func() bool { return wsl }
}

func findAgentStatus(t *testing.T, status Status, id model.AgentID) AgentStatus {
	t.Helper()
	for _, agent := range status.Agents {
		if agent.Agent == id {
			return agent
		}
	}
	t.Fatalf("agent %s not found in %#v", id, status.Agents)
	return AgentStatus{}
}

// Sync runs the legacy cleanup on every install now that CodeGraph is
// mandatory, so it must not reformat prompt files that carry no legacy
// CodeGraph guidance.
func TestCleanLegacyCodeGraphGuidanceLeavesFilesWithoutLegacyGuidanceUntouched(t *testing.T) {
	home := t.TempDir()
	promptPath := filepath.Join(home, ".claude", "CLAUDE.md")
	original := "# Rules\n\n\n## Section\n\n\n"
	mustWrite(t, promptPath, original)

	result, err := CleanLegacyCodeGraphGuidance(home)
	if err != nil {
		t.Fatalf("CleanLegacyCodeGraphGuidance() error = %v", err)
	}
	if result.Changed || len(result.Files) != 0 {
		t.Fatalf("cleanup result = %#v, want no change", result)
	}
	got, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("prompt rewritten:\n got %q\nwant %q", got, original)
	}
}
