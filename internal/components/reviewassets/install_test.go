package reviewassets_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/components/reviewassets"
	"github.com/carsimax/brunnr-code/internal/model"
	"gopkg.in/yaml.v3"
)

// Captured by actual sdd.Inject in the disposable af4ce122 worktree, TestParityCapture.
// The four Claude review-* lens hashes were re-pinned deliberately when the
// shared reviewerprovider.SeverityRules joined the rendered severity section,
// and again when those rules gained the observable-harm qualification.
var installedHashes = map[model.AgentID]map[string]string{
	model.AgentClaudeCode: {
		"jd-fix-agent.md": "cdeae78ee628b3a2eb53f767a16b438dba4da987908cc771baa81aa8686a7336", "jd-judge-a.md": "585162b0123fb5898ee63a0b784b0952919dbe5db103be6a93f1fef81125654d", "jd-judge-b.md": "e90a5bf1410e6e8c68f5f8e9408954512c785b7fb1a36ff4db170fc63dd99538", "review-readability.md": "18722db66c7a12445f812cf00d8075634b2762ce3f8da186411e212e61663d1e", "review-refuter.md": "8d07f9d3f3b732ae351258dbc25edc70e0405ddd95a882ddac279c5cdc69d365", "review-reliability.md": "5cbb8fca833ee057606d8c79c79f4b0866f647f299c1c03e10e952258cfe4795", "review-resilience.md": "9d023680ebf7cb9231e9c4938465a87ddfa3e8d52136c56e5a571e2e0feb5b2d", "review-risk.md": "ce83c929444ceccc26763f517ed1ea68fffe76a9097b275cbaea773257c09dce",
	},
}

func containsFile(files []string, path string) bool {
	for _, file := range files {
		if file == path {
			return true
		}
	}
	return false
}

// This pins the installed YAML configuration, not Claude's runtime enforcement.
func TestInstalledClaudeReviewAgentsHaveExplicitEmptyTools(t *testing.T) {
	adapter, err := agents.NewAdapter(model.AgentClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		guidance string
	}{
		{name: "without CodeGraph"},
		{name: "with CodeGraph", guidance: "Use CodeGraph before broad filesystem search."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			result, err := reviewassets.InstallNativeAgents(home, adapter, reviewassets.InstallOptions{
				CodeGraphGuidanceMarkdown: tc.guidance,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Changed || len(result.Skipped) != 0 {
				t.Fatalf("unexpected fresh install result: %+v", result)
			}
			for _, name := range []string{
				"review-risk.md", "review-readability.md", "review-reliability.md",
				"review-resilience.md", "review-refuter.md",
			} {
				t.Run(name, func(t *testing.T) {
					data, err := os.ReadFile(filepath.Join(adapter.SubAgentsDir(home), name))
					if err != nil {
						t.Fatal(err)
					}
					body, found := strings.CutPrefix(string(data), "---\n")
					if !found {
						t.Fatal("installed agent has no YAML frontmatter")
					}
					frontmatter, _, found := strings.Cut(body, "\n---\n")
					if !found {
						t.Fatal("installed agent has unterminated YAML frontmatter")
					}
					var fields map[string]any
					if err := yaml.Unmarshal([]byte(frontmatter), &fields); err != nil {
						t.Fatalf("parse installed frontmatter: %v", err)
					}
					value, present := fields["tools"]
					if !present {
						t.Fatal("tools must be explicit; omitting it inherits available tools")
					}
					tools, ok := value.([]any)
					if !ok || len(tools) != 0 {
						t.Fatalf("tools = %#v, want an explicit empty YAML sequence", value)
					}
				})
			}
		})
	}
}

func TestInstalledNativeAgentParity(t *testing.T) {
	count := 0
	for agent, expected := range installedHashes {
		t.Run(string(agent), func(t *testing.T) {
			home := t.TempDir()
			adapter, err := agents.NewAdapter(agent)
			if err != nil {
				t.Fatal(err)
			}
			opts := reviewassets.InstallOptions{CodeGraphGuidanceMarkdown: "Use CodeGraph before broad filesystem search."}
			result, err := reviewassets.InstallNativeAgents(home, adapter, opts)
			if err != nil {
				t.Fatal(err)
			}
			if !result.Changed || len(result.Files) != len(expected)+1 || !containsFile(result.Files, filepath.Join(adapter.SubAgentsDir(home), reviewassets.OwnershipLedgerFilename)) {
				t.Fatalf("unexpected install result: %+v", result)
			}
			entries, err := os.ReadDir(adapter.SubAgentsDir(home))
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != len(expected)+1 {
				t.Fatalf("installed %d entries, want %d agents plus ledger", len(entries), len(expected)+1)
			}
			for name, want := range expected {
				if strings.HasPrefix(name, "sdd-") {
					t.Fatal("SDD asset in retained fixture")
				}
				path := filepath.Join(adapter.SubAgentsDir(home), name)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != want {
					t.Errorf("%s hash: got %s want %s", name, got, want)
				}
			}
			second, err := reviewassets.InstallNativeAgents(home, adapter, opts)
			if err != nil {
				t.Fatal(err)
			}
			if second.Changed || len(second.Files) != 0 {
				t.Fatalf("second install changed files: %+v", second)
			}
		})
		count += len(expected)
	}
	if count != 8 {
		t.Fatalf("fixture has %d paths, want 8", count)
	}
}
