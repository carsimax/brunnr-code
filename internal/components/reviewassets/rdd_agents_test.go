package reviewassets

import (
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
)

// rddAgentNames are the native agents receipt-driven development owns.
var rddAgentNames = []string{"review-risk", "review-readability", "review-reliability", "review-resilience", "review-refuter", "review-validator"}

func isRDDAgentFile(name string) bool {
	base := strings.TrimSuffix(strings.TrimSuffix(name, ".md"), ".yaml")
	for _, candidate := range rddAgentNames {
		if base == candidate {
			return true
		}
	}
	return false
}

func TestNativeAgentManifestShipsReviewAgentsOnlyToRDDRuntimes(t *testing.T) {
	t.Parallel()

	for agent, names := range NativeAgentManifest {
		for _, name := range names {
			if isRDDAgentFile(name) && !model.SupportsReceiptDrivenDevelopment(agent) {
				t.Errorf("%s installs RDD agent %s but does not support receipt-driven development", agent, name)
			}
		}
	}
	for agent, names := range RetiredNativeAgentManifest {
		if model.SupportsReceiptDrivenDevelopment(agent) {
			t.Errorf("%s supports receipt-driven development but lists retired review agents", agent)
		}
		for _, name := range names {
			if !isRDDAgentFile(name) {
				t.Errorf("%s retires %s, which is not an RDD agent", agent, name)
			}
		}
	}
	for _, want := range []string{"jd-fix-agent.md", "jd-judge-a.md", "jd-judge-b.md"} {
		for _, agent := range []model.AgentID{model.AgentClaudeCode} {
			if !containsName(NativeAgentManifest[agent], want) {
				t.Errorf("%s lost Judgment Day agent %s", agent, want)
			}
		}
	}
	if !containsName(NativeAgentManifest[model.AgentKimi], "gentleman.yaml") {
		t.Error("Kimi lost its main agent")
	}
	for _, want := range []string{"review-risk.md", "review-refuter.md"} {
		if !containsName(NativeAgentManifest[model.AgentClaudeCode], want) {
			t.Errorf("Claude Code lost RDD agent %s", want)
		}
	}
}

func TestNativeAgentPathsCoverRetiredAgents(t *testing.T) {
	t.Parallel()

	for _, agent := range []model.AgentID{model.AgentKimi} {
		names := NativeAgentFileNames(agent)
		for _, name := range RetiredNativeAgentManifest[agent] {
			if !containsName(names, name) {
				t.Errorf("%s backup names miss retired agent %s", agent, name)
			}
		}
		for _, name := range NativeAgentManifest[agent] {
			if !containsName(names, name) {
				t.Errorf("%s backup names miss installed agent %s", agent, name)
			}
		}
	}
	if !NativeAgentsSupported(model.AgentCursor) || NativeAgentsSupported(model.AgentCodex) {
		t.Fatal("native agent support must cover cleanup-only runtimes and nothing else")
	}
}

func containsName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}
