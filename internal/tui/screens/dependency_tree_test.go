package screens

import (
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/planner"
)

func TestRenderDependencyTreeGenericEmptyPlanKeepsExistingCopy(t *testing.T) {
	selection := model.Selection{Preset: model.PresetFullGentleman}

	out := RenderDependencyTree(planner.ResolvedPlan{}, selection, 0)

	if !strings.Contains(out, "No components selected yet.") {
		t.Fatalf("RenderDependencyTree() missing generic empty copy; output:\n%s", out)
	}
}
