package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/components/agentguidance"
	"github.com/carsimax/brunnr-code/internal/model"
)

func TestStrictTDDLegacyCarrierRetiredByInstallAndSync(t *testing.T) {
	for _, agent := range []model.AgentID{model.AgentClaudeCode, model.AgentCodex} {
		t.Run(string(agent), func(t *testing.T) {
			home := t.TempDir()
			path := agentguidance.StrictTDDPath(home, agent)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("user content\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := agentguidance.InjectStrictTDDWithOptions(home, agent, true, agentguidance.RoutingOptions{}); err != nil {
				t.Fatal(err)
			}
			step := agentRoutingGuidanceStep{agent: agent, homeDir: home, scope: ScopeGlobal}
			if err := step.Run(); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(path)
			if err != nil || strings.Contains(string(raw), "gentle-ai:strict-tdd-mode") || !strings.Contains(string(raw), "user content") || !strings.Contains(string(raw), "relevant runnable deterministic test") {
				t.Fatalf("legacy marker or missing routing/user content: %s %v", raw, err)
			}
			changed := []string{}
			step.changedFiles = &changed
			if err := step.Run(); err != nil || len(changed) != 0 {
				t.Fatalf("sync not idempotent: %v %v", changed, err)
			}
		})
	}
}
