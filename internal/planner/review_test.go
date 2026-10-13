package planner

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/system"
)

func TestBuildReviewPayloadIncludesPlatformDecision(t *testing.T) {
	selection := model.Selection{
		Preset: model.PresetFull,
	}

	resolved := ResolvedPlan{
		Agents:            []model.AgentID{model.AgentClaudeCode},
		OrderedComponents: []model.ComponentID{model.ComponentEngram},
		PlatformDecision: PlatformDecision{
			OS:        "darwin",
			Supported: true,
		},
	}

	payload := BuildReviewPayload(selection, resolved)

	if !reflect.DeepEqual(payload.PlatformDecision, resolved.PlatformDecision) {
		t.Fatalf("platform decision = %#v, want %#v", payload.PlatformDecision, resolved.PlatformDecision)
	}
}

// --- Batch E: Platform decision propagation matrix ---

func TestPlatformDecisionFromProfileMatrix(t *testing.T) {
	tests := []struct {
		name    string
		profile system.PlatformProfile
		want    PlatformDecision
	}{
		{
			name: "darwin profile maps to a supported decision",
			profile: system.PlatformProfile{
				OS: "darwin", Supported: true,
			},
			want: PlatformDecision{
				OS: "darwin", Supported: true,
			},
		}, {
			name:    "unsupported profile keeps the unsupported decision",
			profile: system.PlatformProfile{OS: "darwin", Supported: false},
			want:    PlatformDecision{OS: "darwin", Supported: false},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := PlatformDecisionFromProfile(tc.profile)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("PlatformDecisionFromProfile() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestBuildReviewPayloadPlatformDecisionPropagatesPerProfile(t *testing.T) {
	profiles := []PlatformDecision{
		{OS: "darwin", Supported: true},
		{OS: "darwin", Supported: false},
	}

	for _, decision := range profiles {
		t.Run(fmt.Sprintf("%s/supported=%t", decision.OS, decision.Supported), func(t *testing.T) {
			selection := model.Selection{
				Preset: model.PresetFull,
			}
			resolved := ResolvedPlan{
				Agents:           []model.AgentID{model.AgentClaudeCode},
				PlatformDecision: decision,
			}

			payload := BuildReviewPayload(selection, resolved)
			if !reflect.DeepEqual(payload.PlatformDecision, decision) {
				t.Fatalf("review payload platform decision = %#v, want %#v", payload.PlatformDecision, decision)
			}
		})
	}
}

// ─── Issue #145: ReviewPayload must include Skills ────────────────────────────

// TestBuildReviewPayloadIncludesSkills verifies that BuildReviewPayload populates
// Skills from selection.Skills.
//
// Closes #145.
func TestBuildReviewPayloadIncludesSkills(t *testing.T) {
	selection := model.Selection{
		Preset: model.PresetFull,
		Skills: []model.SkillID{"judgment-day", "branch-pr", "go-testing"},
	}
	resolved := ResolvedPlan{
		Agents:            []model.AgentID{model.AgentClaudeCode},
		OrderedComponents: []model.ComponentID{model.ComponentEngram, model.ComponentSkills},
	}

	payload := BuildReviewPayload(selection, resolved)

	if len(payload.Skills) != 3 {
		t.Fatalf("Skills len = %d, want 3; got %v", len(payload.Skills), payload.Skills)
	}
	if payload.Skills[0] != "judgment-day" {
		t.Errorf("Skills[0] = %q, want %q", payload.Skills[0], "judgment-day")
	}
	if payload.Skills[2] != "go-testing" {
		t.Errorf("Skills[2] = %q, want %q", payload.Skills[2], "go-testing")
	}
}

// TestBuildReviewPayloadSkillsNilWhenNotSelected verifies that Skills is nil/empty
// when no skills are in the selection.
//
// Closes #145.
func TestBuildReviewPayloadSkillsNilWhenNotSelected(t *testing.T) {
	selection := model.Selection{
		Preset: model.PresetFull,
		// Skills not set
	}
	resolved := ResolvedPlan{
		Agents:            []model.AgentID{model.AgentClaudeCode},
		OrderedComponents: []model.ComponentID{model.ComponentEngram},
	}

	payload := BuildReviewPayload(selection, resolved)

	if len(payload.Skills) != 0 {
		t.Errorf("Skills = %v, want nil/empty when no skills selected", payload.Skills)
	}
}

// ─── Issue #149: ReviewPayload must include StrictTDD ─────────────

// TestBuildReviewPayloadIncludesStrictTDD verifies that BuildReviewPayload sets
// StrictTDD from selection.StrictTDD.
//
// Closes #149.
func TestBuildReviewPayloadIncludesStrictTDD(t *testing.T) {
	selection := model.Selection{
		Preset:    model.PresetFull,
		StrictTDD: true,
	}
	resolved := ResolvedPlan{
		Agents:            []model.AgentID{model.AgentClaudeCode},
		OrderedComponents: []model.ComponentID{model.ComponentEngram},
	}

	payload := BuildReviewPayload(selection, resolved)

	if !payload.StrictTDD {
		t.Errorf("StrictTDD = false, want true (selection.StrictTDD=true)")
	}
}

// TestBuildReviewPayloadStrictTDDFalseWhenDisabled verifies that StrictTDD=false
// is correctly propagated.
//
// Closes #149.
func TestBuildReviewPayloadStrictTDDFalseWhenDisabled(t *testing.T) {
	selection := model.Selection{
		Preset:    model.PresetFull,
		StrictTDD: false,
	}
	resolved := ResolvedPlan{
		Agents:            []model.AgentID{model.AgentClaudeCode},
		OrderedComponents: []model.ComponentID{model.ComponentEngram},
	}

	payload := BuildReviewPayload(selection, resolved)

	if payload.StrictTDD {
		t.Errorf("StrictTDD = true, want false (selection.StrictTDD=false)")
	}
}

func TestResolverOutputIsPlatformAgnostic(t *testing.T) {
	// Planner resolver does NOT set PlatformDecision — it is set by CLI after resolve.
	// This test confirms resolver output has zero-value PlatformDecision.
	resolver := NewResolver(MVPGraph())
	selection := model.Selection{
		Agents:     []model.AgentID{model.AgentClaudeCode},
		Components: []model.ComponentID{model.ComponentEngram},
	}

	plan, err := resolver.Resolve(selection)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	zero := PlatformDecision{}
	if plan.PlatformDecision != zero {
		t.Fatalf("resolver should not set PlatformDecision, got %#v", plan.PlatformDecision)
	}
}
