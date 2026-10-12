package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/agents"
	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
	"github.com/gentleman-programming/gentle-ai/v4/internal/system"
)

// sharedReferencePattern matches a `_shared/<file>.md` reference in installed
// orchestrator, guidance, or skill text.
var sharedReferencePattern = regexp.MustCompile(`_shared/([A-Za-z0-9_.-]+\.md)`)

// skillRuntimes are the runtimes that installed skills/_shared references in
// v3.7.0. Pi has no adapter skills directory and is intentionally excluded.
var skillRuntimes = []model.AgentID{
	model.AgentClaudeCode, model.AgentCodex,
}

// v370SharedReferences is the exact non-SDD skills/_shared set v3.7.0
// installed. odd-orchestrator-sections.md is embedded source material only.
var v370SharedReferences = []string{
	"README.md", "engram-convention.md", "persistence-contract.md", "research-lifecycle.md",
	"review-ledger-contract.md", "skill-resolver.md",
}

func installFullGentleman(t *testing.T, agent model.AgentID) string {
	t.Helper()
	home := installTestHome(t)
	if _, err := RunInstall([]string{"--agent", string(agent), "--preset", "full-gentleman"}, system.DetectionResult{}); err != nil {
		t.Fatalf("install %s: %v", agent, err)
	}
	return home
}

// TestInstallLeavesNoDanglingSharedReferences guards the SDD retirement
// collateral (#4471 T4): every `_shared/*.md` file that installed orchestrator,
// guidance, or skill text points to must exist in the runtime's skills
// directory, and no retired SDD-only shared file may be installed.
func TestInstallLeavesNoDanglingSharedReferences(t *testing.T) {
	for _, agent := range skillRuntimes {
		t.Run(string(agent), func(t *testing.T) {
			home := installFullGentleman(t, agent)
			adapter, err := agents.NewAdapter(agent)
			if err != nil {
				t.Fatal(err)
			}
			sharedDir := filepath.Join(adapter.SkillsDir(home), "_shared")

			referenced := map[string][]string{}
			err = filepath.WalkDir(home, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() {
					if entry.Name() == "backups" || path == sharedDir {
						return filepath.SkipDir
					}
					return nil
				}
				raw, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				for _, match := range sharedReferencePattern.FindAllStringSubmatch(string(raw), -1) {
					referenced[match[1]] = append(referenced[match[1]], path)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(referenced) == 0 {
				t.Fatalf("no installed text references a _shared file; the inventory assertion would be vacuous")
			}
			for name, sources := range referenced {
				if _, err := os.Stat(filepath.Join(sharedDir, name)); err != nil {
					t.Errorf("dangling reference _shared/%s (from %v): %v", name, sources, err)
				}
			}

			entries, err := os.ReadDir(sharedDir)
			if err != nil {
				t.Fatalf("read %s: %v", sharedDir, err)
			}
			var installed []string
			for _, entry := range entries {
				name := entry.Name()
				if strings.HasPrefix(name, "sdd-") || name == "openspec-convention.md" || name == "SKILL.md" {
					t.Errorf("retired SDD shared file installed: %s", name)
				}
				installed = append(installed, name)
			}
			sort.Strings(installed)
			if strings.Join(installed, ",") != strings.Join(v370SharedReferences, ",") {
				t.Errorf("installed _shared = %v, want the v3.7.0 non-SDD set %v", installed, v370SharedReferences)
			}
			ledger, err := os.ReadFile(filepath.Join(sharedDir, "review-ledger-contract.md"))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(ledger, []byte("{{GENTLE_AI_RUNTIME_AGENT_ID}}")) || !bytes.Contains(ledger, []byte("--agent "+string(agent))) {
				t.Errorf("review-ledger-contract.md is not bound to runtime %s", agent)
			}
		})
	}
}

func readJSONObject(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	root := map[string]any{}
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return root
}

// TestUninstallRemovesRestoredSharedFiles proves uninstall removes only the
// restored shared files gentle-ai owns and keeps user-authored neighbors.
func TestUninstallRemovesRestoredSharedFiles(t *testing.T) {
	home := installFullGentleman(t, model.AgentClaudeCode)
	adapter, err := agents.NewAdapter(model.AgentClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	sharedDir := filepath.Join(adapter.SkillsDir(home), "_shared")
	userShared := filepath.Join(sharedDir, "my-notes.md")
	if err := os.WriteFile(userShared, []byte("user"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RunUninstallWithSelection(home, "", []model.AgentID{model.AgentClaudeCode}, nil); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	for _, name := range v370SharedReferences {
		if _, err := os.Stat(filepath.Join(sharedDir, name)); !os.IsNotExist(err) {
			t.Errorf("owned _shared/%s survived uninstall: %v", name, err)
		}
	}
	if _, err := os.Stat(userShared); err != nil {
		t.Errorf("user file %s removed: %v", userShared, err)
	}
}

// TestInstallRefreshesSharedReferencesInCompatibilityRoot restores the v3.7.0
// ~/.agents/skills refresh: an existing compatibility root receives the shared
// references bound to the generic runtime slot, and the obsolete marker goes.
func TestInstallRefreshesSharedReferencesInCompatibilityRoot(t *testing.T) {
	home := installTestHome(t)
	sharedDir := filepath.Join(home, ".agents", "skills", "_shared")
	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(sharedDir, "SKILL.md")
	userNote := filepath.Join(sharedDir, "my-notes.md")
	for _, path := range []string{marker, userNote} {
		if err := os.WriteFile(path, []byte("user"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := RunInstall([]string{"--agent", "claude-code", "--preset", "full-gentleman"}, system.DetectionResult{}); err != nil {
		t.Fatal(err)
	}
	for _, name := range v370SharedReferences {
		if _, err := os.Stat(filepath.Join(sharedDir, name)); err != nil {
			t.Errorf("compatibility root missing _shared/%s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(sharedDir, "odd-orchestrator-sections.md")); !os.IsNotExist(err) {
		t.Errorf("internal odd-orchestrator-sections.md installed: %v", err)
	}
	ledger, err := os.ReadFile(filepath.Join(sharedDir, "review-ledger-contract.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(ledger, []byte("--agent <runtime>")) {
		t.Error("compatibility review-ledger-contract.md is not bound to the generic <runtime> slot")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("legacy _shared/SKILL.md marker survived: %v", err)
	}
	if _, err := os.Stat(userNote); err != nil {
		t.Errorf("user note removed: %v", err)
	}
}

// TestUninstallRemovesEmptiedSkillsDirectory proves child directories are
// evaluated before their parent: a skills directory emptied by uninstall is
// removed, while one holding a user file is kept.
func TestUninstallRemovesEmptiedSkillsDirectory(t *testing.T) {
	for _, keepUserFile := range []bool{false, true} {
		t.Run(fmt.Sprintf("user file %v", keepUserFile), func(t *testing.T) {
			home := installFullGentleman(t, model.AgentClaudeCode)
			adapter, err := agents.NewAdapter(model.AgentClaudeCode)
			if err != nil {
				t.Fatal(err)
			}
			skillDir := adapter.SkillsDir(home)
			if keepUserFile {
				if err := os.WriteFile(filepath.Join(skillDir, "user.md"), []byte("user"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := RunUninstallWithSelection(home, "", []model.AgentID{model.AgentClaudeCode}, nil); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			_, statErr := os.Stat(skillDir)
			if keepUserFile && statErr != nil {
				t.Fatalf("skills directory with a user file removed: %v", statErr)
			}
			if !keepUserFile && !os.IsNotExist(statErr) {
				entries, _ := os.ReadDir(skillDir)
				t.Fatalf("emptied skills directory survived uninstall: %v (entries %v)", statErr, entries)
			}
			for _, dir := range []string{filepath.Join(skillDir, "_shared")} {
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Errorf("emptied directory %s survived uninstall: %v", dir, err)
				}
			}
		})
	}
}
