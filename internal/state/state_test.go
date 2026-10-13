package state

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/carsimax/brunnr-code/internal/model"
)

// TestWriteReconciledAcceptsDesiredStateVisibleAfterWriteError verifies that
// a persistence error is reconciled against the bytes visible on disk.
func TestWriteReconciledAcceptsDesiredStateVisibleAfterWriteError(t *testing.T) {
	home := t.TempDir()
	desired := InstallState{InstalledAgents: []string{"example-agent"}}
	if err := Write(home, desired); err != nil {
		t.Fatal(err)
	}
	statePath := Path(home)
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".brunnr-code", "persisted-state.json")
	if err := os.Rename(statePath, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, statePath); err != nil {
		t.Skipf("state symlink unavailable: %v", err)
	}

	if err := WriteReconciled(home, desired); err != nil {
		t.Fatalf("WriteReconciled() error = %v", err)
	}
	visible, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(visible) != string(data) {
		t.Fatalf("visible state changed:\n got %s\nwant %s", visible, data)
	}
}

func fullyPopulatedInstallState() InstallState {
	lastUpdateCheck := time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC)
	rddModeRecordedAt := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	return InstallState{
		InstalledAgents:        []string{"claude-code", "example-agent"},
		InstalledBinaryVersion: "1.2.3",
		ManagedAssetDigest:     "sha256:managed-assets",
		SelectionConfigured:    true,
		Components:             []model.ComponentID{model.ComponentEngram, model.ComponentSkills},
		Skills:                 []model.SkillID{model.SkillGoTesting, model.SkillWorkUnitCommits},
		Preset:                 model.PresetCustom,
		StrictTDD:              true,
		ClaudeModelAssignments: map[string]string{"odd-explorer": "sonnet"},
		ClaudePhaseAssignments: map[string]ClaudePhaseAssignmentState{
			"odd-worker": {Model: "opus", Effort: "max"},
		},
		CodexModelAssignments: map[string]string{
			"odd-verify": "high",
		},
		CodexOrchestratorAssignment: &CodexOrchestratorAssignmentState{Model: "gpt-5.6-luna", Effort: "high"},
		CodexServiceTier:            "priority",
		CodexCarrilModelAssignments: map[string]string{
			"odd-verify":   "gpt-5.6-luna",
			"odd-explorer": "gpt-5.4-mini",
		},
		CodexPhaseModelAssignments: map[string]string{
			"jd-judge-a": "gpt-5.6-sol",
		},
		LastUpdateCheck:   &lastUpdateCheck,
		RDDMode:           "off",
		RDDModeRecordedAt: &rddModeRecordedAt,
	}
}

func TestInstallStatePreservesEveryField(t *testing.T) {
	for _, tt := range []struct {
		name string
		run  func(t *testing.T, want InstallState) InstallState
	}{
		{
			name: "JSON round-trip",
			run: func(t *testing.T, want InstallState) InstallState {
				home := t.TempDir()
				if err := Write(home, want); err != nil {
					t.Fatal(err)
				}
				got, err := Read(home)
				if err != nil {
					t.Fatal(err)
				}
				return got
			},
		},
		{
			name: "MergeAgents",
			run: func(_ *testing.T, want InstallState) InstallState {
				return MergeAgents(want, []string{"example-agent", "codex"})
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			want := fullyPopulatedInstallState()
			wantAfter := want
			wantAfter.StrictTDD = false // retired selection is never persisted or propagated
			if tt.name == "MergeAgents" {
				wantAfter.InstalledAgents = []string{"claude-code", "example-agent", "codex"}
			}
			got := tt.run(t, want)
			if !reflect.DeepEqual(got, wantAfter) {
				t.Fatalf("InstallState = %#v, want %#v", got, wantAfter)
			}
		})
	}
}

// TestMergeAgents verifies that MergeAgents appends new agents to existing
// installed_agents with deduplication and preserves all other fields.
func TestMergeAgents(t *testing.T) {
	existingClaude := map[string]string{"odd-explorer": "sonnet", "jd-fix-agent": "haiku"}

	tests := []struct {
		name      string
		existing  InstallState
		newAgents []string
		wantIDs   []string
	}{
		{
			name:      "empty existing state gets new agent",
			existing:  InstallState{},
			newAgents: []string{"third-agent"},
			wantIDs:   []string{"third-agent"},
		},
		{
			name:      "existing single agent plus new agent appended",
			existing:  InstallState{InstalledAgents: []string{"claude-code"}},
			newAgents: []string{"example-agent"},
			wantIDs:   []string{"claude-code", "example-agent"},
		},
		{
			name:      "duplicate agent is deduped",
			existing:  InstallState{InstalledAgents: []string{"example-agent", "another-agent", "codex"}},
			newAgents: []string{"example-agent"},
			wantIDs:   []string{"example-agent", "another-agent", "codex"},
		},
		{
			name:      "existing multiple agents plus new agent appended",
			existing:  InstallState{InstalledAgents: []string{"example-agent", "another-agent", "codex"}},
			newAgents: []string{"third-agent"},
			wantIDs:   []string{"example-agent", "another-agent", "codex", "third-agent"},
		},
		{
			name: "model_assignments preserved across merge",
			existing: InstallState{
				InstalledAgents:        []string{"example-agent"},
				ClaudeModelAssignments: existingClaude,
			},
			newAgents: []string{"third-agent"},
			wantIDs:   []string{"example-agent", "third-agent"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeAgents(tt.existing, tt.newAgents)

			if !reflect.DeepEqual(got.InstalledAgents, tt.wantIDs) {
				t.Errorf("InstalledAgents = %v, want %v", got.InstalledAgents, tt.wantIDs)
			}

			// Verify all preserved fields are unchanged.
			if !reflect.DeepEqual(got.ClaudeModelAssignments, tt.existing.ClaudeModelAssignments) {
				t.Errorf("ClaudeModelAssignments not preserved: got %v, want %v", got.ClaudeModelAssignments, tt.existing.ClaudeModelAssignments)
			}
		})
	}
}

// TestLegacyCodeGraphSelectionKeysAreIgnored proves that state files written
// while CodeGraph was an optional selection still decode, and that the retired
// keys are not written back.
func TestLegacyCodeGraphSelectionKeysAreIgnored(t *testing.T) {
	home := t.TempDir()
	legacy := []byte(`{"installed_agents":["claude-code"],"community_tools":["codegraph"],"community_tools_configured":true}`)
	path := Path(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Read(home)
	if err != nil {
		t.Fatalf("Read() legacy state error = %v", err)
	}
	if !reflect.DeepEqual(got.InstalledAgents, []string{"claude-code"}) {
		t.Fatalf("InstalledAgents = %v, want [claude-code]", got.InstalledAgents)
	}

	if err := Write(home, got); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), "community_tools") {
		t.Fatalf("rewritten state still carries retired keys: %s", written)
	}
}

func TestSelectionRoundTripPreservesPresenceAndExplicitEmpty(t *testing.T) {
	tests := []InstallState{
		{SelectionConfigured: true, Components: []model.ComponentID{model.ComponentSkills}, Skills: []model.SkillID{model.SkillGoTesting}, Preset: model.PresetCustom, StrictTDD: true},
		{SelectionConfigured: true, Components: []model.ComponentID{}, Skills: []model.SkillID{}, Preset: model.PresetCustom},
		{InstalledAgents: []string{"example-agent"}},
	}
	for i, want := range tests {
		home := t.TempDir()
		if err := Write(home, want); err != nil {
			t.Fatal(err)
		}
		got, err := Read(home)
		if err != nil || got.SelectionConfigured != want.SelectionConfigured || !slices.Equal(got.Components, want.Components) || !slices.Equal(got.Skills, want.Skills) || got.Preset != want.Preset || got.StrictTDD {
			t.Errorf("case %d selection = %#v, want %#v", i, got, want)
		}
	}
}

func TestLegacyStrictTDDValuesDecodeButNeverWriteOrRestore(t *testing.T) {
	for _, value := range []string{"true", "false"} {
		t.Run(value, func(t *testing.T) {
			var legacy InstallState
			if err := json.Unmarshal([]byte(`{"selection_configured":true,"strict_tdd":`+value+`,"preset":"minimal"}`), &legacy); err != nil {
				t.Fatal(err)
			}
			selection := model.Selection{StrictTDD: true}
			legacy.RestoreSelection(&selection)
			if selection.StrictTDD {
				t.Fatalf("legacy toggle leaked into selection: %+v", selection)
			}
			home := t.TempDir()
			if err := Write(home, legacy); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(Path(home))
			if err != nil || bytes.Contains(raw, []byte(`"strict_tdd"`)) || !bytes.Contains(raw, []byte(`"preset": "minimal"`)) {
				t.Fatalf("migration lost unrelated state or wrote retired toggle: %s, %v", raw, err)
			}
		})
	}
}

// TestWriteAndRead writes state and reads it back, verifying agents match.
func TestWriteAndRead(t *testing.T) {
	home := t.TempDir()
	agents := []string{"claude-code", "example-agent"}

	if err := Write(home, InstallState{InstalledAgents: agents}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	s, err := Read(home)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if !reflect.DeepEqual(s.InstalledAgents, agents) {
		t.Errorf("InstalledAgents = %v, want %v", s.InstalledAgents, agents)
	}
}

// An empty object would drop installation settings and the persona, so the
// null refusal must point at restoring the intended state instead.
func TestReadNullStateAdvisesRestoreNotReset(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(Path(home)), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(Path(home), []byte("null"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Read(home)
	if err == nil {
		t.Fatal("Read() error = nil, want null state refusal")
	}
	if msg := err.Error(); strings.Contains(msg, "{}") || !strings.Contains(msg, "restore a valid state file from a backup that keeps your intended installation settings") {
		t.Fatalf("Read() error = %q, want restore advice without an empty-object reset", msg)
	}
}

// TestReadKeepsRejectingNonObjectAndTrailingDocuments pins existing strict
// decoding for shapes the persona guard must not relax.
func TestReadKeepsRejectingNonObjectAndTrailingDocuments(t *testing.T) {
	for _, stateJSON := range []string{`[]`, `"persona"`, `1`, `{} {}`, `{"persona":"neutral"} null`, `{"persona":`} {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Dir(Path(home)), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(Path(home), []byte(stateJSON), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		if got, err := Read(home); err == nil {
			t.Fatalf("Read(%q) = %+v, want error", stateJSON, got)
		}
	}
}

// TestWriteCreatesStateDir verifies that Write creates the .brunnr-code directory
// when it does not exist yet.
func TestWriteCreatesStateDir(t *testing.T) {
	home := t.TempDir()

	if err := Write(home, InstallState{InstalledAgents: []string{"example-agent"}}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(home, stateDir)); err != nil {
		t.Errorf("Write() did not create %q: %v", stateDir, err)
	}
}

// TestWriteStateFilePath verifies Path() returns the expected location.
func TestWriteStateFilePath(t *testing.T) {
	home := t.TempDir()
	got := Path(home)
	want := filepath.Join(home, ".brunnr-code", "state.json")
	if got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

// TestReadMissing verifies that reading a non-existent file returns an error (not a panic).
func TestReadMissing(t *testing.T) {
	home := t.TempDir()
	// No Write — state.json does not exist.

	_, err := Read(home)
	if err == nil {
		t.Fatalf("Read() expected error for missing file, got nil")
	}

	if !os.IsNotExist(err) {
		t.Logf("Read() error = %v (non-nil, as expected — OS-level may differ)", err)
	}
}

// TestReadCorrupt verifies that writing garbage produces an error on read.
func TestReadCorrupt(t *testing.T) {
	home := t.TempDir()

	// Create the directory and write garbage JSON.
	if err := os.MkdirAll(filepath.Join(home, stateDir), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(Path(home), []byte("not valid json {{{{"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Read(home)
	if err == nil {
		t.Fatalf("Read() expected error for corrupt JSON, got nil")
	}
}

// TestWriteOverwrite verifies that a second Write call replaces the previous state.
func TestWriteOverwrite(t *testing.T) {
	home := t.TempDir()

	if err := Write(home, InstallState{InstalledAgents: []string{"claude-code"}}); err != nil {
		t.Fatalf("Write() first error = %v", err)
	}

	if err := Write(home, InstallState{InstalledAgents: []string{"example-agent", "another-agent"}}); err != nil {
		t.Fatalf("Write() second error = %v", err)
	}

	s, err := Read(home)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	want := []string{"example-agent", "another-agent"}
	if !reflect.DeepEqual(s.InstalledAgents, want) {
		t.Errorf("InstalledAgents after overwrite = %v, want %v", s.InstalledAgents, want)
	}
}

func TestWriteFailurePreservesExistingState(t *testing.T) {
	home := t.TempDir()
	original := InstallState{InstalledAgents: []string{"example-agent"}, Preset: "minimal"}
	if err := Write(home, original); err != nil {
		t.Fatal(err)
	}

	statePath := Path(home)
	stateTarget := filepath.Join(home, stateDir, "persisted-state.json")
	if err := os.Rename(statePath, stateTarget); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(stateTarget, statePath); err != nil {
		t.Skipf("state symlink unavailable: %v", err)
	}

	err := Write(home, InstallState{InstalledAgents: []string{"claude-code"}})
	if err == nil {
		t.Fatal("Write() error = nil, want atomic replacement refusal")
	}
	got, readErr := Read(home)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("state after failed Write() = %#v, want %#v", got, original)
	}
}

// TestWriteEmptyAgents verifies that an empty agent list round-trips correctly.
func TestWriteEmptyAgents(t *testing.T) {
	home := t.TempDir()

	if err := Write(home, InstallState{InstalledAgents: []string{}}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	s, err := Read(home)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	// An empty slice round-trips as an empty slice (not nil).
	if len(s.InstalledAgents) != 0 {
		t.Errorf("InstalledAgents = %v, want empty", s.InstalledAgents)
	}
}

func TestClaudePhaseAssignmentsRoundTrip(t *testing.T) {
	home := t.TempDir()

	want := InstallState{
		InstalledAgents: []string{"claude-code"},
		ClaudePhaseAssignments: map[string]ClaudePhaseAssignmentState{
			"odd-worker":   {Model: "sonnet", Effort: "max"},
			"jd-fix-agent": {Model: "haiku"},
		},
	}

	if err := Write(home, want); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	got, err := Read(home)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if !reflect.DeepEqual(got.ClaudePhaseAssignments, want.ClaudePhaseAssignments) {
		t.Errorf("ClaudePhaseAssignments = %v, want %v", got.ClaudePhaseAssignments, want.ClaudePhaseAssignments)
	}
}

// TestBackwardCompatNoAssignments verifies that a state.json written before
// model assignment support was added still reads correctly (fields are nil).
func TestBackwardCompatNoAssignments(t *testing.T) {
	home := t.TempDir()

	// Simulate a legacy state file with only installed_agents.
	if err := os.MkdirAll(filepath.Join(home, stateDir), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	legacy := []byte(`{"installed_agents":["claude-code"]}` + "\n")
	if err := os.WriteFile(Path(home), legacy, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	s, err := Read(home)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if !reflect.DeepEqual(s.InstalledAgents, []string{"claude-code"}) {
		t.Errorf("InstalledAgents = %v, want [claude-code]", s.InstalledAgents)
	}
	if s.ClaudeModelAssignments != nil {
		t.Errorf("ClaudeModelAssignments = %v, want nil", s.ClaudeModelAssignments)
	}
}

// TestInstallStateCodexRoundTrip verifies that CodexModelAssignments persists
// with the "codexModelAssignments" JSON key and is omitted when empty.
func TestInstallStateCodexRoundTrip(t *testing.T) {
	home := t.TempDir()

	assignments := map[string]string{
		"odd-worker":   "high",
		"odd-explorer": "low",
		"default":      "medium",
	}

	s := InstallState{
		InstalledAgents:       []string{"codex"},
		CodexModelAssignments: assignments,
	}

	if err := Write(home, s); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	got, err := Read(home)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if !reflect.DeepEqual(got.CodexModelAssignments, assignments) {
		t.Errorf("CodexModelAssignments = %v, want %v", got.CodexModelAssignments, assignments)
	}
}

// TestInstallStateCodexOmitEmpty verifies that CodexModelAssignments is omitted
// from the JSON when empty.
func TestInstallStateCodexOmitEmpty(t *testing.T) {
	home := t.TempDir()

	s := InstallState{InstalledAgents: []string{"codex"}}
	if err := Write(home, s); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	data, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if contains(string(data), "codexModelAssignments") {
		t.Error("JSON must not contain codexModelAssignments when empty")
	}
}

// TestInstallStateCodexMissingKeyReadback verifies that a state file without
// codexModelAssignments reads back as nil (forward-compat / absent key).
func TestInstallStateCodexMissingKeyReadback(t *testing.T) {
	home := t.TempDir()

	if err := os.MkdirAll(filepath.Join(home, stateDir), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	legacy := []byte(`{"installed_agents":["codex"]}` + "\n")
	if err := os.WriteFile(Path(home), legacy, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	s, err := Read(home)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if s.CodexModelAssignments != nil {
		t.Errorf("CodexModelAssignments = %v, want nil (forward-compat)", s.CodexModelAssignments)
	}
}

// ─── WU-1 RED: CodexCarrilModelAssignments round-trip and backward-compat ────

// TestCodexCarrilModelAssignments_RoundTrip verifies the new carril→model key
// serialises with the JSON key "codexCarrilModelAssignments" and reads back.
func TestCodexCarrilModelAssignments_RoundTrip(t *testing.T) {
	home := t.TempDir()

	carrilMap := map[string]string{
		"odd-verify":   "gpt-5.5",
		"odd-worker":   "gpt-5.5",
		"odd-explorer": "gpt-5.4-mini",
	}
	s := InstallState{
		InstalledAgents:             []string{"codex"},
		CodexCarrilModelAssignments: carrilMap,
	}

	if err := Write(home, s); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	got, err := Read(home)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if !reflect.DeepEqual(got.CodexCarrilModelAssignments, carrilMap) {
		t.Errorf("CodexCarrilModelAssignments = %v, want %v", got.CodexCarrilModelAssignments, carrilMap)
	}

	// JSON key must be "codexCarrilModelAssignments"
	data, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !contains(string(data), "codexCarrilModelAssignments") {
		t.Errorf("JSON must contain key codexCarrilModelAssignments; got:\n%s", data)
	}
}

// TestCodexCarrilModelAssignments_BackwardCompat verifies that a state blob
// without the new key still reads cleanly (field is nil or empty).
func TestCodexCarrilModelAssignments_BackwardCompat(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, stateDir), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	// Legacy blob has codexModelAssignments but no codexCarrilModelAssignments.
	legacy := `{"installed_agents":["codex"],"codexModelAssignments":{"odd-worker":"high"}}` + "\n"
	if err := os.WriteFile(Path(home), []byte(legacy), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	s, err := Read(home)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if s.CodexCarrilModelAssignments != nil {
		t.Errorf("CodexCarrilModelAssignments = %v, want nil for legacy state", s.CodexCarrilModelAssignments)
	}
	// Original key must still be there.
	if s.CodexModelAssignments["odd-worker"] != "high" {
		t.Errorf("CodexModelAssignments[odd-worker] = %q, want high", s.CodexModelAssignments["odd-worker"])
	}
}

// TestCodexCarrilModelAssignments_OmitWhenEmpty verifies that the new key is
// absent from the JSON when the map is nil/empty (omitempty).
func TestCodexCarrilModelAssignments_OmitWhenEmpty(t *testing.T) {
	home := t.TempDir()
	s := InstallState{InstalledAgents: []string{"codex"}}
	if err := Write(home, s); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	data, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if contains(string(data), "codexCarrilModelAssignments") {
		t.Error("JSON must not contain codexCarrilModelAssignments when empty")
	}
}

// TestMergeAgents_PreservesCodexCarrilAssignments verifies that MergeAgents
// preserves CodexCarrilModelAssignments from the existing state.
func TestMergeAgents_PreservesCodexCarrilAssignments(t *testing.T) {
	existing := InstallState{
		InstalledAgents: []string{"codex"},
		CodexCarrilModelAssignments: map[string]string{
			"odd-verify":   "gpt-5.5",
			"odd-explorer": "gpt-5.4-mini",
		},
	}
	merged := MergeAgents(existing, []string{"example-agent"})
	if !reflect.DeepEqual(merged.CodexCarrilModelAssignments, existing.CodexCarrilModelAssignments) {
		t.Errorf("MergeAgents did not preserve CodexCarrilModelAssignments: got %v, want %v",
			merged.CodexCarrilModelAssignments, existing.CodexCarrilModelAssignments)
	}
}

// ─── WU-2 RED: CodexPhaseModelAssignments round-trip, omitempty, MergeAgents ──

// TestCodexPhaseModelAssignments_RoundTrip verifies the new field round-trips
// through Write/Read with JSON key "codexPhaseModelAssignments".
func TestCodexPhaseModelAssignments_RoundTrip(t *testing.T) {
	home := t.TempDir()

	assignments := map[string]string{
		"jd-judge-a":   "gpt-5.5",
		"odd-worker":   "gpt-5.4",
		"odd-explorer": "gpt-5.4-mini",
	}
	s := InstallState{
		InstalledAgents:            []string{"codex"},
		CodexPhaseModelAssignments: assignments,
	}

	if err := Write(home, s); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	got, err := Read(home)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if !reflect.DeepEqual(got.CodexPhaseModelAssignments, assignments) {
		t.Errorf("CodexPhaseModelAssignments = %v, want %v", got.CodexPhaseModelAssignments, assignments)
	}

	// JSON key must be "codexPhaseModelAssignments"
	data, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !contains(string(data), "codexPhaseModelAssignments") {
		t.Errorf("JSON must contain key codexPhaseModelAssignments; got:\n%s", data)
	}
}

// TestCodexPhaseModelAssignments_OmitEmpty verifies the field is absent from JSON
// when nil/empty (omitempty).
func TestCodexPhaseModelAssignments_OmitEmpty(t *testing.T) {
	home := t.TempDir()
	s := InstallState{InstalledAgents: []string{"codex"}}
	if err := Write(home, s); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	data, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if contains(string(data), "codexPhaseModelAssignments") {
		t.Error("JSON must not contain codexPhaseModelAssignments when empty")
	}
}

// TestCodexPhaseModelAssignments_LegacyAbsent verifies that old state files
// without the key read back with nil CodexPhaseModelAssignments.
func TestCodexPhaseModelAssignments_LegacyAbsent(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, stateDir), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	legacy := `{"installed_agents":["codex"],"codexModelAssignments":{"odd-worker":"high"}}` + "\n"
	if err := os.WriteFile(Path(home), []byte(legacy), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	s, err := Read(home)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if s.CodexPhaseModelAssignments != nil {
		t.Errorf("CodexPhaseModelAssignments = %v, want nil for legacy state", s.CodexPhaseModelAssignments)
	}
}

// TestMergeAgents_PreservesCodexPhaseModelAssignments verifies that MergeAgents
// carries the field from the existing state into the merged result.
func TestMergeAgents_PreservesCodexPhaseModelAssignments(t *testing.T) {
	existing := InstallState{
		InstalledAgents: []string{"codex"},
		CodexPhaseModelAssignments: map[string]string{
			"jd-judge-a":   "gpt-5.5",
			"odd-explorer": "gpt-5.4-mini",
		},
	}
	merged := MergeAgents(existing, []string{"example-agent"})
	if !reflect.DeepEqual(merged.CodexPhaseModelAssignments, existing.CodexPhaseModelAssignments) {
		t.Errorf("MergeAgents did not preserve CodexPhaseModelAssignments: got %v, want %v",
			merged.CodexPhaseModelAssignments, existing.CodexPhaseModelAssignments)
	}
}

// ─── Slice 2 RED: LastUpdateCheck round-trip and backward-compat ─────────────

// TestLastUpdateCheck_RoundTrip verifies that LastUpdateCheck survives a
// Write/Read cycle with the expected JSON key "last_update_check".
func TestLastUpdateCheck_RoundTrip(t *testing.T) {
	home := t.TempDir()

	ts := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	s := InstallState{
		InstalledAgents: []string{"claude-code"},
		LastUpdateCheck: &ts,
	}

	if err := Write(home, s); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	got, err := Read(home)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if got.LastUpdateCheck == nil || !got.LastUpdateCheck.Equal(ts) {
		t.Errorf("LastUpdateCheck = %v, want %v", got.LastUpdateCheck, ts)
	}

	// JSON must contain the expected key.
	data, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !contains(string(data), "last_update_check") {
		t.Errorf("JSON must contain key last_update_check; got:\n%s", data)
	}
}

// TestLastUpdateCheck_OmitWhenZero verifies that a zero-value LastUpdateCheck
// is omitted from JSON (omitempty), so old binaries that wrote state without
// the field still produce clean JSON.
func TestLastUpdateCheck_OmitWhenZero(t *testing.T) {
	home := t.TempDir()
	s := InstallState{InstalledAgents: []string{"claude-code"}}
	if err := Write(home, s); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	data, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if contains(string(data), "last_update_check") {
		t.Error("JSON must not contain last_update_check when zero")
	}
}

// TestLastUpdateCheck_BackwardCompat verifies that state.json files written
// before the LastUpdateCheck field was added still read without error, with a
// nil LastUpdateCheck (never checked = always-check behavior).
func TestLastUpdateCheck_BackwardCompat(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, stateDir), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	legacy := `{"installed_agents":["claude-code"]}` + "\n"
	if err := os.WriteFile(Path(home), []byte(legacy), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	s, err := Read(home)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if s.LastUpdateCheck != nil {
		t.Errorf("LastUpdateCheck = %v, want nil for legacy state", s.LastUpdateCheck)
	}
}

// TestMergeAgents_PreservesLastUpdateCheck verifies MergeAgents carries
// LastUpdateCheck from the existing state into the merged result.
func TestMergeAgents_PreservesLastUpdateCheck(t *testing.T) {
	ts := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	existing := InstallState{
		InstalledAgents: []string{"claude-code"},
		LastUpdateCheck: &ts,
	}
	merged := MergeAgents(existing, []string{"example-agent"})
	if merged.LastUpdateCheck == nil || !merged.LastUpdateCheck.Equal(ts) {
		t.Errorf("MergeAgents did not preserve LastUpdateCheck: got %v, want %v",
			merged.LastUpdateCheck, ts)
	}
}

// TestRDDMode_RoundTrip verifies the global receipt-driven-development kill
// switch survives a write/read cycle in uncommitted user state.
func TestRDDMode_RoundTrip(t *testing.T) {
	home := t.TempDir()
	recorded := time.Now().UTC().Truncate(time.Second)
	s := InstallState{
		InstalledAgents:   []string{"claude-code"},
		RDDMode:           "off",
		RDDModeRecordedAt: &recorded,
	}
	if err := Write(home, s); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	got, err := Read(home)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got.RDDMode != "off" {
		t.Errorf("RDDMode = %q after round-trip, want %q", got.RDDMode, "off")
	}
	if got.RDDModeRecordedAt == nil || !got.RDDModeRecordedAt.Equal(recorded) {
		t.Errorf("RDDModeRecordedAt = %v, want %v", got.RDDModeRecordedAt, recorded)
	}
	data, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !contains(string(data), "rdd_mode") || !contains(string(data), "rdd_mode_recorded_at") {
		t.Errorf("JSON must contain the rdd mode keys; got:\n%s", data)
	}
}

// TestRDDMode_BackwardCompat verifies that state files written before the kill
// switch existed still read cleanly with an unconfigured mode.
func TestRDDMode_BackwardCompat(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, stateDir), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	legacy := `{"installed_agents":["claude-code"]}` + "\n"
	if err := os.WriteFile(Path(home), []byte(legacy), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	s, err := Read(home)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if s.RDDMode != "" || s.RDDModeRecordedAt != nil {
		t.Errorf("legacy state = %q/%v, want an unconfigured global mode", s.RDDMode, s.RDDModeRecordedAt)
	}
	data, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if contains(string(data), "rdd_mode") {
		t.Error("reading legacy state must not rewrite it with rdd mode keys")
	}
}

// TestMergeAgents_PreservesRDDMode verifies an incremental agent install never
// silently drops the user's kill-switch choice.
func TestMergeAgents_PreservesRDDMode(t *testing.T) {
	recorded := time.Now().UTC()
	existing := InstallState{
		InstalledAgents:   []string{"claude-code"},
		RDDMode:           "off",
		RDDModeRecordedAt: &recorded,
	}
	merged := MergeAgents(existing, []string{"example-agent"})
	if merged.RDDMode != "off" || merged.RDDModeRecordedAt == nil || !merged.RDDModeRecordedAt.Equal(recorded) {
		t.Errorf("MergeAgents dropped the global mode: %q/%v", merged.RDDMode, merged.RDDModeRecordedAt)
	}
}

// ─── LastSyncedAt round-trip and backward-compat ─────────────────────────

func TestLastSyncedAt_RoundTrip(t *testing.T) {
	home := t.TempDir()
	ts := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	s := InstallState{InstalledAgents: []string{"codex"}, LastSyncedAt: &ts}
	if err := Write(home, s); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	got, err := Read(home)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got.LastSyncedAt == nil || !got.LastSyncedAt.Equal(ts) {
		t.Errorf("LastSyncedAt = %v, want %v", got.LastSyncedAt, ts)
	}
	data, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !contains(string(data), "last_synced_at") {
		t.Errorf("JSON must contain key last_synced_at; got:\n%s", data)
	}
}

func TestLastSyncedAt_OmitWhenZero(t *testing.T) {
	home := t.TempDir()
	s := InstallState{InstalledAgents: []string{"codex"}}
	if err := Write(home, s); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	data, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if contains(string(data), "last_synced_at") {
		t.Error("JSON must not contain last_synced_at when zero")
	}
}

func TestLastSyncedAt_BackwardCompat(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, stateDir), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	legacy := `{"installed_agents":["codex"]}` + "\n"
	if err := os.WriteFile(Path(home), []byte(legacy), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	s, err := Read(home)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if s.LastSyncedAt != nil {
		t.Errorf("LastSyncedAt = %v, want nil for legacy state", s.LastSyncedAt)
	}
}

func TestMergeAgents_PreservesLastSyncedAt(t *testing.T) {
	ts := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	existing := InstallState{InstalledAgents: []string{"codex"}, LastSyncedAt: &ts}
	merged := MergeAgents(existing, []string{"example-agent"})
	if merged.LastSyncedAt == nil || !merged.LastSyncedAt.Equal(ts) {
		t.Errorf("MergeAgents did not preserve LastSyncedAt: got %v, want %v",
			merged.LastSyncedAt, ts)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSub(s, substr)
}

func findSub(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestRestoreSelectionDropsUnknownComponents pins that a persisted component
// ID this build no longer ships (for example a removed integration) is dropped
// on restore, so stale state never reaches sync's unsupported-component path.
func TestRestoreSelectionDropsUnknownComponents(t *testing.T) {
	persisted := InstallState{
		SelectionConfigured: true,
		Components:          []model.ComponentID{model.ComponentEngram, "retired-component", model.ComponentSkills},
	}
	var selection model.Selection
	persisted.RestoreSelection(&selection)

	want := []model.ComponentID{model.ComponentEngram, model.ComponentSkills}
	if !reflect.DeepEqual(selection.Components, want) {
		t.Fatalf("RestoreSelection components = %v, want %v", selection.Components, want)
	}
}
