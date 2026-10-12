package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
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
