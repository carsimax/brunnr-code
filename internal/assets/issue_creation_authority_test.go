package assets

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssueCreationAuthorityBoundary(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")
	duplicatePath := filepath.Join(repositoryRoot, "skills", "issue-creation", "SKILL.md")
	if _, err := os.Stat(duplicatePath); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			t.Fatalf("duplicate project issue-creation authority still exists at %s", duplicatePath)
		}
		t.Fatalf("stat duplicate project issue-creation authority: %v", err)
	}

	agents, err := os.ReadFile(filepath.Join(repositoryRoot, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	const canonicalRegistryRow = "| `issue-creation` | When creating a GitHub issue, reporting a bug, or requesting a feature. | [`internal/assets/skills/issue-creation/SKILL.md`](internal/assets/skills/issue-creation/SKILL.md) |"
	if !strings.Contains(string(agents), canonicalRegistryRow) {
		t.Fatalf("AGENTS.md must route the canonical issue-creation identity directly to the embedded authority; missing row %q", canonicalRegistryRow)
	}
	for _, stale := range []string{"gentle-ai-issue-creation", "[`skills/issue-creation/SKILL.md`](skills/issue-creation/SKILL.md)"} {
		if strings.Contains(string(agents), stale) {
			t.Fatalf("AGENTS.md still references stale issue-creation authority %q", stale)
		}
	}

	canonical := MustRead("skills/issue-creation/SKILL.md")
	if !strings.Contains(canonical, "\nname: issue-creation\n") {
		t.Fatal("embedded issue-creation authority must retain canonical frontmatter identity name: issue-creation")
	}
}

func TestDelegatedWorkflowMutationContract(t *testing.T) {
	const delegatedWorkflowReference = "skills/issue-creation/references/delegated-workflow-actions.md"
	reference := MustRead(delegatedWorkflowReference)
	if readReference, err := Read(delegatedWorkflowReference); err != nil || readReference != reference {
		t.Fatalf("Read(%q) differs from MustRead: %v", delegatedWorkflowReference, err)
	}
}
