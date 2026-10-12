package cli

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/pipeline"
	"github.com/carsimax/brunnr-code/internal/planner"
	"github.com/carsimax/brunnr-code/internal/system"
)

type claudeHooksRollbackFailureStep struct{ run func() error }

func (claudeHooksRollbackFailureStep) ID() string   { return "test:fail-after-claude-hooks" }
func (s claudeHooksRollbackFailureStep) Run() error { return s.run() }

// TestInstallClaudeHooksRollback injects a failure after the scoped Claude
// hooks are written and checks that rollback restores settings. The managed
// persona is mandatory, so every install also selects it; the component-free
// snapshot of the hook settings is pinned separately by
// TestInstallBackupTargetsSnapshotClaudeSettingsWithoutPersona.
func TestInstallClaudeHooksRollback(t *testing.T) {
	for _, scope := range []InstallScope{ScopeWorkspace, ScopeGlobal} {
		for _, existing := range []bool{false, true} {
			name := string(scope) + "/new settings"
			if existing {
				name = string(scope) + "/existing settings"
			}
			t.Run(name, func(t *testing.T) {
				home, workspace := installTestHome(t), t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("USERPROFILE", home)
				t.Chdir(workspace)
				target, untouched := workspace, home
				if scope == ScopeGlobal {
					target, untouched = home, workspace
				}
				settings := filepath.Join(target, ".claude", "settings.json")
				otherSettings := filepath.Join(untouched, ".claude", "settings.json")
				const personal = "{\"hooks\":{\"Stop\":[{\"hooks\":[{\"type\":\"command\",\"command\":\"echo personal\"}]}]},\"custom\":true}\n"
				mustWriteFile(t, otherSettings, []byte(personal))
				if existing {
					mustWriteFile(t, settings, []byte(personal))
				}
				before := snapshotTree(t, home)
				priorPlan := installStagePlan
				t.Cleanup(func() { installStagePlan = priorPlan })
				failure := errors.New("injected failure after scoped Claude hooks")
				observed := false
				installStagePlan = func(rt *installRuntime) pipeline.StagePlan {
					if want := []model.ComponentID{model.ComponentSkills, model.ComponentPersona}; !slices.Equal(rt.selection.Components, want) {
						t.Fatalf("selection = %v, want %v (skills plus the mandatory persona)", rt.selection.Components, want)
					}
					plan := priorPlan(rt)
					plan.Apply = append(plan.Apply, claudeHooksRollbackFailureStep{run: func() error {
						observed = strings.Count(readTextFile(t, settings), "brunnr-code review stop-hook --agent claude-code") == 2
						return failure
					}})
					return plan
				}
				result, err := RunInstall([]string{"--scope=" + string(scope), "--agent=claude-code", "--components=skills", "--skills=go-testing"}, system.DetectionResult{})
				if err == nil || !strings.Contains(err.Error(), failure.Error()) || !result.Execution.Rollback.Success {
					t.Fatalf("RunInstall() = %v, rollback=%+v; want injected failure and successful rollback", err, result.Execution.Rollback)
				}
				if !observed {
					t.Fatal("failure occurred without observing the scoped hook writes")
				}
				if existing {
					if got := readTextFile(t, settings); got != personal {
						t.Errorf("rollback did not restore personal settings: %s", got)
					}
				} else if _, err := os.Stat(settings); !os.IsNotExist(err) {
					t.Errorf("rollback left newly created settings: %v", err)
				}
				if got := readTextFile(t, otherSettings); got != personal {
					t.Error("install/rollback changed opposite-scope settings")
				}
				// Backups are expected, but no install state may be published on failure.
				statePath := filepath.Join(home, ".brunnr-code", "state.json")
				if _, exists := before[filepath.Join(".brunnr-code", "state.json")]; !exists {
					if _, err := os.Stat(statePath); !os.IsNotExist(err) {
						t.Errorf("failed install published state: %v", err)
					}
				}
			})
		}
	}
}

// TestInstallBackupTargetsSnapshotClaudeSettingsWithoutPersona pins that the
// retained Claude hooks snapshot their scoped settings independently of the
// components in the plan, so the snapshot does not depend on persona (the
// only component that also declares settings.json).
func TestInstallBackupTargetsSnapshotClaudeSettingsWithoutPersona(t *testing.T) {
	for _, scope := range []InstallScope{ScopeWorkspace, ScopeGlobal} {
		t.Run(string(scope), func(t *testing.T) {
			home, workspace := t.TempDir(), t.TempDir()
			target := workspace
			if scope == ScopeGlobal {
				target = home
			}
			selection := model.Selection{Agents: []model.AgentID{model.AgentClaudeCode}, Components: []model.ComponentID{model.ComponentSkills}}
			resolved := planner.ResolvedPlan{Agents: selection.Agents, OrderedComponents: selection.Components}
			paths, err := backupTargets(home, workspace, scope, selection, resolved)
			if err != nil {
				t.Fatal(err)
			}
			if settings := filepath.Join(target, ".claude", "settings.json"); !slices.Contains(paths, settings) {
				t.Fatalf("backup targets miss scoped Claude settings %s: %v", settings, paths)
			}
		})
	}
}

func TestInstallClaudeHooksRespectScope(t *testing.T) {
	for _, scope := range []InstallScope{ScopeWorkspace, ScopeGlobal} {
		t.Run(string(scope), func(t *testing.T) {
			home, workspace := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Chdir(workspace)
			previousHome, previousCommand, previousLookPath := osUserHomeDir, runCommand, cmdLookPath
			osUserHomeDir = func() (string, error) { return home, nil }
			runCommand = func(string, ...string) error { return nil }
			cmdLookPath = func(string) (string, error) { return "", exec.ErrNotFound }
			t.Cleanup(func() {
				osUserHomeDir, runCommand, cmdLookPath = previousHome, previousCommand, previousLookPath
			})

			const original = "{\"hooks\":{\"Stop\":[{\"hooks\":[{\"type\":\"command\",\"command\":\"echo personal\"}]}]},\"custom\":true}\n"
			target, untouched := workspace, home
			if scope == ScopeGlobal {
				target, untouched = home, workspace
			}
			for _, root := range []string{home, workspace} {
				mustWriteFile(t, filepath.Join(root, ".claude", "settings.json"), []byte(original))
			}
			install := func() {
				t.Helper()
				_, err := RunInstall([]string{"--scope=" + string(scope), "--agent=claude-code", "--components=persona"}, system.DetectionResult{})
				if err != nil {
					t.Fatalf("RunInstall() error = %v", err)
				}
			}
			install()
			path := filepath.Join(target, ".claude", "settings.json")
			first := readTextFile(t, path)
			var settings struct {
				Custom bool `json:"custom"`
				Hooks  map[string][]struct {
					Hooks []struct {
						Command string `json:"command"`
					} `json:"hooks"`
				} `json:"hooks"`
			}
			if err := json.Unmarshal([]byte(first), &settings); err != nil {
				t.Fatal(err)
			}
			got := map[string]int{}
			personal := 0
			for event, entries := range settings.Hooks {
				for _, entry := range entries {
					for _, hook := range entry.Hooks {
						if strings.HasPrefix(hook.Command, "brunnr-code ") || strings.Contains(hook.Command, "brunnr-code skill-registry refresh") {
							got[event]++
						}
						if hook.Command == "echo personal" {
							personal++
						}
					}
				}
			}
			want := map[string]int{"SessionStart": 1, "Stop": 1, "SubagentStop": 0, "UserPromptSubmit": 1}
			for event, count := range want {
				if got[event] != count {
					t.Errorf("managed %s hooks = %d, want %d", event, got[event], count)
				}
			}
			if !settings.Custom || personal != 1 {
				t.Error("install changed personal settings or hooks")
			}
			untouchedPath := filepath.Join(untouched, ".claude", "settings.json")
			if readTextFile(t, untouchedPath) != original {
				t.Error("install modified settings outside the selected scope")
			}
			install()
			if readTextFile(t, path) != first {
				t.Error("second install changed hook settings")
			}
			if readTextFile(t, untouchedPath) != original {
				t.Error("second install modified settings outside the selected scope")
			}
		})
	}
}
