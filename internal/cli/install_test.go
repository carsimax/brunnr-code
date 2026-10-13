package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/system"
)

func TestParseInstallFlagsSupportsCSVAndRepeated(t *testing.T) {
	flags, err := ParseInstallFlags([]string{
		"--agent", "claude-code,codex",
		"--agent", "cursor",
		"--dry-run",
	})
	if err != nil {
		t.Fatalf("ParseInstallFlags() error = %v", err)
	}

	if !reflect.DeepEqual(flags.Agents, []string{"claude-code", "codex", "cursor"}) {
		t.Fatalf("agents = %v", flags.Agents)
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
		Skills: []model.SkillID{
			model.SkillJudgmentDay,
			model.SkillGoTesting,
			model.SkillCreator,
			model.SkillImprover,
			model.SkillSkillRegistry,
			model.SkillChainedPR,
			model.SkillCognitiveDoc,
			model.SkillWorkUnitCommits,
		},
	}

	if !reflect.DeepEqual(input.Selection, want) {
		t.Fatalf("selection = %#v, want %#v", input.Selection, want)
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

// TestParseInstallFlagsRejectsRemovedSetupFlags pins that install has one
// setup: the preset, component and skill flags no longer exist.
func TestParseInstallFlagsRejectsRemovedSetupFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--preset", "full"},
		{"--component", "engram"},
		{"--components", "engram"},
		{"--skill", "go-testing"},
		{"--skills", "go-testing"},
	} {
		t.Run(args[0], func(t *testing.T) {
			_, err := ParseInstallFlags(args)
			if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
				t.Fatalf("ParseInstallFlags(%v) error = %v, want unknown flag error", args, err)
			}
		})
	}
}

func TestParseInstallFlagsRejectsEmptyAgentList(t *testing.T) {
	for _, args := range [][]string{{"--agent", ","}, {"--agents="}, {"--agent", " , "}} {
		if _, err := ParseInstallFlags(args); err == nil {
			t.Errorf("ParseInstallFlags(%v) accepted an empty agent list", args)
		}
	}
}
