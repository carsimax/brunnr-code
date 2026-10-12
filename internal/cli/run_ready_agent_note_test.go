package cli

import (
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/planner"
	"github.com/carsimax/brunnr-code/internal/verify"
)

// TestWithPostInstallNotesNamesOnlyTheInstalledRunnableAgents closes fisidj
// finding 4 (organic-dx Phase 3f task 3f.4): the completion line is the
// literal last line of the first-run experience and must name only what was
// installed.
func TestWithPostInstallNotesNamesOnlyTheInstalledRunnableAgents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		agents     []model.AgentID
		wantClaude bool
	}{
		{name: "claude only", agents: []model.AgentID{model.AgentClaudeCode}, wantClaude: true},
		{name: "codex only", agents: []model.AgentID{model.AgentCodex}, wantClaude: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := verify.Report{Ready: true, FinalNote: verify.ReadyMessage}
			resolved := planner.ResolvedPlan{Agents: tt.agents}

			updated := withPostInstallNotes(report, resolved)
			hasClaude := strings.Contains(updated.FinalNote, "`claude`")
			if hasClaude != tt.wantClaude {
				t.Fatalf("FinalNote names claude = %v, want %v: %q", hasClaude, tt.wantClaude, updated.FinalNote)
			}
		})
	}
}

// TestWithPostInstallNotesFallsBackWhenNoRunnableAgentSelected proves an
// install that selected no named runnable agent (e.g. an
// IDE-integrated agent only) gets the generic ready message instead of naming
// an agent that was never installed.
func TestWithPostInstallNotesFallsBackWhenNoRunnableAgentSelected(t *testing.T) {
	t.Parallel()

	report := verify.Report{Ready: true, FinalNote: verify.ReadyMessage}
	resolved := planner.ResolvedPlan{Agents: []model.AgentID{model.AgentID("unknown-runtime")}}

	updated := withPostInstallNotes(report, resolved)
	if strings.Contains(updated.FinalNote, "claude") {
		t.Fatalf("FinalNote names an agent that was never installed: %q", updated.FinalNote)
	}
}

// TestWithPostInstallNotesDoesNotOverrideAnAlreadyCustomizedFinalNote proves
// the override is scoped to exactly the generic production ready message and
// never clobbers a FinalNote a test (or another note-builder) constructed by
// hand with different text.
func TestWithPostInstallNotesDoesNotOverrideAnAlreadyCustomizedFinalNote(t *testing.T) {
	t.Parallel()

	// AgentClaudeCode isolates the ready-agent-run override.
	report := verify.Report{Ready: true, FinalNote: "You're ready."}
	resolved := planner.ResolvedPlan{Agents: []model.AgentID{model.AgentClaudeCode}}

	updated := withPostInstallNotes(report, resolved)
	if updated.FinalNote != "You're ready." {
		t.Fatalf("FinalNote changed unexpectedly: %q", updated.FinalNote)
	}
}
