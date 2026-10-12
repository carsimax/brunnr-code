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
