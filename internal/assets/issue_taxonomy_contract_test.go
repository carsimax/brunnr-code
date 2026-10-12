package assets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These are authored skill contracts, not runtime authorization tests.
func TestIssueTaxonomyShippedContract(t *testing.T) {
	canonical := MustRead("skills/issue-creation/SKILL.md")
	taxonomyTerms(t, canonical, `version: "1.6"`, "reviewed policy requires a catalog", "missing required catalog", "unknown label", "inventory is not permission", "untrusted data", "other repositories", "Classification does not grant status or priority authority", "runtime version/digest enforcement", "external control center")
	reference := MustRead("skills/issue-creation/references/delegated-workflow-actions.md")
	taxonomyTerms(t, reference, "approved catalog", "Preserve every existing", "multiple type labels", "human", "Before any generic `$LABEL`", "MAINTAIN", "ADMIN")
	for _, path := range []string{"skills/systemic-issue-triage/SKILL.md", "internal/assets/skills/systemic-issue-triage/SKILL.md", "skills/issue-root-resolution/SKILL.md", "internal/assets/skills/branch-pr/SKILL.md"} {
		t.Run(path, func(t *testing.T) {
			taxonomyTerms(t, taxonomyRead(t, path), "CONTRIBUTING.md", "internal/assets/skills/issue-creation/SKILL.md", "inventory is not permission")
		})
	}
	public := taxonomyRead(t, "skills/systemic-issue-triage/SKILL.md")
	if public != MustRead("skills/systemic-issue-triage/SKILL.md") {
		t.Error("public triage and shipped asset differ")
	}
}

// Pins authored decision examples and gates; this is not a label mutation engine.
func TestIssueTaxonomyHumanReclassification(t *testing.T) {
	canonical := MustRead("skills/issue-creation/SKILL.md")
	taxonomyTerms(t, canonical, "Automatic classification", "Human-authorized type correction", "Preserve every existing", "historical migration")
	reference := MustRead("skills/issue-creation/references/delegated-workflow-actions.md")
	taxonomyTerms(t, reference, "exact existing catalog names to remove and add", "fresh concrete target-host capability", "fresh complete pre-state", "unexpected types", "No wildcard or inferred all-type removal", "expected final type set", "every unrelated label", "no server-atomic guarantee", "unknown", "no retry")
	for _, row := range []string{
		"| One wrong type; automatic classification | DEFER; preserve it, do not add a second type |",
		"| One wrong type; no direct human instruction | DEFER; no mutation |",
		"| One wrong type; explicit named authorized replacement | ALLOW one bounded attempt; preserve unrelated labels |",
		"| Multiple types; automatic classification | DEFER; preserve all types |",
		"| Multiple types; complete human-named resolution | ALLOW one bounded attempt to the exact chosen final type set |",
		"| Multiple types; incomplete human choice | STOP; no mutation |",
		"| Unknown label, wrong target, unavailable capability, or unexpected pre-state types | STOP; no mutation |",
		"| Post-attempt readback unknown, unavailable, or mismatched | UNKNOWN; STOP, no retry or further mutation |",
	} {
		taxonomyTerms(t, reference, row)
	}
	for _, kind := range []string{"issue", "pr"} {
		taxonomyTerms(t, reference, "gh "+kind+" edit \"$NUMBER\" --repo \"$TARGET\" --add-label \"$ADD_TYPES\" --remove-label \"$REMOVE_TYPES\"")
	}
	for _, path := range []string{"skills/systemic-issue-triage/SKILL.md", "internal/assets/skills/systemic-issue-triage/SKILL.md", "skills/issue-root-resolution/SKILL.md", "internal/assets/skills/branch-pr/SKILL.md"} {
		t.Run(path, func(t *testing.T) {
			taxonomyTerms(t, taxonomyRead(t, path), "automatic classification", "human-authorized type correction")
		})
	}
}

func taxonomyRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func taxonomyTerms(t *testing.T, text string, terms ...string) {
	t.Helper()
	for _, term := range terms {
		if !strings.Contains(text, term) {
			t.Errorf("missing authored contract %q", term)
		}
	}
}
