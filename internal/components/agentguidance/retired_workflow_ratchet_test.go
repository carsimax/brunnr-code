package agentguidance_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/catalog"
	"github.com/carsimax/brunnr-code/internal/components/agentguidance"
	"github.com/carsimax/brunnr-code/internal/components/communitytool"
	"github.com/carsimax/brunnr-code/internal/components/engram"
	"github.com/carsimax/brunnr-code/internal/components/persona"
	"github.com/carsimax/brunnr-code/internal/components/reviewassets"
)

// retiredWorkflowReference matches SDD and OpenSpec, both retired in v4.0.0.
var retiredWorkflowReference = regexp.MustCompile(`(?i)sdd|openspec`)

// TestRenderedGuidanceHasNoRetiredWorkflowReferences is a ratchet over what
// Brunnr-Code writes into each runtime: guidance and orchestrator blocks, strict
// TDD, personas and output styles, Engram protocol, CodeGraph guidance, and
// native review/Judgment Day agents. Every scan starts from an empty home, so
// any match was rendered by Brunnr-Code itself. There is no allowlist: retirement
// inventories live in Go cleanup code and are never rendered.
func TestRenderedGuidanceHasNoRetiredWorkflowReferences(t *testing.T) {
	engram.SetLookPathForTest(t, "/usr/local/bin/engram", "")
	codeGraph := communitytool.CodeGraphGuidanceMarkdown()
	assertNoRetiredWorkflowReference(t, "CodeGraphGuidanceMarkdown", codeGraph)

	for _, agent := range catalog.AllAgents() {
		t.Run(string(agent.ID), func(t *testing.T) {
			adapter, err := agents.NewAdapter(agent.ID)
			if err != nil {
				t.Fatal(err)
			}
			options := agentguidance.RoutingOptions{
				// Upgraded Codex installs still carry legacy carril keys.
				CodexCarrilModelAssignments: map[string]string{"sdd-cheap": "gpt-cheap", "sdd-mid": "gpt-mid", "sdd-strong": "gpt-strong"},
			}
			home := t.TempDir()
			routing, err := agentguidance.InjectRoutingWithOptions(home, agent.ID, options)
			if err != nil {
				t.Fatalf("InjectRoutingWithOptions: %v", err)
			}
			tdd, err := agentguidance.InjectStrictTDDWithOptions(home, agent.ID, true, options)
			if err != nil {
				t.Fatalf("InjectStrictTDDWithOptions: %v", err)
			}
			files := append(routing.Files, tdd.Files...)
			assertRenderedFilesClean(t, files)
			rendered, err := agentguidance.RenderOrchestratorWithSource(agent.ID, reviewassets.ReviewExecutionContractFor)
			if err != nil {
				t.Fatalf("RenderOrchestratorWithSource: %v", err)
			}
			assertNoRetiredWorkflowReference(t, "orchestrator", rendered)
			// Shared-section markers bind the composer only; they never ship.
			if strings.Contains(rendered, "orchestrator-section:") {
				t.Errorf("orchestrator leaks a shared-section marker")
			}

			{
				home := t.TempDir()
				t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
				result, err := persona.Inject(home, adapter)
				if err != nil {
					t.Fatalf("persona.Inject: %v", err)
				}
				assertRenderedFilesClean(t, result.Files)
			}

			for _, version := range []string{"", "engram 1.18.0"} {
				home := t.TempDir()
				t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
				result, err := engram.InjectWithOptions(home, adapter, engram.InjectOptions{Version: version})
				if err != nil {
					t.Fatalf("engram.InjectWithOptions(version=%q): %v", version, err)
				}
				assertRenderedFilesClean(t, result.Files)
			}

			if reviewassets.NativeAgentsSupported(agent.ID) {
				home := t.TempDir()
				result, err := reviewassets.InstallNativeAgents(home, adapter, reviewassets.InstallOptions{CodeGraphGuidanceMarkdown: codeGraph})
				if err != nil {
					t.Fatalf("InstallNativeAgents: %v", err)
				}
				assertRenderedFilesClean(t, result.Files)
			}
		})
	}

	t.Run("codegraph-guidance-files", func(t *testing.T) {
		home := t.TempDir()
		result, err := communitytool.InjectCodeGraphGuidance(home)
		if err != nil {
			t.Fatal(err)
		}
		assertRenderedFilesClean(t, result.Files)
	})
}

func assertRenderedFilesClean(t *testing.T, paths []string) {
	t.Helper()
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue // Reported paths can include cleanup targets.
		}
		if err != nil {
			t.Fatalf("read rendered %s: %v", path, err)
		}
		assertNoRetiredWorkflowReference(t, path, string(content))
	}
}

func assertNoRetiredWorkflowReference(t *testing.T, source, content string) {
	t.Helper()
	for index, line := range strings.Split(content, "\n") {
		if retiredWorkflowReference.MatchString(line) {
			t.Errorf("%s:%d renders retired workflow reference: %s", source, index+1, strings.TrimSpace(line))
		}
	}
}
