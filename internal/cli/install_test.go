package cli

import (
	"reflect"
	"testing"

	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/system"
)

func TestParseInstallFlagsSupportsCSVAndRepeated(t *testing.T) {
	flags, err := ParseInstallFlags([]string{
		"--agent", "claude-code,codex",
		"--agent", "cursor",
		"--component", "engram,persona",
		"--component", "skills",
		"--skill", "go-testing",
		"--preset", "minimal",
		"--dry-run",
	})
	if err != nil {
		t.Fatalf("ParseInstallFlags() error = %v", err)
	}

	if !reflect.DeepEqual(flags.Agents, []string{"claude-code", "codex", "cursor"}) {
		t.Fatalf("agents = %v", flags.Agents)
	}

	if !reflect.DeepEqual(flags.Components, []string{"engram", "persona", "skills"}) {
		t.Fatalf("components = %v", flags.Components)
	}

	if !flags.DryRun {
		t.Fatalf("DryRun = false, want true")
	}
}

func TestNormalizeInstallFlagsDefaults(t *testing.T) {
	input, err := NormalizeInstallFlags(InstallFlags{}, system.DetectionResult{})
	if err != nil {
		t.Fatalf("NormalizeInstallFlags() error = %v", err)
	}

	want := model.Selection{
		Agents: []model.AgentID{model.AgentClaudeCode, model.AgentCodex},
		Preset: model.PresetFull,
		Components: []model.ComponentID{
			model.ComponentEngram,
			model.ComponentSkills,
			model.ComponentPermission,
			model.ComponentPersona,
		},
	}

	if !reflect.DeepEqual(input.Selection, want) {
		t.Fatalf("selection = %#v, want %#v", input.Selection, want)
	}
}

func TestNormalizeInstallFlagsAcceptsBundledSkills(t *testing.T) {
	input, err := NormalizeInstallFlags(InstallFlags{Skills: []string{
		string(model.SkillSystemicIssueTriage),
		string(model.SkillRDDDefectWorkflow),
	}}, system.DetectionResult{})
	if err != nil {
		t.Fatalf("NormalizeInstallFlags() error = %v", err)
	}

	want := []model.SkillID{model.SkillSystemicIssueTriage, model.SkillRDDDefectWorkflow}
	if !reflect.DeepEqual(input.Selection.Skills, want) {
		t.Fatalf("skills = %v, want %v", input.Selection.Skills, want)
	}
}

func TestNormalizeInstallFlagsCustomAcceptsOptionalInstallables(t *testing.T) {
	input, err := NormalizeInstallFlags(InstallFlags{
		Preset:     string(model.PresetCustom),
		Components: []string{string(model.ComponentPermission)},
	}, system.DetectionResult{})
	if err != nil {
		t.Fatalf("NormalizeInstallFlags() error = %v", err)
	}

	want := []model.ComponentID{model.ComponentPermission}
	if !reflect.DeepEqual(input.Selection.Components, want) {
		t.Fatalf("components = %#v, want %#v", input.Selection.Components, want)
	}
}

func TestRunInstallDryRunSkipsExecution(t *testing.T) {
	result, err := RunInstall([]string{"--dry-run"}, system.DetectionResult{})
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}

	if !result.DryRun {
		t.Fatalf("DryRun = false, want true")
	}

	if len(result.Plan.Apply) == 0 {
		t.Fatalf("apply steps = 0, want > 0")
	}

	if len(result.Execution.Apply.Steps) != 0 || len(result.Execution.Prepare.Steps) != 0 {
		t.Fatalf("execution should be empty in dry-run")
	}
}

// ─── Detection-default consumer regression tests ───────────────────────────

// makeDetectionWithAgents builds a DetectionResult with the specified agents
// marked as Exists=true. All other agents are absent.
func makeDetectionWithAgents(present ...string) system.DetectionResult {
	var configs []system.ConfigState
	// Full canonical agent set — mirrors knownAgentConfigDirs in config_scan.go.
	known := []string{"claude-code", "codex"}
	presentSet := make(map[string]bool, len(present))
	for _, p := range present {
		presentSet[p] = true
	}
	for _, agent := range known {
		configs = append(configs, system.ConfigState{
			Agent:       agent,
			Path:        "/tmp/fake/" + agent,
			Exists:      presentSet[agent],
			IsDirectory: presentSet[agent],
		})
	}
	return system.DetectionResult{Configs: configs}
}

// TestDefaultAgentsFromDetection_CodexIsIncludedWhenPresent is a regression
// guard: when the codex config dir exists, defaultAgentsFromDetection must
// include model.AgentCodex in its result. Previously the switch statement
// omitted codex, so detection-driven selection silently dropped it.
func TestDefaultAgentsFromDetection_CodexIsIncludedWhenPresent(t *testing.T) {
	detection := makeDetectionWithAgents("codex")
	agents := defaultAgentsFromDetection(detection)

	found := false
	for _, id := range agents {
		if id == model.AgentCodex {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("defaultAgentsFromDetection() did not include codex even though config dir is present; got %v", agents)
	}
}

// TestDefaultAgentsFromDetection_AllAgentsMappedCorrectly verifies every
// canonical agent string maps to its model.AgentID constant. This prevents
// silent drops when new agents are added to ScanConfigs without updating the
// consumer switch.
func TestDefaultAgentsFromDetection_AllAgentsMappedCorrectly(t *testing.T) {
	tests := []struct {
		configAgent string
		wantID      model.AgentID
	}{
		{"claude-code", model.AgentClaudeCode},
		{"codex", model.AgentCodex},
	}

	for _, tt := range tests {
		t.Run(tt.configAgent, func(t *testing.T) {
			detection := makeDetectionWithAgents(tt.configAgent)
			agents := defaultAgentsFromDetection(detection)

			found := false
			for _, id := range agents {
				if id == tt.wantID {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("defaultAgentsFromDetection() missing %q → %q mapping; got %v",
					tt.configAgent, tt.wantID, agents)
			}
			// Exactly one agent should be in the result (only one dir exists).
			if len(agents) != 1 {
				t.Errorf("defaultAgentsFromDetection() returned %d agents, want 1; got %v", len(agents), agents)
			}
		})
	}
}
