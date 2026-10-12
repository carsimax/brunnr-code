package agentguidance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/model"
)

func TestStrictTDDCarrierLifecycle(t *testing.T) {
	for _, agent := range []model.AgentID{model.AgentClaudeCode, model.AgentCodex} {
		t.Run(string(agent), func(t *testing.T) {
			home := t.TempDir()
			adapter, err := agents.NewAdapter(agent)
			if err != nil {
				t.Fatal(err)
			}
			path := adapter.SystemPromptFile(home)
			if got := StrictTDDPath(home, agent); got != path {
				t.Fatalf("carrier = %q, want %q", got, path)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("user before\nuser after\n"), 0644); err != nil {
				t.Fatal(err)
			}
			first, err := InjectStrictTDDWithOptions(home, agent, true, RoutingOptions{})
			if err != nil || !first.Changed {
				t.Fatalf("enable: %+v %v", first, err)
			}
			again, err := InjectStrictTDDWithOptions(home, agent, true, RoutingOptions{})
			if err != nil || again.Changed {
				t.Fatalf("repeat: %+v %v", again, err)
			}
			raw, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(raw), "Strict TDD Mode: enabled") {
				t.Fatalf("content: %q %v", raw, err)
			}
			off, err := InjectStrictTDDWithOptions(home, agent, false, RoutingOptions{})
			if err != nil || !off.Changed {
				t.Fatalf("disable: %+v %v", off, err)
			}
			if againOff, err := InjectStrictTDDWithOptions(home, agent, false, RoutingOptions{}); err != nil || againOff.Changed {
				t.Fatalf("repeat disable: %+v %v", againOff, err)
			}
			raw, err = os.ReadFile(path)
			if err != nil || string(raw) != "user before\nuser after\n" {
				t.Fatalf("user content changed: %q %v", raw, err)
			}
		})
	}
}

func TestStrictTDDUnsupportedCarrier(t *testing.T) {
	for _, agent := range []model.AgentID{"unknown"} {
		t.Run(string(agent), func(t *testing.T) {
			home := t.TempDir()
			if path := StrictTDDPath(home, agent); path != "" {
				t.Fatalf("unexpected carrier: %q", path)
			}
			for _, enabled := range []bool{true, false} {
				result, err := InjectStrictTDDWithOptions(home, agent, enabled, RoutingOptions{})
				if err != nil || result.Changed {
					t.Fatalf("enabled=%v: %+v %v", enabled, result, err)
				}
			}
		})
	}
}
