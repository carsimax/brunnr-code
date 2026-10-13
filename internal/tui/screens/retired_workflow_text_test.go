package screens

import (
	"regexp"
	"testing"

	"github.com/carsimax/brunnr-code/internal/model"
)

// TestScreensShowNoRetiredWorkflowText guards that no screen offers the
// retired OpenSpec workflow as a current feature.
func TestScreensShowNoRetiredWorkflowText(t *testing.T) {
	retired := regexp.MustCompile(`(?i)openspec`)
	for name, view := range map[string]string{
		"complete":               RenderComplete(CompletePayload{ConfiguredAgents: 1, InstalledComponents: 1}),
		"uninstall engram scope": RenderUninstallEngramScope(false, model.EngramUninstallScopeGlobal, 0),
		"uninstall confirm":      RenderUninstallConfirm(model.UninstallModeFull, []model.AgentID{model.AgentCodex}, []model.ComponentID{model.ComponentSkills}, model.EngramUninstallScopeGlobal, false, 0, false, 0),
	} {
		if match := retired.FindString(view); match != "" {
			t.Errorf("%s screen shows retired workflow text %q:\n%s", name, match, view)
		}
	}
}
