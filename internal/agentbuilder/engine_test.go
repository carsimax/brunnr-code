package agentbuilder

import (
	"testing"

	"github.com/carsimax/brunnr-code/internal/model"
)

// ─── NewEngine tests ─────────────────────────────────────────────────────────

func TestNewEngine_ClaudeCode_ReturnsClaudeEngine(t *testing.T) {
	engine := NewEngine(model.AgentClaudeCode)
	if engine == nil {
		t.Fatal("expected non-nil engine for claude-code")
	}
	if engine.Agent() != model.AgentClaudeCode {
		t.Errorf("Agent() = %q, want %q", engine.Agent(), model.AgentClaudeCode)
	}
}

func TestNewEngine_Codex_ReturnsCodexEngine(t *testing.T) {
	engine := NewEngine(model.AgentCodex)
	if engine == nil {
		t.Fatal("expected non-nil engine for codex")
	}
	if engine.Agent() != model.AgentCodex {
		t.Errorf("Agent() = %q, want %q", engine.Agent(), model.AgentCodex)
	}
}

func TestNewEngine_Unknown_ReturnsNil(t *testing.T) {
	engine := NewEngine(model.AgentID("unknown-agent"))
	if engine != nil {
		t.Errorf("expected nil engine for unknown agent, got %v", engine)
	}
}

// ─── Engine command construction tests ───────────────────────────────────────
// These test the Agent() method (which identifies the CLI binary used)
// without actually executing anything.

func TestClaudeEngine_AgentID(t *testing.T) {
	e := &ClaudeEngine{}
	if e.Agent() != model.AgentClaudeCode {
		t.Errorf("ClaudeEngine.Agent() = %q, want %q", e.Agent(), model.AgentClaudeCode)
	}
}

func TestCodexEngine_AgentID(t *testing.T) {
	e := &CodexEngine{}
	if e.Agent() != model.AgentCodex {
		t.Errorf("CodexEngine.Agent() = %q, want %q", e.Agent(), model.AgentCodex)
	}
}

// TestAllSupportedEngines verifies that all supported engine IDs produce non-nil engines.
func TestAllSupportedEngines(t *testing.T) {
	supported := []model.AgentID{
		model.AgentClaudeCode,
		model.AgentCodex,
	}
	for _, id := range supported {
		t.Run(string(id), func(t *testing.T) {
			engine := NewEngine(id)
			if engine == nil {
				t.Errorf("NewEngine(%q) returned nil", id)
			}
		})
	}
}
