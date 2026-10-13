package screens

import (
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/planner"
)

// ─── Issue #145: Review screen must show individual skills ───────────────────

// TestRenderReviewShowsSkillNames verifies that when ReviewPayload.Skills is
// populated, RenderReview output contains each individual skill name.
//
// Closes #145.
func TestRenderReviewShowsSkillNames(t *testing.T) {
	payload := planner.ReviewPayload{
		Agents: []model.AgentID{model.AgentClaudeCode},
		Preset: model.PresetFull,
		Components: []planner.ComponentAction{
			{ID: model.ComponentSkills, Action: "selected"},
		},
		Skills: []model.SkillID{"judgment-day", "branch-pr", "go-testing"},
	}

	out := RenderReview(payload, 0, "")

	for _, skillName := range []string{"judgment-day", "branch-pr", "go-testing"} {
		if !strings.Contains(out, skillName) {
			t.Errorf("RenderReview output missing skill %q; output:\n%s", skillName, out)
		}
	}
}

// TestRenderReviewHidesSkillsSectionWhenEmpty verifies that when there are no
// skills selected, the review screen does not crash and shows no skill names.
//
// Closes #145.
func TestRenderReviewHidesSkillsSectionWhenEmpty(t *testing.T) {
	payload := planner.ReviewPayload{
		Agents: []model.AgentID{model.AgentClaudeCode},
		Preset: model.PresetFull,
		// No Skills field.
	}

	out := RenderReview(payload, 0, "")

	// Should not panic and should render something.
	if len(out) == 0 {
		t.Fatal("RenderReview returned empty string")
	}
}

// ─── Legacy Strict TDD selections are not installer review choices ───────────

func TestRenderReviewOmitsLegacyStrictTDDChoice(t *testing.T) {
	for _, strict := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[strict], func(t *testing.T) {
			payload := planner.ReviewPayload{
				Components: []planner.ComponentAction{{ID: model.ComponentEngram, Action: "selected"}},
				StrictTDD:  strict,
			}
			if out := RenderReview(payload, 0, ""); strings.Contains(out, "Strict TDD") {
				t.Fatalf("legacy choice appears in review:\n%s", out)
			}
		})
	}
}

func TestRenderReviewShowsDeferredInstallReviewMode(t *testing.T) {
	out := RenderReview(planner.ReviewPayload{}, 0, "RDD OFF (global setting after successful installation)")
	if !strings.Contains(out, "Receipt-Driven Development") || !strings.Contains(out, "RDD OFF") {
		t.Fatalf("review summary omitted selected RDD mode:\n%s", out)
	}
}

func TestRenderReviewNamesSingleSetup(t *testing.T) {
	payload := planner.ReviewPayload{
		Agents: []model.AgentID{model.AgentClaudeCode},
		Preset: model.PresetFull,
	}

	out := RenderReview(payload, 0, "")

	if !strings.Contains(out, "Dev Stack + Permissions") {
		t.Fatalf("RenderReview missing the single setup name; output:\n%s", out)
	}
}
