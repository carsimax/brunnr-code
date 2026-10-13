package skills

import (
	"slices"
	"testing"

	"github.com/carsimax/brunnr-code/internal/model"
)

func TestSetupSkillsIsTheFixedSetupSet(t *testing.T) {
	want := []model.SkillID{
		model.SkillJudgmentDay,
		model.SkillGoTesting,
		model.SkillCreator,
		model.SkillImprover,
		model.SkillSkillRegistry,
		model.SkillChainedPR,
		model.SkillCognitiveDoc,
		model.SkillWorkUnitCommits,
	}
	if got := SetupSkills(); !slices.Equal(got, want) {
		t.Fatalf("SetupSkills() = %v, want %v", got, want)
	}
}

// TestSetupSkillsExcludeOnlyContributorSkills pins that every retained skill
// outside the setup is a contributor skill.
func TestSetupSkillsExcludeOnlyContributorSkills(t *testing.T) {
	setup := SetupSkills()
	contributorOnly := map[model.SkillID]bool{
		model.SkillBranchPR:            true,
		model.SkillIssueCreation:       true,
		model.SkillCommentWriter:       true,
		model.SkillRDDDefectWorkflow:   true,
		model.SkillSystemicIssueTriage: true,
	}

	var outside []model.SkillID
	for _, id := range AllSkillIDs() {
		if !slices.Contains(setup, id) {
			outside = append(outside, id)
		}
	}
	if len(outside) != len(contributorOnly) {
		t.Fatalf("inventory has %d skills outside the setup (%v), want exactly the %d contributor skills", len(outside), outside, len(contributorOnly))
	}
	for _, id := range outside {
		if !contributorOnly[id] {
			t.Fatalf("inventory contains unexpected non-setup skill %q; only contributor skills may live outside the setup", id)
		}
	}
	for id := range contributorOnly {
		if slices.Contains(setup, id) {
			t.Fatalf("SetupSkills() includes contributor skill %q", id)
		}
	}
}

func TestSetupSkillsReturnsAFreshSlice(t *testing.T) {
	first := SetupSkills()
	first[0] = model.SkillCommentWriter
	if got := SetupSkills(); got[0] != model.SkillJudgmentDay {
		t.Fatalf("SetupSkills() shares its backing array: %v", got)
	}
}

func TestAllSkillIDsIncludesEveryKnownSkill(t *testing.T) {
	all := AllSkillIDs()

	required := []model.SkillID{
		model.SkillCreator,
		model.SkillSkillRegistry,
		model.SkillCognitiveDoc,
		model.SkillCommentWriter,
		model.SkillJudgmentDay,
		model.SkillImprover,
		model.SkillGoTesting,
		model.SkillSystemicIssueTriage,
	}

	skillSet := make(map[model.SkillID]struct{}, len(all))
	for _, skill := range all {
		skillSet[skill] = struct{}{}
	}

	for _, req := range required {
		if _, ok := skillSet[req]; !ok {
			t.Fatalf("AllSkillIDs() missing %q", req)
		}
	}
}
