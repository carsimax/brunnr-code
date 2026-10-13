package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/carsimax/brunnr-code/internal/components/communitytool"
	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/planner"
	"github.com/carsimax/brunnr-code/internal/system"
)

func TestInstallRuntimeStagePlanAlwaysAddsCodeGraphStep(t *testing.T) {
	runtime := &installRuntime{
		homeDir:      t.TempDir(),
		workspaceDir: "/work/project",
		selection:    model.Selection{},
		resolved:     planner.ResolvedPlan{},
		profile:      system.PlatformProfile{},
		state:        &runtimeState{},
	}

	plan := runtime.stagePlan()
	var got []string
	for _, step := range plan.Apply {
		got = append(got, step.ID())
	}
	want := []string{"apply:rollback-restore", "codegraph:install"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("apply step IDs = %#v, want %#v", got, want)
	}
}

func TestSyncStagePlanAlwaysAddsCodeGraphGuidanceStep(t *testing.T) {
	for _, tt := range []struct {
		scope InstallScope
		want  bool
	}{
		{scope: ScopeGlobal, want: true},
		{scope: ScopeWorkspace, want: false},
	} {
		t.Run(string(tt.scope), func(t *testing.T) {
			home := t.TempDir()
			plan := (&syncRuntime{homeDir: home, workspaceDir: t.TempDir(), backupRoot: t.TempDir(), scope: tt.scope, state: &runtimeState{}}).stagePlan()
			got := false
			for _, step := range plan.Apply {
				if step.ID() == "sync:codegraph-guidance" {
					got = true
				}
			}
			if got != tt.want {
				t.Fatalf("sync plan has CodeGraph guidance step = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBackupTargetsSnapshotCrossAgentCodeGraphGuidance(t *testing.T) {
	home := t.TempDir()
	claudeConfig := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	targets, err := backupTargets(home, "", ScopeGlobal, model.Selection{}, planner.ResolvedPlan{})
	if err != nil {
		t.Fatal(err)
	}
	guidancePaths := communitytool.CodeGraphGuidancePaths(home)
	if len(guidancePaths) == 0 {
		t.Fatal("CodeGraphGuidancePaths() = empty; Claude fixture was not detected")
	}
	for _, path := range guidancePaths {
		if !slices.Contains(targets, path) {
			t.Fatalf("backup targets = %v, missing guidance path %q", targets, path)
		}
	}
}

type failAfterPluginRegistrationStep struct {
	codeGraphSucceeded *bool
	tuiPath            string
	logoPath           string
}

func (s failAfterPluginRegistrationStep) ID() string { return "test:late-plugin-rollback-control" }

func (s failAfterPluginRegistrationStep) Run() error {
	if s.codeGraphSucceeded == nil || !*s.codeGraphSucceeded {
		return errors.New("CodeGraph did not complete before plugin rollback control")
	}
	tui, err := os.ReadFile(s.tuiPath)
	if err != nil {
		return fmt.Errorf("read plugin registration before rollback control: %w", err)
	}
	var config struct {
		Plugin []string `json:"plugin"`
	}
	if err := json.Unmarshal(tui, &config); err != nil {
		return fmt.Errorf("decode plugin registration %q: %w", s.tuiPath, err)
	}
	for _, plugin := range []string{s.logoPath} {
		if !slices.Contains(config.Plugin, plugin) {
			return fmt.Errorf("plugin registration %q is missing exact plugin %q", s.tuiPath, plugin)
		}
	}
	if _, err := os.Stat(s.logoPath); err != nil {
		return fmt.Errorf("Logo plugin was not written before rollback control: %w", err)
	}
	return errors.New("late plugin rollback control")
}

func TestCodeGraphInstallStepUsesInjectableInstaller(t *testing.T) {
	previousInstall := installCodeGraph
	previousRunCommand := runCommand
	t.Cleanup(func() {
		installCodeGraph = previousInstall
		runCommand = previousRunCommand
	})

	runCommand = func(string, ...string) error {
		t.Fatal("codeGraphInstallStep should not call real command runner when installer is injected")
		return nil
	}

	var gotTool model.CommunityToolID
	var gotWorkspace, gotHome string
	var runner communitytool.Runner
	installCodeGraph = func(tool model.CommunityToolID, workspaceDir string, home string, r communitytool.Runner, _ communitytool.Detector) (communitytool.Result, error) {
		gotTool, gotWorkspace, gotHome, runner = tool, workspaceDir, home, r
		return communitytool.Result{Tool: tool}, nil
	}

	step := codeGraphInstallStep{id: "codegraph:install", workspaceDir: "/work/project", homeDir: "/tmp/runtime-home"}
	if err := step.Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if gotTool != model.CommunityToolCodeGraph || gotWorkspace != "/work/project" || gotHome != "/tmp/runtime-home" || runner == nil {
		t.Fatalf("installer args = (%q, %q, %q, %#v), want CodeGraph, workspace, runtime home, runner", gotTool, gotWorkspace, gotHome, runner)
	}
}

func withCodeGraphLookPath(t *testing.T, lookPath func(string) (string, error)) {
	t.Helper()
	previousLookPath := cmdLookPath
	t.Cleanup(func() { cmdLookPath = previousLookPath })
	cmdLookPath = func(name string) (string, error) {
		if name != "codegraph" {
			return "", errors.New("not found")
		}
		return lookPath(name)
	}
}

func installFakeCodeGraphMCP(t *testing.T, tools string) {
	t.Helper()
	binDir := t.TempDir()
	codeGraphPath := filepath.Join(binDir, "codegraph")
	script := "#!/bin/sh\n" +
		"[ \"$1\" = serve ] && [ \"$2\" = --mcp ] || exit 64\n" +
		"IFS= read -r initialize || exit 65\n" +
		"printf '%s\\n' '{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"protocolVersion\":\"2025-03-26\",\"capabilities\":{},\"serverInfo\":{\"name\":\"fake-codegraph\",\"version\":\"1\"}}}'\n" +
		"IFS= read -r initialized || exit 66\n" +
		"IFS= read -r tools_list || exit 67\n" +
		"printf '%s\\n' '{\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"tools\":" + tools + "}}'\n"
	if err := os.WriteFile(codeGraphPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// CodeGraph is mandatory, so every native reviewer install grants the Claude
// reviewers the CodeGraph explore tool without any selection.
func TestNativeReviewAgentStepAlwaysGrantsCodeGraphExplore(t *testing.T) {
	home := t.TempDir()
	var changed []string
	step := nativeReviewAgentStep{id: "native", agent: model.AgentClaudeCode, homeDir: home, workspaceDir: t.TempDir(), scope: ScopeGlobal, selection: model.Selection{}, changedFiles: &changed}
	if err := step.Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(changed) == 0 {
		t.Fatal("native review step wrote no reviewer files")
	}
	granted := false
	for _, path := range changed {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("mcp__codegraph__codegraph_explore")) {
			granted = true
		}
	}
	if !granted {
		t.Fatalf("no reviewer in %v grants mcp__codegraph__codegraph_explore", changed)
	}
}

func TestCodeGraphCommandEnvironment(t *testing.T) {
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory available: %v", err)
	}
	runtimeHome := t.TempDir()
	tests := []struct {
		name    string
		homeDir string
		xdg     string
		want    map[string]string
	}{
		{name: "real home inherits the process environment", homeDir: realHome, want: nil},
		{name: "real home keeps an absolute XDG_CONFIG_HOME", homeDir: realHome, xdg: "/custom/xdg", want: nil},
		{
			name:    "runtime home redirects every config root",
			homeDir: runtimeHome,
			want: map[string]string{
				"HOME":            runtimeHome,
				"XDG_CONFIG_HOME": filepath.Join(runtimeHome, ".config"),
				"CODEX_HOME":      filepath.Join(runtimeHome, ".codex"),
			},
		},
		{
			name:    "runtime home ignores the real user's XDG_CONFIG_HOME",
			homeDir: runtimeHome,
			xdg:     "/custom/xdg",
			want: map[string]string{
				"HOME":            runtimeHome,
				"XDG_CONFIG_HOME": filepath.Join(runtimeHome, ".config"),
				"CODEX_HOME":      filepath.Join(runtimeHome, ".codex"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", tt.xdg)
			if got := codeGraphCommandEnvironment(tt.homeDir); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("codeGraphCommandEnvironment(%q) = %#v, want %#v", tt.homeDir, got, tt.want)
			}
		})
	}
}

// The install step must run every CodeGraph command (package install,
// upgrade, `codegraph install`) against the runtime home, exactly like sync,
// so an install into a non-real home never touches the real one.
func TestCodeGraphInstallAndSyncRunAgainstTheRuntimeHome(t *testing.T) {
	home := t.TempDir()
	previousInstall := installCodeGraph
	previousRun := runCodeGraphCommand
	t.Cleanup(func() {
		installCodeGraph = previousInstall
		runCodeGraphCommand = previousRun
	})
	var envs []map[string]string
	runCodeGraphCommand = func(env map[string]string, name string, args ...string) error {
		envs = append(envs, env)
		return nil
	}
	installCodeGraph = func(_ model.CommunityToolID, _ string, _ string, r communitytool.Runner, _ communitytool.Detector) (communitytool.Result, error) {
		return communitytool.Result{}, r.Run("codegraph", "install", "--yes")
	}

	if err := (codeGraphInstallStep{id: "codegraph:install", workspaceDir: "/work/project", homeDir: home}).Run(); err != nil {
		t.Fatalf("install step Run() error = %v", err)
	}
	if err := (codeGraphHomeRunner{homeDir: home}).Run("codegraph", "install", "--yes"); err != nil {
		t.Fatalf("sync runner Run() error = %v", err)
	}

	want := codeGraphCommandEnvironment(home)
	if len(envs) != 2 || !reflect.DeepEqual(envs[0], want) || !reflect.DeepEqual(envs[1], want) {
		t.Fatalf("command environments = %#v, want install and sync both to use %#v", envs, want)
	}
	if envs[0]["HOME"] != home {
		t.Fatalf("install HOME = %q, want runtime home %q", envs[0]["HOME"], home)
	}
}
