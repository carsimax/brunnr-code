package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/components/communitytool"
	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
	"github.com/gentleman-programming/gentle-ai/v4/internal/planner"
	"github.com/gentleman-programming/gentle-ai/v4/internal/system"
)

func TestInstallRuntimeStagePlanAddsCommunityToolStepsInSelectionOrder(t *testing.T) {
	runtime := &installRuntime{
		homeDir:      t.TempDir(),
		workspaceDir: "/work/project",
		selection: model.Selection{
			CommunityTools: []model.CommunityToolID{model.CommunityToolCodeGraph},
		},
		resolved: planner.ResolvedPlan{},
		profile:  system.PlatformProfile{},
		state:    &runtimeState{},
	}

	plan := runtime.stagePlan()
	var got []string
	for _, step := range plan.Apply {
		got = append(got, step.ID())
	}
	want := []string{"apply:rollback-restore", "community-tool:codegraph"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("apply step IDs = %#v, want %#v", got, want)
	}
}

func TestBackupTargetsSnapshotCrossAgentCodeGraphGuidance(t *testing.T) {
	home := t.TempDir()
	claudeConfig := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	selection := model.Selection{CommunityTools: []model.CommunityToolID{model.CommunityToolCodeGraph}}
	targets, err := backupTargets(home, "", ScopeGlobal, selection, planner.ResolvedPlan{})
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
		return fmt.Errorf("Gentle Logo was not written before rollback control: %w", err)
	}
	return errors.New("late plugin rollback control")
}

func TestNativeReviewCodeGraphGuidanceMarkdownOnlyWhenSelected(t *testing.T) {
	tests := []struct {
		name      string
		setupHome func(t *testing.T, home string)
		lookPath  func(string) (string, error)
		selected  []model.CommunityToolID
		want      bool
	}{
		{
			name:     "CLI missing and no selection",
			lookPath: func(string) (string, error) { return "", errors.New("not found") },
		},
		{
			name:     "CLI available but not configured",
			lookPath: func(string) (string, error) { return "/bin/codegraph", nil },
		},
		{
			name:     "selected CodeGraph",
			lookPath: func(string) (string, error) { return "", errors.New("not found") },
			selected: []model.CommunityToolID{model.CommunityToolCodeGraph},
			want:     true,
		},
		{
			name: "managed guidance without selection",
			setupHome: func(t *testing.T, home string) {
				mustWriteFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), []byte(strings.Join([]string{
					"existing Claude guidance",
					"<!-- gentle-ai:codegraph-guidance -->",
					"CodeGraph guidance with `gentle-ai codegraph init --cwd <project-root>`",
					"<!-- /gentle-ai:codegraph-guidance -->",
				}, "\n")))
			},
			lookPath: func(string) (string, error) { return "/bin/codegraph", nil },
		},
		{
			name: "MCP wiring without selection",
			setupHome: func(t *testing.T, home string) {
				mustWriteFile(t, filepath.Join(home, ".codex", "config.toml"), []byte(strings.Join([]string{
					`[mcp_servers.codegraph]`,
					`command = "codegraph"`,
				}, "\n")))
			},
			lookPath: func(string) (string, error) { return "/bin/codegraph", nil },
		},
		{
			name: "legacy marker without selection",
			setupHome: func(t *testing.T, home string) {
				mustWriteFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), []byte(strings.Join([]string{
					"custom notes",
					"<!-- CODEGRAPH_START -->",
					"old CodeGraph instructions",
					"<!-- CODEGRAPH_END -->",
				}, "\n")))
			},
			lookPath: func(string) (string, error) { return "/bin/codegraph", nil },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			previousLookPath := cmdLookPath
			t.Cleanup(func() { cmdLookPath = previousLookPath })
			cmdLookPath = tc.lookPath

			home := t.TempDir()
			if tc.setupHome != nil {
				tc.setupHome(t, home)
			}

			got := nativeReviewCodeGraphGuidanceMarkdown(home, tc.selected)
			if !tc.want {
				if got != "" {
					t.Fatalf("guidance = %q, want empty", got)
				}
				return
			}
			if !strings.Contains(got, "gentle-ai codegraph init --cwd <project-root>") {
				t.Fatalf("CodeGraph guidance missing search-order rule:\n%s", got)
			}
		})
	}
}

func TestCommunityToolInstallStepUsesInjectableInstaller(t *testing.T) {
	previousInstall := installCommunityToolWithHome
	previousRunCommand := runCommand
	t.Cleanup(func() {
		installCommunityToolWithHome = previousInstall
		runCommand = previousRunCommand
	})

	runCommand = func(string, ...string) error {
		t.Fatal("communityToolInstallStep should not call real command runner when installer is injected")
		return nil
	}

	var gotTool model.CommunityToolID
	var gotWorkspace string
	var runner communitytool.Runner
	installCommunityToolWithHome = func(tool model.CommunityToolID, workspaceDir string, _ string, r communitytool.Runner, _ communitytool.Detector) (communitytool.Result, error) {
		gotTool = tool
		gotWorkspace = workspaceDir
		runner = r
		return communitytool.Result{Tool: tool}, nil
	}

	step := communityToolInstallStep{id: "community-tool:codegraph", tool: model.CommunityToolCodeGraph, workspaceDir: "/work/project"}
	if err := step.Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if gotTool != model.CommunityToolCodeGraph || gotWorkspace != "/work/project" || runner == nil {
		t.Fatalf("installer args = (%q, %q, %#v), want CodeGraph, workspace, runner", gotTool, gotWorkspace, runner)
	}
}

func TestCommunityToolInstallStepPassesRuntimeHomeToPiReconciler(t *testing.T) {
	previous := installCommunityToolWithHome
	t.Cleanup(func() { installCommunityToolWithHome = previous })
	var gotHome string
	installCommunityToolWithHome = func(_ model.CommunityToolID, _ string, home string, _ communitytool.Runner, _ communitytool.Detector) (communitytool.Result, error) {
		gotHome = home
		return communitytool.Result{Tool: model.CommunityToolCodeGraph}, nil
	}
	step := communityToolInstallStep{id: "community-tool:codegraph", tool: model.CommunityToolCodeGraph, workspaceDir: "/work/project", homeDir: "/tmp/pi-home"}
	if err := step.Run(); err != nil {
		t.Fatal(err)
	}
	if gotHome != "/tmp/pi-home" {
		t.Fatalf("home = %q, want runtime home", gotHome)
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
