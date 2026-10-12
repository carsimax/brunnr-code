package screens

import (
	"strings"
	"testing"
)

func TestRenderCompleteShowsManualActions(t *testing.T) {
	out := RenderComplete(CompletePayload{ManualActions: []string{"Pi CodeGraph child drifted; preserved: /tmp/worker.md"}})
	if !strings.Contains(out, "Manual actions required") || !strings.Contains(out, "Pi CodeGraph child drifted") {
		t.Fatalf("manual action missing from completion output: %q", out)
	}
}

func TestRenderCompleteDistinguishesPartialRollbackAndKeepsManualActions(t *testing.T) {
	out := RenderComplete(CompletePayload{FailedSteps: []FailedStep{{ID: "install", Error: "failed"}}, RollbackPerformed: true, RollbackComplete: false, ManualActions: []string{"Pi drift requires manual repair"}})
	if !strings.Contains(out, "partially completed") || !strings.Contains(out, "Pi drift requires manual repair") {
		t.Fatalf("output = %q, want partial rollback and manual action", out)
	}
}
