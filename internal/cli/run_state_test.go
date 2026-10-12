package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/carsimax/brunnr-code/internal/backup"

	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/state"
	"github.com/carsimax/brunnr-code/internal/system"
)

func TestMergeExplicitAgentInstallStatePreservesExistingAssignmentsWhenFreshStateIsEmpty(t *testing.T) {
	home := t.TempDir()
	if err := state.Write(home, state.InstallState{
		InstalledAgents: []string{"claude-code"},
		ClaudeModelAssignments: map[string]string{
			"odd-worker": "opus",
		},
		CodexModelAssignments: map[string]string{
			"odd-worker": "low",
		},
		CodexCarrilModelAssignments: map[string]string{
			"odd-verify": "gpt-5.5",
		},
		CodexPhaseModelAssignments: map[string]string{
			"odd-verify": "gpt-5.4",
		},
	}); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	merged, err := mergeExplicitAgentInstallState(home, state.InstallState{InstalledAgents: []string{"codex"}}, []string{"codex"}, InstallFlags{})
	if err != nil {
		t.Fatalf("mergeExplicitAgentInstallState() error = %v, want nil", err)
	}
	if got := merged.InstalledAgents; len(got) != 2 || got[0] != "claude-code" || got[1] != "codex" {
		t.Fatalf("InstalledAgents = %#v, want [claude-code codex]", got)
	}
	if merged.ClaudeModelAssignments["odd-worker"] != "opus" {
		t.Fatalf("ClaudeModelAssignments not preserved: %#v", merged.ClaudeModelAssignments)
	}
	if merged.CodexModelAssignments["odd-worker"] != "low" {
		t.Fatalf("CodexModelAssignments not preserved: %#v", merged.CodexModelAssignments)
	}
	if merged.CodexCarrilModelAssignments["odd-verify"] != "gpt-5.5" {
		t.Fatalf("CodexCarrilModelAssignments not preserved: %#v", merged.CodexCarrilModelAssignments)
	}
	if merged.CodexPhaseModelAssignments["odd-verify"] != "gpt-5.4" {
		t.Fatalf("CodexPhaseModelAssignments not preserved: %#v", merged.CodexPhaseModelAssignments)
	}
}

// TestMergeExplicitAgentInstallStateDropsUnsupportedAgents proves an
// incremental install silently drops persisted agents this build no longer
// supports instead of carrying them forward.
func TestMergeExplicitAgentInstallStateDropsUnsupportedAgents(t *testing.T) {
	home := t.TempDir()
	if err := state.Write(home, state.InstallState{InstalledAgents: []string{"opencode", "claude-code"}, SelectionConfigured: true}); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	merged, err := mergeExplicitAgentInstallState(home, state.InstallState{InstalledAgents: []string{"codex"}}, []string{"codex"}, InstallFlags{})
	if err != nil {
		t.Fatalf("mergeExplicitAgentInstallState() error = %v, want nil", err)
	}
	if got := merged.InstalledAgents; len(got) != 2 || got[0] != "claude-code" || got[1] != "codex" {
		t.Fatalf("InstalledAgents = %#v, want [claude-code codex]", got)
	}
}

func TestMergeExplicitAgentInstallStateMergesOnlyExplicitSelectionField(t *testing.T) {
	original := state.InstallState{InstalledAgents: []string{"claude-code"}, SelectionConfigured: true, Components: []model.ComponentID{model.ComponentEngram}, Skills: []model.SkillID{model.SkillCommentWriter}, Preset: model.PresetCustom}
	fresh := state.InstallState{InstalledAgents: []string{"codex"}, SelectionConfigured: true, Components: []model.ComponentID{model.ComponentPersona}, Skills: []model.SkillID{model.SkillGoTesting}, Preset: model.PresetFull}
	cases := []InstallFlags{{Components: []string{"persona"}}, {Skills: []string{"go-testing"}}, {Preset: "full"}}
	wants := []string{"[persona]|[comment-writer]|custom", "[engram]|[go-testing]|custom", "[engram]|[comment-writer]|full"}
	for i, flags := range cases {
		home := t.TempDir()
		if err := state.Write(home, original); err != nil {
			t.Fatal(err)
		}
		persisted, err := os.ReadFile(state.Path(home))
		if err != nil {
			t.Fatal(err)
		}
		legacy := strings.Replace(string(persisted), "{", `{"strict_tdd":true,`, 1)
		if err := os.WriteFile(state.Path(home), []byte(legacy), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := mergeExplicitAgentInstallState(home, fresh, []string{"codex"}, flags)
		if err != nil {
			t.Fatalf("flags %#v merge error: %v", flags, err)
		}
		key := fmt.Sprintf("%v|%v|%s", got.Components, got.Skills, got.Preset)
		if key != wants[i] || got.StrictTDD || !got.SelectionConfigured || len(got.InstalledAgents) != 2 || got.InstalledAgents[0] != "claude-code" || got.InstalledAgents[1] != "codex" {
			t.Errorf("flags %#v merged state %#v, selection %s, want %s with legacy StrictTDD ignored and agents preserved", flags, got, key, wants[i])
		}
	}
}

func TestRunInstallPersistsConfiguredSelection(t *testing.T) {
	home := t.TempDir()
	original := osUserHomeDir
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { osUserHomeDir = original })
	if _, err := RunInstall([]string{"--agent", "claude-code", "--preset", "custom"}, system.DetectionResult{}); err != nil {
		t.Fatal(err)
	}
	got, err := state.Read(home)
	if err != nil || !got.SelectionConfigured || got.Preset != model.PresetCustom || len(got.Components) != 0 || len(got.InstalledAgents) != 1 || got.InstalledAgents[0] != "claude-code" {
		t.Fatalf("persisted selection = %#v, err = %v", got, err)
	}
	wantDigest, err := managedAssetDigest()
	if err != nil {
		t.Fatal(err)
	}
	if got.ManagedAssetDigest != wantDigest {
		t.Fatalf("managed asset digest = %q, want %q", got.ManagedAssetDigest, wantDigest)
	}
}

func TestRunInstallExplicitSelectionThenSync(t *testing.T) {
	lastCheck := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name     string
		existing *state.InstallState
	}{
		{name: "no prior state"},
		{name: "update-check stub", existing: &state.InstallState{LastUpdateCheck: &lastCheck}},
		{name: "legacy install without selection", existing: &state.InstallState{
			InstalledAgents: []string{"claude-code"}, LastUpdateCheck: &lastCheck,
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			home := convergenceTestHome(t)
			oldBackupHome := backup.UserHomeDirFn
			backup.UserHomeDirFn = func() (string, error) { return home, nil }
			t.Cleanup(func() { backup.UserHomeDirFn = oldBackupHome })
			if tt.existing != nil {
				if err := state.Write(home, *tt.existing); err != nil {
					t.Fatal(err)
				}
			}

			flags := InstallFlags{Agents: []string{"claude-code"}, Components: []string{"persona", "skills"}, Preset: "minimal"}
			input, err := NormalizeInstallFlags(flags, system.DetectionResult{})
			if err != nil {
				t.Fatal(err)
			}
			installed, err := RunInstall([]string{"--agent", "claude-code", "--component", "persona,skills", "--preset", "minimal"}, system.DetectionResult{})
			if err != nil || !installed.Verify.Ready {
				t.Fatalf("RunInstall() error = %v, ready = %v", err, installed.Verify.Ready)
			}
			got, err := state.Read(home)
			if err != nil {
				t.Fatal(err)
			}
			wantComponents := []model.ComponentID{model.ComponentPersona, model.ComponentSkills}
			if !got.SelectionConfigured || !slices.Equal(got.Components, wantComponents) || !slices.Equal(got.Skills, input.Selection.Skills) || got.Preset != model.PresetMinimal || !slices.Equal(got.InstalledAgents, []string{"claude-code"}) {
				t.Fatalf("persisted selection = %#v, want explicit components, skills and minimal preset", got)
			}
			if tt.existing != nil && (got.LastUpdateCheck == nil || !got.LastUpdateCheck.Equal(lastCheck)) {
				t.Fatalf("LastUpdateCheck = %v, want %v", got.LastUpdateCheck, lastCheck)
			}
			before := snapshotManagedTree(t, home)
			synced, err := RunSync(nil)
			if err != nil {
				t.Fatalf("RunSync() error = %v", err)
			}
			if !slices.Equal(synced.Selection.Components, wantComponents) || !slices.Equal(synced.Selection.Skills, input.Selection.Skills) || synced.Selection.Preset != model.PresetMinimal {
				t.Fatalf("sync selection = %#v, want recorded explicit selection", synced.Selection)
			}
			assertSameManagedTree(t, "sync after explicit install", before, snapshotManagedTree(t, home))
			if synced.FilesChanged != 0 {
				t.Errorf("sync changed %d files: %v", synced.FilesChanged, synced.ChangedFiles)
			}
		})
	}
}

func TestRunInstallIncrementalSelectionPreservesUnspecifiedFields(t *testing.T) {
	t.Chdir(t.TempDir())
	home := convergenceTestHome(t)
	if err := state.Write(home, state.InstallState{
		InstalledAgents: []string{"codex"}, SelectionConfigured: true,
		Components: []model.ComponentID{model.ComponentSkills, model.ComponentEngram},
		Skills:     []model.SkillID{model.SkillCommentWriter}, Preset: model.PresetCustom}); err != nil {
		t.Fatal(err)
	}
	if _, err := RunInstall([]string{"--agent", "claude-code", "--component", "persona"}, system.DetectionResult{}); err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}
	got, err := state.Read(home)
	if err != nil {
		t.Fatal(err)
	}
	if !got.SelectionConfigured || !slices.Equal(got.Components, []model.ComponentID{model.ComponentPersona}) || !slices.Equal(got.Skills, []model.SkillID{model.SkillCommentWriter}) || got.Preset != model.PresetCustom || !slices.Equal(got.InstalledAgents, []string{"codex", "claude-code"}) {
		t.Fatalf("incremental selection = %#v, want explicit components with existing skills, preset and agents preserved", got)
	}
}

func TestMergeExplicitAgentInstallStatePreservesFreshAssignments(t *testing.T) {
	home := t.TempDir()
	if err := state.Write(home, state.InstallState{
		InstalledAgents: []string{"claude-code"},
		CodexModelAssignments: map[string]string{
			"odd-worker": "low",
		},
	}); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	fresh := state.InstallState{
		InstalledAgents: []string{"codex"},
		CodexModelAssignments: codexEffortsToStrings(map[string]model.CodexEffort{
			"odd-worker": model.CodexEffortHigh,
		}),
		CodexCarrilModelAssignments: map[string]string{
			"odd-verify": "gpt-5.5",
		},
		CodexPhaseModelAssignments: map[string]string{
			"odd-worker": "gpt-5.4",
		},
	}

	merged, err := mergeExplicitAgentInstallState(home, fresh, []string{"codex"}, InstallFlags{})
	if err != nil {
		t.Fatalf("mergeExplicitAgentInstallState() error = %v, want nil", err)
	}
	if got := merged.InstalledAgents; len(got) != 2 || got[0] != "claude-code" || got[1] != "codex" {
		t.Fatalf("InstalledAgents = %#v, want [claude-code codex]", got)
	}
	if merged.CodexModelAssignments["odd-worker"] != "high" {
		t.Fatalf("CodexModelAssignments[odd-worker] = %q, want high", merged.CodexModelAssignments["odd-worker"])
	}
	if merged.CodexCarrilModelAssignments["odd-verify"] != "gpt-5.5" {
		t.Fatalf("CodexCarrilModelAssignments not preserved: %#v", merged.CodexCarrilModelAssignments)
	}
	if merged.CodexPhaseModelAssignments["odd-worker"] != "gpt-5.4" {
		t.Fatalf("CodexPhaseModelAssignments not preserved: %#v", merged.CodexPhaseModelAssignments)
	}
}

// TestMergeExplicitAgentInstallStateFailsHonestlyOnCorruptState was renamed
// from TestMergeExplicitAgentInstallStateSkipsCorruptState (install/sync
// surface audit finding 2). The old assertion (ok == false, no error) let
// RunInstall silently return (result, nil) on an unreadable/corrupted
// ~/.brunnr-code/state.json — the pipeline ran to completion, but the user's
// agent selection was never persisted and the CLI reported success anyway.
// The honest contract is: an unreadable existing state during an explicit
// `--agent` install must fail loudly instead of vanishing.
func TestMergeExplicitAgentInstallStateFailsHonestlyOnCorruptState(t *testing.T) {
	home := t.TempDir()
	statePath := state.Path(home)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(statePath, []byte("{not valid json\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := mergeExplicitAgentInstallState(home, state.InstallState{InstalledAgents: []string{"codex"}}, []string{"codex"}, InstallFlags{})
	if err == nil {
		t.Fatal("mergeExplicitAgentInstallState() error = nil for corrupt state, want a non-nil error")
	}
}

// TestRunInstallFailsHonestlyWhenExistingStateIsCorruptDuringExplicitAgentInstall
// closes install/sync surface audit finding 2: previously, `brunnr-code install
// --agent X` against a corrupted ~/.brunnr-code/state.json completed the whole
// pipeline (files written, verification passed) and RunInstall returned
// (result, nil) -- reported success -- WITHOUT ever calling state.Write. The
// user believed the install fully completed; state.json stayed corrupted
// forever, silently breaking every future `brunnr-code sync`.
func TestRunInstallFailsHonestlyWhenExistingStateIsCorruptDuringExplicitAgentInstall(t *testing.T) {
	home := t.TempDir()
	original := osUserHomeDir
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { osUserHomeDir = original })

	statePath := state.Path(home)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(statePath, []byte("{not valid json\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := RunInstall([]string{"--agent", "claude-code", "--preset", "custom"}, system.DetectionResult{})
	if err == nil {
		t.Fatal("RunInstall() error = nil, want an error naming the unreadable install state instead of a silent success")
	}
}
