package screens

import (
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/model"
)

func TestRenderPresetClarifiesCustomManualSelection(t *testing.T) {
	out := RenderPreset(model.PresetCustom, 3)

	if !strings.Contains(out, "Choose each component manually") {
		t.Fatalf("RenderPreset missing custom preset clarification; output:\n%s", out)
	}
	if strings.Contains(out, "Pick individual components yourself") {
		t.Fatalf("RenderPreset still shows old custom preset wording; output:\n%s", out)
	}
}
