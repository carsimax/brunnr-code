package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/reviewtransaction"
	"github.com/carsimax/brunnr-code/internal/system"
	"github.com/carsimax/brunnr-code/internal/tui/screens"
	tea "github.com/charmbracelet/bubbletea"
)

type flowAction struct {
	key       tea.KeyMsg
	cursor    int
	setCursor bool
	prepare   func(Model) Model
	runCmd    bool
}

// TestAgentsContinueOpensInstallPlan pins the single-setup flow: Continue on
// Agents goes straight to the read-only Install Plan for the full setup, and
// Back (Esc or the Back row) returns to Agents.
func TestAgentsContinueOpensInstallPlan(t *testing.T) {
	for _, back := range []struct {
		name   string
		action flowAction
	}{
		{"esc", flowAction{key: tea.KeyMsg{Type: tea.KeyEsc}}},
		{"back row", flowAction{key: tea.KeyMsg{Type: tea.KeyEnter}, cursor: 1, setCursor: true}},
	} {
		t.Run(back.name, func(t *testing.T) {
			m := NewModel(system.DetectionResult{}, "dev")
			m.Screen, m.Selection.Agents = ScreenAgents, []model.AgentID{model.AgentCodex}
			m.Cursor = len(screens.AgentOptions())
			state := applyFlowAction(t, m, flowAction{key: tea.KeyMsg{Type: tea.KeyEnter}})
			if state.Screen != ScreenDependencyTree {
				t.Fatalf("agents continue = %v, want DependencyTree", state.Screen)
			}
			if state.Selection.Preset != model.PresetFull {
				t.Fatalf("preset = %q, want %q", state.Selection.Preset, model.PresetFull)
			}
			assertInstallerSnapshot(t, state, fullPlanSnapshot)
			state = applyFlowAction(t, state, back.action)
			if state.Screen != ScreenAgents {
				t.Fatalf("install plan back = %v, want Agents", state.Screen)
			}
		})
	}
}

// TestInstallFlowHasNoSetupPickers walks the whole installer route and fails
// if any screen other than the fixed sequence appears.
func TestInstallFlowHasNoSetupPickers(t *testing.T) {
	m := installReviewModeStatusFixture(NewModel(system.DetectionResult{}, "dev"))
	m.Screen, m.Selection.Agents = ScreenAgents, []model.AgentID{model.AgentCodex}
	m.Cursor = len(screens.AgentOptions())
	want := []Screen{ScreenDependencyTree, ScreenInstallReviewMode, ScreenReview}
	for i, screen := range want {
		m = applyFlowAction(t, m, flowAction{key: tea.KeyMsg{Type: tea.KeyEnter}, cursor: 0, setCursor: i > 0, runCmd: true})
		if m.Screen != screen {
			t.Fatalf("step %d = %v, want %v", i, m.Screen, screen)
		}
	}
	assertViewSections(t, m.View(), "Review and Confirm", "Dev Stack + Permissions", "Install", "Back")
	if strings.Contains(m.View(), "Strict TDD") {
		t.Fatalf("removed choice appeared in review:\n%s", m.View())
	}
	m = applyFlowAction(t, m, flowAction{key: tea.KeyMsg{Type: tea.KeyEsc}})
	if m.Screen != ScreenInstallReviewMode {
		t.Fatalf("review Esc = %v, want RDD choice", m.Screen)
	}
	m = applyFlowAction(t, m, flowAction{key: tea.KeyMsg{Type: tea.KeyEsc}})
	if m.Screen != ScreenDependencyTree {
		t.Fatalf("RDD choice Esc = %v, want DependencyTree", m.Screen)
	}
}

func installReviewModeStatusFixture(state Model) Model {
	state.ReviewModeCwdFn = func() (string, error) { return "/isolated-repo", nil }
	state.ReviewModeStatusFn = func(context.Context, string) (reviewtransaction.RDDModeStatus, error) {
		return reviewtransaction.RDDModeStatus{Schema: reviewtransaction.RDDModeStatusSchema, Global: reviewtransaction.RDDModeUnset}, nil
	}
	return state
}

func applyFlowAction(t *testing.T, state Model, action flowAction) Model {
	t.Helper()
	if action.prepare != nil {
		state = action.prepare(state)
	}
	if action.setCursor {
		state.Cursor = action.cursor
	}
	updated, cmd := state.Update(action.key)
	state = updated.(Model)
	if action.runCmd && cmd != nil {
		updated, _ = state.Update(cmd())
		state = updated.(Model)
	}
	return state
}

// These snapshots inline representative screens; the TUI keeps no golden
// fixture files.
const fullPlanSnapshot = `Install Plan

Components to install
  1. persona included
     Managed agent behavior and conversation tone
  2. engram included
     Persistent cross-session memory
  3. permissions included
     Security-first defaults and guardrails
  4. skills included
     Curated coding skill library

▸ Continue
  Back

j/k: navigate • enter: select • esc: back
`

func assertInstallerSnapshot(t *testing.T, state Model, want string) {
	t.Helper()
	if got := state.View(); got != strings.TrimSuffix(want, "\n") {
		t.Fatalf("screen %v rendering changed:\nwant:\n%s\ngot:\n%s", state.Screen, want, got)
	}
}

func assertViewSections(t *testing.T, view string, sections ...string) {
	t.Helper()
	for _, section := range sections {
		if !strings.Contains(view, section) {
			t.Fatalf("rendering missing %q:\n%s", section, view)
		}
	}
}

func assertInstallerView(t *testing.T, state Model, title string) {
	t.Helper()
	view := state.View()
	if !strings.Contains(view, title) || strings.Contains(view, "STRICT TDD MODE") {
		t.Fatalf("installer view for %v missing %q or retained obsolete choice:\n%s", state.Screen, title, view)
	}
}
