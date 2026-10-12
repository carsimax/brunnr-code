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
	"github.com/gentleman-programming/gentle-ai/v4/internal/pipeline"
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

func TestBackupTargetsIncludeGentleLogoComponentPaths(t *testing.T) {
	home := t.TempDir()
	targets, err := backupTargets(home, "", ScopeGlobal, model.Selection{
		Agents:     []model.AgentID{model.AgentOpenCode},
		Components: []model.ComponentID{model.ComponentOpenCodeGentleLogo},
	}, planner.ResolvedPlan{Agents: []model.AgentID{model.AgentOpenCode}, OrderedComponents: []model.ComponentID{model.ComponentOpenCodeGentleLogo}})
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		filepath.Join(home, ".config", "opencode", "tui.json"),
		filepath.Join(home, ".config", "opencode", "tui-plugins", "gentle-logo.tsx"),
	} {
		if !slices.Contains(targets, path) {
			t.Fatalf("backup targets = %v, missing selected plugin path %q", targets, path)
		}
	}
}

func TestCodeGraphFailureDoesNotLeaveEarlierOpenCodePluginRegistration(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}

	previousInstall := installCommunityToolWithHome
	t.Cleanup(func() { installCommunityToolWithHome = previousInstall })
	installCommunityToolWithHome = func(model.CommunityToolID, string, string, communitytool.Runner, communitytool.Detector) (communitytool.Result, error) {
		return communitytool.Result{Tool: model.CommunityToolCodeGraph}, errors.New("CodeGraph reconciliation failed")
	}

	runtime, err := newInstallRuntime(home, ScopeGlobal, ChannelStable, model.Selection{
		Agents:         []model.AgentID{model.AgentOpenCode},
		Components:     []model.ComponentID{model.ComponentOpenCodeGentleLogo},
		CommunityTools: []model.CommunityToolID{model.CommunityToolCodeGraph},
	}, planner.ResolvedPlan{Agents: []model.AgentID{model.AgentOpenCode}, OrderedComponents: []model.ComponentID{model.ComponentOpenCodeGentleLogo}}, system.PlatformProfile{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.state.cleanupCompatibilityTransaction)

	result := pipeline.NewOrchestrator(pipeline.DefaultRollbackPolicy()).Execute(runtime.stagePlan())
	if result.Err == nil || !strings.Contains(result.Err.Error(), "CodeGraph reconciliation failed") {
		t.Fatalf("install pipeline error = %v, want CodeGraph reconciliation failure", result.Err)
	}

	tuiPath := filepath.Join(home, ".config", "opencode", "tui.json")
	if _, err := os.Stat(tuiPath); !os.IsNotExist(err) {
		t.Fatalf("OpenCode plugin registration remains after CodeGraph failure: %v", err)
	}
}

func TestInstallRollbackRestoresSelectedOpenCodePluginPathsAfterPluginRegistration(t *testing.T) {
	for _, test := range []struct {
		name        string
		preexisting bool
	}{
		{name: "removes paths created by plugins"},
		{name: "restores pre-existing bytes", preexisting: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			tuiPath := filepath.Join(home, ".config", "opencode", "tui.json")
			logoPath := filepath.Join(home, ".config", "opencode", "tui-plugins", "gentle-logo.tsx")
			originalTUI := []byte("{\n  \"plugin\": [\"existing-plugin\"]\n}\n")
			originalLogo := []byte("pre-existing Gentle Logo bytes\n")
			if test.preexisting {
				mustWriteFile(t, tuiPath, originalTUI)
				mustWriteFile(t, logoPath, originalLogo)
			}

			previousInstall := installCommunityToolWithHome
			t.Cleanup(func() { installCommunityToolWithHome = previousInstall })
			codeGraphSucceeded := false
			installCommunityToolWithHome = func(model.CommunityToolID, string, string, communitytool.Runner, communitytool.Detector) (communitytool.Result, error) {
				codeGraphSucceeded = true
				return communitytool.Result{Tool: model.CommunityToolCodeGraph}, nil
			}

			runtime, err := newInstallRuntime(home, ScopeGlobal, ChannelStable, model.Selection{
				Agents:         []model.AgentID{model.AgentOpenCode},
				Components:     []model.ComponentID{model.ComponentOpenCodeGentleLogo},
				CommunityTools: []model.CommunityToolID{model.CommunityToolCodeGraph},
			}, planner.ResolvedPlan{Agents: []model.AgentID{model.AgentOpenCode}, OrderedComponents: []model.ComponentID{model.ComponentOpenCodeGentleLogo}}, system.PlatformProfile{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(runtime.state.cleanupCompatibilityTransaction)

			plan := runtime.stagePlan()
			plan.Apply = append(plan.Apply, failAfterPluginRegistrationStep{
				codeGraphSucceeded: &codeGraphSucceeded,
				tuiPath:            tuiPath,
				logoPath:           logoPath,
			})
			result := pipeline.NewOrchestrator(pipeline.DefaultRollbackPolicy()).Execute(plan)
			if result.Err == nil || !strings.Contains(result.Err.Error(), "late plugin rollback control") {
				t.Fatalf("install pipeline error = %v, want late plugin rollback failure", result.Err)
			}
			if !result.Rollback.Success {
				t.Fatalf("rollback = %#v, want successful outer restore", result.Rollback)
			}

			if !test.preexisting {
				for _, path := range []string{tuiPath, logoPath} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("created plugin path remains after rollback %q: %v", path, err)
					}
				}
				return
			}
			for path, want := range map[string][]byte{tuiPath: originalTUI, logoPath: originalLogo} {
				got, err := os.ReadFile(path)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("restored %q = %q, %v; want original bytes %q", path, got, err, want)
				}
			}
		})
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

func TestSuccessfulCodeGraphReconciliationStillRegistersOpenCodePlugin(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}

	previousInstall := installCommunityToolWithHome
	t.Cleanup(func() { installCommunityToolWithHome = previousInstall })
	installCommunityToolWithHome = func(model.CommunityToolID, string, string, communitytool.Runner, communitytool.Detector) (communitytool.Result, error) {
		return communitytool.Result{Tool: model.CommunityToolCodeGraph}, nil
	}

	runtime, err := newInstallRuntime(home, ScopeGlobal, ChannelStable, model.Selection{
		Agents:         []model.AgentID{model.AgentOpenCode},
		Components:     []model.ComponentID{model.ComponentOpenCodeGentleLogo},
		CommunityTools: []model.CommunityToolID{model.CommunityToolCodeGraph},
	}, planner.ResolvedPlan{Agents: []model.AgentID{model.AgentOpenCode}, OrderedComponents: []model.ComponentID{model.ComponentOpenCodeGentleLogo}}, system.PlatformProfile{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.state.cleanupCompatibilityTransaction)

	result := pipeline.NewOrchestrator(pipeline.DefaultRollbackPolicy()).Execute(runtime.stagePlan())
	if result.Err != nil {
		t.Fatalf("install pipeline error = %v", result.Err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".config", "opencode", "tui.json"))
	if err != nil || !strings.Contains(string(data), "gentle-logo.tsx") {
		t.Fatalf("OpenCode plugin registration = %q, %v; want successful registration", data, err)
	}
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
				mustWriteFile(t, filepath.Join(home, ".config", "opencode", "opencode.json"), []byte(`{}`))
				mustWriteFile(t, filepath.Join(home, ".config", "opencode", "AGENTS.md"), []byte(strings.Join([]string{
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

func TestCommunityToolGuidanceOnRetainedOpenCodePrompt(t *testing.T) {
	for _, tc := range []struct {
		name     string
		selected []model.CommunityToolID
		lookPath func(string) (string, error)
		want     bool
	}{
		{name: "selected despite unavailable CLI", selected: []model.CommunityToolID{model.CommunityToolCodeGraph}, lookPath: func(string) (string, error) { return "", errors.New("not found") }, want: true},
		{name: "configured elsewhere but not selected", lookPath: func(string) (string, error) { return "/bin/codegraph", nil }},
		{name: "CLI available but not selected", lookPath: func(string) (string, error) { return "/bin/codegraph", nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			withCodeGraphLookPath(t, tc.lookPath)
			promptPath := filepath.Join(home, ".config", "opencode", "AGENTS.md")
			mustWriteFile(t, filepath.Join(home, ".config", "opencode", "opencode.json"), []byte(`{}`))
			mustWriteFile(t, promptPath, []byte("user-owned OpenCode instructions\n"))
			if tc.name == "configured elsewhere but not selected" {
				mustWriteFile(t, filepath.Join(home, ".codex", "config.toml"), []byte("[mcp_servers.codegraph]\ncommand = \"codegraph\"\n"))
			}

			result, err := communitytool.InjectCodeGraphGuidanceIfSelected(home, tc.selected)
			if err != nil {
				t.Fatalf("InjectCodeGraphGuidanceIfSelected() error = %v", err)
			}
			if result.Changed != tc.want {
				t.Fatalf("guidance changed = %v, want %v", result.Changed, tc.want)
			}
			assertOpenCodeSharedPromptCodeGraphGuidance(t, home, tc.want)
		})
	}

	t.Run("selected tool does not create an undetected OpenCode installation", func(t *testing.T) {
		home := t.TempDir()
		result, err := communitytool.InjectCodeGraphGuidanceIfSelected(home, []model.CommunityToolID{model.CommunityToolCodeGraph})
		if err != nil || result.Changed {
			t.Fatalf("undetected OpenCode guidance = %#v, %v; want no changes", result, err)
		}
		promptPath := filepath.Join(home, ".config", "opencode", "AGENTS.md")
		if _, err := os.Stat(promptPath); !os.IsNotExist(err) {
			t.Fatalf("undetected OpenCode prompt was created: %v", err)
		}
	})
}

func TestComponentSyncStepOmitsCodeGraphGuidanceFromLegacyMarkerWithoutSelection(t *testing.T) {
	home := t.TempDir()
	withCodeGraphLookPath(t, func(string) (string, error) { return "", errors.New("not found") })
	mustWriteFile(t, filepath.Join(home, ".config", "opencode", "opencode.json"), []byte(`{}`))
	mustWriteFile(t, filepath.Join(home, ".config", "opencode", "AGENTS.md"), []byte(strings.Join([]string{
		"custom notes",
		"<!-- CODEGRAPH_START -->",
		"old CodeGraph instructions",
		"<!-- CODEGRAPH_END -->",
	}, "\n")))

	var changed []string
	step := codeGraphGuidanceSyncStep{homeDir: home, changedFiles: &changed}
	if err := step.Run(); err != nil {
		t.Fatalf("codeGraphGuidanceSyncStep.Run() error = %v", err)
	}

	promptPath := filepath.Join(home, ".config", "opencode", "AGENTS.md")
	data, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "CODEGRAPH_START") || strings.Contains(string(data), "old CodeGraph instructions") {
		t.Fatalf("legacy guidance remains without selection: %s", data)
	}
	if !strings.Contains(string(data), "custom notes") {
		t.Fatalf("cleanup removed user-owned guidance: %s", data)
	}
	if !slices.Contains(changed, promptPath) {
		t.Fatalf("changed paths = %v, want cleaned guidance", changed)
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

func assertOpenCodeSharedPromptCodeGraphGuidance(t *testing.T, home string, want bool) {
	t.Helper()
	promptPath := filepath.Join(home, ".config", "opencode", "AGENTS.md")
	content, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", promptPath, err)
	}
	text := string(content)
	if !strings.Contains(text, "user-owned OpenCode instructions") {
		t.Fatalf("user-owned OpenCode guidance was overwritten: %s", text)
	}
	hasGuidance := strings.Contains(text, "<!-- gentle-ai:codegraph-guidance -->") && strings.Contains(text, "gentle-ai codegraph init --cwd <project-root>")
	if hasGuidance != want {
		t.Fatalf("CodeGraph guidance present = %v, want %v in %s", hasGuidance, want, promptPath)
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
