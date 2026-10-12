package agentguidance

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/agents/capabilitymanifest"
	"github.com/carsimax/brunnr-code/internal/catalog"
	"github.com/carsimax/brunnr-code/internal/model"
)

const (
	orchestratorOpenMarker   = "<!-- brunnr-code:" + OrchestratorSectionID + " -->"
	orchestratorCloseMarker  = "<!-- /brunnr-code:" + OrchestratorSectionID + " -->"
	routingOpenMarkerForTest = "<!-- brunnr-code:" + RoutingSectionID + " -->"
)

// orchestratorRuntimes lists every agent whose installed prompt carries the
// orchestrator.
func orchestratorRuntimes(t *testing.T) []model.AgentID {
	t.Helper()

	var selected []model.AgentID
	for _, agent := range catalog.AllAgents() {
		adapter, err := agents.NewAdapter(agent.ID)
		if err != nil {
			t.Fatalf("NewAdapter(%q) error = %v", agent.ID, err)
		}
		if !adapter.SupportsSystemPrompt() {
			continue
		}
		selected = append(selected, agent.ID)
	}
	if len(selected) != supportedAgentCount {
		t.Fatalf("selected %d orchestrator runtimes, want %d", len(selected), supportedAgentCount)
	}
	return selected
}

// headingCount counts markdown heading lines whose title contains name, so a
// prose reference to a section never counts as a second copy of it.
func headingCount(content, name string) int {
	count := 0
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") && strings.Contains(trimmed, name) {
			count++
		}
	}
	return count
}

func advertisesReviewTransport(t *testing.T, agent model.AgentID) bool {
	t.Helper()

	manifest, err := capabilitymanifest.ForAgent(agent)
	if err != nil {
		t.Fatalf("capabilitymanifest.ForAgent(%q) error = %v", agent, err)
	}
	return manifest.Advertises(capabilitymanifest.ContractReviewTransportV1)
}

func TestInjectRoutingInstallsOrchestratorForEveryRuntime(t *testing.T) {
	t.Parallel()

	for _, agent := range orchestratorRuntimes(t) {
		t.Run(string(agent), func(t *testing.T) {
			t.Parallel()

			result, err := InjectRoutingWithOptions(t.TempDir(), agent, RoutingOptions{})
			if err != nil {
				t.Fatalf("InjectRouting(%q) error = %v", agent, err)
			}
			prompt := deliveredGuidance(t, result.Files[0])

			rendered, err := RenderOrchestrator(agent)
			if err != nil {
				t.Fatalf("RenderOrchestrator(%q) error = %v", agent, err)
			}
			if got := strings.Count(prompt, strings.TrimSpace(rendered)); got != 1 {
				t.Fatalf("installed prompt carries the rendered orchestrator %d times, want 1:\n%s", got, prompt)
			}
			if got := strings.Count(prompt, orchestratorOpenMarker); got != 1 {
				t.Fatalf("orchestrator marker count = %d, want 1", got)
			}
			if strings.Index(prompt, orchestratorOpenMarker) > strings.Index(prompt, routingOpenMarkerForTest) {
				t.Fatal("orchestrator block must precede the routing block it points to")
			}
			if strings.Contains(prompt, "{{BRUNNR_CODE_") {
				t.Fatalf("installed prompt carries an unresolved placeholder:\n%s", prompt)
			}

			for _, heading := range []string{
				"Implementation Routing",
				"ODD protocol",
				"Organic Driven Development Is The Default Workflow",
				"Lossless Blocking Prompts",
				"Delegation Rules",
				"Delegated Verification Gate",
				"Native Checking Contract",
				"Language Domain Contract",
				"Cost and Context Balance",
			} {
				if got := headingCount(prompt, heading); got != 1 {
					t.Errorf("heading %q appears %d times, want 1", heading, got)
				}
			}

			// Receipt-driven development ships only to its runtimes: the
			// provider defect handoff, the user-owned switch, and the native
			// review lifecycle, whose placeholder section every other runtime
			// loses.
			wantReview := 0
			if model.SupportsReceiptDrivenDevelopment(agent) {
				wantReview = 1
			}
			for _, heading := range []string{
				"Brunnr-Code Provider Defect Handoff",
				"Receipt-driven development is user-owned",
				"Native Compact Review Orchestration",
			} {
				if got := headingCount(prompt, heading); got != wantReview {
					t.Errorf("heading %q appears %d times, want %d", heading, got, wantReview)
				}
			}
			if wantReview == 1 && !strings.Contains(prompt, "--agent "+string(agent)) && !strings.Contains(prompt, "`"+string(agent)+"`") {
				t.Errorf("review contract is not bound to runtime %q", agent)
			}
			if wantReview == 0 && headingCount(prompt, "Review Execution Contract") != 0 {
				t.Error("runtime without review transport kept the review execution placeholder section")
			}
		})
	}
}

func TestInjectRoutingOrchestratorIsIdempotentForEveryRuntime(t *testing.T) {
	t.Parallel()

	for _, agent := range orchestratorRuntimes(t) {
		t.Run(string(agent), func(t *testing.T) {
			t.Parallel()

			targetDir := t.TempDir()
			first, err := InjectRoutingWithOptions(targetDir, agent, RoutingOptions{})
			if err != nil {
				t.Fatal(err)
			}
			afterFirst := readFile(t, first.Files[0])

			second, err := InjectRoutingWithOptions(targetDir, agent, RoutingOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if second.Changed {
				t.Fatal("second identical injection reported a change")
			}
			if got := readFile(t, first.Files[0]); got != afterFirst {
				t.Fatalf("second injection rewrote the prompt:\n%s", got)
			}
		})
	}
}

// A prompt that has a routing block but no orchestrator block gets the
// orchestrator inserted ahead of routing, leaving every byte the user wrote
// outside managed markers untouched.
func TestInjectRoutingInsertsOrchestratorAheadOfExistingRouting(t *testing.T) {
	t.Parallel()

	for _, agent := range markdownSectionAgents(t) {
		t.Run(string(agent), func(t *testing.T) {
			t.Parallel()

			targetDir := t.TempDir()
			adapter, err := agents.NewAdapter(agent)
			if err != nil {
				t.Fatal(err)
			}
			promptPath := adapter.SystemPromptFile(targetDir)
			legacy := "USER PREAMBLE\n\n" +
				"USER MIDDLE\n\n" +
				routingOpenMarkerForTest + "\nstale routing\n<!-- /brunnr-code:" + RoutingSectionID + " -->\n\n" +
				"USER EPILOGUE\n"
			if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(promptPath, []byte(legacy), 0o600); err != nil {
				t.Fatal(err)
			}

			if _, err := InjectRoutingWithOptions(targetDir, agent, RoutingOptions{}); err != nil {
				t.Fatal(err)
			}
			got := readFile(t, promptPath)

			if count := strings.Count(got, orchestratorOpenMarker); count != 1 {
				t.Fatalf("orchestrator block count = %d, want 1", count)
			}
			preamble := strings.Index(got, "USER PREAMBLE")
			orchestrator := strings.Index(got, orchestratorOpenMarker)
			middle := strings.Index(got, "USER MIDDLE")
			routing := strings.Index(got, routingOpenMarkerForTest)
			epilogue := strings.Index(got, "USER EPILOGUE")
			if preamble != 0 || !(preamble < middle && middle < orchestrator && orchestrator < routing && routing < epilogue) {
				t.Fatalf("injection did not place the orchestrator ahead of routing with user text in place:\n%s", got)
			}
			if info, err := os.Stat(promptPath); err != nil {
				t.Fatal(err)
			} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
				t.Fatalf("prompt mode = %v, want preserved 0600", info.Mode().Perm())
			}
		})
	}
}

// A prompt that already carries a stale current block converges on exactly one
// refreshed block.
func TestInjectRoutingRefreshesStaleOrchestratorBlock(t *testing.T) {
	t.Parallel()

	targetDir := t.TempDir()
	adapter, err := agents.NewAdapter(model.AgentClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	promptPath := adapter.SystemPromptFile(targetDir)
	existing := "USER\n\n" + orchestratorOpenMarker + "\nold current\n" + orchestratorCloseMarker + "\n"
	if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(promptPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := InjectRoutingWithOptions(targetDir, model.AgentClaudeCode, RoutingOptions{}); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, promptPath)
	if strings.Contains(got, "old current") {
		t.Fatalf("stale orchestrator copies survived:\n%s", got)
	}
	if count := strings.Count(got, orchestratorOpenMarker); count != 1 || !strings.HasPrefix(got, "USER\n") {
		t.Fatalf("want one orchestrator block after user text, got %d:\n%s", count, got)
	}
}

// Not parallel: it swaps the package-level contract source and restores it
// before any parallel test resumes.
func TestRenderOrchestratorFailsClosedWithoutReviewContractSource(t *testing.T) {
	reviewContractMu.RLock()
	original := reviewContractSource
	reviewContractMu.RUnlock()
	t.Cleanup(func() { SetReviewContractSource(original) })

	SetReviewContractSource(nil)
	if _, err := RenderOrchestrator(model.AgentClaudeCode); !errors.Is(err, ErrMissingReviewContract) {
		t.Fatalf("RenderOrchestrator(claude) error = %v, want ErrMissingReviewContract", err)
	}
}
