package screens

import (
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/planner"
)

func TestRenderDependencyTreeGenericEmptyPlanKeepsExistingCopy(t *testing.T) {
	out := RenderDependencyTree(planner.ResolvedPlan{}, 0)

	if !strings.Contains(out, "No components selected yet.") {
		t.Fatalf("RenderDependencyTree() missing generic empty copy; output:\n%s", out)
	}
}
