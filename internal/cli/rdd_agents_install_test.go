package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
	"github.com/gentleman-programming/gentle-ai/v4/internal/system"
)

func rddAgentFileNames(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "review-") {
			names = append(names, entry.Name())
		}
	}
	return names
}

// TestFreshInstallShipsReviewAgentsOnlyToRDDRuntimes installs every runtime
// with a native agents directory and checks that review agents land only where
// receipt-driven development ships, while Judgment Day stays where it was.
func TestFreshInstallShipsReviewAgentsOnlyToRDDRuntimes(t *testing.T) {
	for _, tc := range []struct {
		agent  model.AgentID
		review bool
		jd     bool
	}{
		{model.AgentClaudeCode, true, true},
	} {
		t.Run(string(tc.agent), func(t *testing.T) {
			home := t.TempDir()
			selection := model.Selection{Agents: []model.AgentID{tc.agent}}
			runInstallInjectionSteps(t, newTestInstallRuntime(t, home, selection))

			dir := resolveAdapters([]model.AgentID{tc.agent})[0].SubAgentsDir(home)
			review := rddAgentFileNames(t, dir)
			if got := len(review) > 0; got != tc.review {
				t.Fatalf("%s review agents = %v, want installed=%v", tc.agent, review, tc.review)
			}
			_, err := os.Stat(filepath.Join(dir, "jd-judge-a.md"))
			if got := err == nil; got != tc.jd {
				t.Fatalf("%s jd-judge-a installed = %v, want %v", tc.agent, got, tc.jd)
			}
			if tc.agent == model.AgentKimi {
				if _, err := os.Stat(filepath.Join(dir, "gentleman.yaml")); err != nil {
					t.Fatalf("Kimi lost its main agent: %v", err)
				}
			}
		})
	}
}

// TestFreshInstallShipsOpenCodeReviewAgents keeps the OpenCode RDD agents.
func TestFreshInstallShipsOpenCodeReviewAgents(t *testing.T) {
	home := installTestHome(t)
	if _, err := RunInstall([]string{"--agent", "opencode", "--preset", "full-gentleman"}, system.DetectionResult{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".config", "opencode", "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Agent map[string]json.RawMessage `json:"agent"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"review-risk", "review-readability", "review-reliability", "review-resilience", "review-refuter", "review-validator", "jd-judge-a", "jd-judge-b", "jd-fix-agent"} {
		if _, ok := settings.Agent[name]; !ok {
			t.Errorf("OpenCode install is missing %q", name)
		}
	}
}
