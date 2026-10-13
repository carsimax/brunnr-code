package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/carsimax/brunnr-code/internal/backup"
	"github.com/carsimax/brunnr-code/internal/components/skills"

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

	merged, err := mergeExplicitAgentInstallState(home, state.InstallState{InstalledAgents: []string{"codex"}}, []string{"codex"})
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

	merged, err := mergeExplicitAgentInstallState(home, state.InstallState{InstalledAgents: []string{"codex"}}, []string{"codex"})
	if err != nil {
		t.Fatalf("mergeExplicitAgentInstallState() error = %v, want nil", err)
	}
	if got := merged.InstalledAgents; len(got) != 2 || got[0] != "claude-code" || got[1] != "codex" {
		t.Fatalf("InstalledAgents = %#v, want [claude-code codex]", got)
	}
}

// TestMergeExplicitAgentInstallStateReplacesLegacySelection pins that an
// incremental install replaces an older preset, component or skill selection
// with the fresh setup while preserving agents and ignoring legacy StrictTDD.
func TestMergeExplicitAgentInstallStateReplacesLegacySelection(t *testing.T) {
	home := t.TempDir()
	legacy := `{"strict_tdd":true,"installed_agents":["claude-code"],"selection_configured":true,"components":["engram"],"skills":["comment-writer"],"preset":"custom"}`
	if err := os.MkdirAll(filepath.Dir(state.Path(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.Path(home), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh := state.InstallState{InstalledAgents: []string{"codex"}}
	fresh.SetSelection(model.Selection{Preset: model.PresetFull, Components: model.SetupComponents(), Skills: skills.SetupSkills()})

	got, err := mergeExplicitAgentInstallState(home, fresh, []string{"codex"})
	if err != nil {
		t.Fatalf("merge error: %v", err)
	}
	if !got.SelectionConfigured || got.StrictTDD || got.Preset != model.PresetFull || !slices.Equal(got.Components, model.SetupComponents()) || !slices.Equal(got.Skills, skills.SetupSkills()) {
		t.Fatalf("merged selection = %#v, want the full setup", got)
	}
	if !slices.Equal(got.InstalledAgents, []string{"claude-code", "codex"}) {
		t.Fatalf("InstalledAgents = %v, want [claude-code codex]", got.InstalledAgents)
	}
}

func TestRunInstallPersistsConfiguredSelection(t *testing.T) {
	home := t.TempDir()
	original := osUserHomeDir
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { osUserHomeDir = original })
	if _, err := RunInstall([]string{"--agent", "claude-code"}, system.DetectionResult{}); err != nil {
		t.Fatal(err)
	}
	got, err := state.Read(home)
	if err != nil || !got.SelectionConfigured || got.Preset != model.PresetFull || !slices.Equal(got.Components, model.SetupComponents()) || !slices.Equal(got.Skills, skills.SetupSkills()) || len(got.InstalledAgents) != 1 || got.InstalledAgents[0] != "claude-code" {
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

func TestRunInstallThenSyncConverges(t *testing.T) {
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

			installed, err := RunInstall([]string{"--agent", "claude-code"}, system.DetectionResult{})
			if err != nil || !installed.Verify.Ready {
				t.Fatalf("RunInstall() error = %v, ready = %v", err, installed.Verify.Ready)
			}
			got, err := state.Read(home)
			if err != nil {
				t.Fatal(err)
			}
			wantComponents, wantSkills := model.SetupComponents(), skills.SetupSkills()
			if !got.SelectionConfigured || !slices.Equal(got.Components, wantComponents) || !slices.Equal(got.Skills, wantSkills) || got.Preset != model.PresetFull || !slices.Equal(got.InstalledAgents, []string{"claude-code"}) {
				t.Fatalf("persisted selection = %#v, want the full setup", got)
			}
			if tt.existing != nil && (got.LastUpdateCheck == nil || !got.LastUpdateCheck.Equal(lastCheck)) {
				t.Fatalf("LastUpdateCheck = %v, want %v", got.LastUpdateCheck, lastCheck)
			}
			before := snapshotManagedTree(t, home)
			synced, err := RunSync(nil)
			if err != nil {
				t.Fatalf("RunSync() error = %v", err)
			}
			if !slices.Equal(synced.Selection.Components, wantComponents) || !slices.Equal(synced.Selection.Skills, wantSkills) || synced.Selection.Preset != model.PresetFull {
				t.Fatalf("sync selection = %#v, want the full setup", synced.Selection)
			}
			assertSameManagedTree(t, "sync after install", before, snapshotManagedTree(t, home))
			if synced.FilesChanged != 0 {
				t.Errorf("sync changed %d files: %v", synced.FilesChanged, synced.ChangedFiles)
			}
		})
	}
}

func TestRunInstallIncrementalReplacesLegacySelection(t *testing.T) {
	t.Chdir(t.TempDir())
	home := convergenceTestHome(t)
	if err := state.Write(home, state.InstallState{
		InstalledAgents: []string{"codex"}, SelectionConfigured: true,
		Components: []model.ComponentID{model.ComponentSkills, model.ComponentEngram},
		Skills:     []model.SkillID{model.SkillCommentWriter}, Preset: "custom"}); err != nil {
		t.Fatal(err)
	}
	if _, err := RunInstall([]string{"--agent", "claude-code"}, system.DetectionResult{}); err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}
	got, err := state.Read(home)
	if err != nil {
		t.Fatal(err)
	}
	if !got.SelectionConfigured || !slices.Equal(got.Components, model.SetupComponents()) || !slices.Equal(got.Skills, skills.SetupSkills()) || got.Preset != model.PresetFull || !slices.Equal(got.InstalledAgents, []string{"codex", "claude-code"}) {
		t.Fatalf("incremental selection = %#v, want the full setup with existing agents preserved", got)
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

	merged, err := mergeExplicitAgentInstallState(home, fresh, []string{"codex"})
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

	_, err := mergeExplicitAgentInstallState(home, state.InstallState{InstalledAgents: []string{"codex"}}, []string{"codex"})
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

	_, err := RunInstall([]string{"--agent", "claude-code"}, system.DetectionResult{})
	if err == nil {
		t.Fatal("RunInstall() error = nil, want an error naming the unreadable install state instead of a silent success")
	}
}
