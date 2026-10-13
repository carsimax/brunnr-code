package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/carsimax/brunnr-code/internal/components/communitytool"
	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/system"
)

// convergenceTestHome prepares an isolated home where every runtime binary
// resolves as installed, so install and sync run the same managed steps a real
// user machine would.
func convergenceTestHome(t *testing.T) string {
	t.Helper()
	home := installTestHome(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	binDir := filepath.Join(home, "bin")
	cmdLookPath = func(name string) (string, error) { return filepath.Join(binDir, name), nil }
	return home
}

// snapshotManagedTree reads every file under root except install state, whose
// sync timestamp is expected to move, and backup snapshots.
func snapshotManagedTree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			if rel == filepath.Join(".brunnr-code", "backups") {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == filepath.Join(".brunnr-code", "state.json") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		files[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return files
}

func assertSameManagedTree(t *testing.T, step string, before, after map[string]string) {
	t.Helper()
	for path, content := range after {
		prev, ok := before[path]
		switch {
		case !ok:
			t.Errorf("%s created %s", step, path)
		case prev != content:
			t.Errorf("%s rewrote %s\n--- before ---\n%s\n--- after ---\n%s", step, path, prev, content)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			t.Errorf("%s removed %s", step, path)
		}
	}
}

// TestInstallThenSyncConverges pins that the first sync after a full install
// is a no-op: sync must apply components in install order, so persona does not
// replace a prompt after Engram wrote its protocol and MCP blocks keep their
// install layout.
func TestInstallThenSyncConverges(t *testing.T) {
	for _, agent := range []string{"claude-code", "codex"} {
		t.Run(agent, func(t *testing.T) {
			home := convergenceTestHome(t)
			if _, err := RunInstall([]string{"--agent", agent, "--preset", "full"}, system.DetectionResult{}); err != nil {
				t.Fatalf("RunInstall() error = %v", err)
			}
			installed := snapshotManagedTree(t, home)

			result, err := RunSync(nil)
			if err != nil {
				t.Fatalf("RunSync() error = %v", err)
			}
			assertSameManagedTree(t, "sync after install", installed, snapshotManagedTree(t, home))
			if result.FilesChanged != 0 {
				t.Errorf("sync after install changed %d files: %v", result.FilesChanged, result.ChangedFiles)
			}
		})
	}
}

func TestCodexReinstallKeepsConfigTOML(t *testing.T) {
	home := convergenceTestHome(t)
	args := []string{"--agent", "codex", "--preset", "full"}
	if _, err := RunInstall(args, system.DetectionResult{}); err != nil {
		t.Fatalf("first RunInstall() error = %v", err)
	}
	configPath := filepath.Join(home, ".codex", "config.toml")
	first, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunInstall(args, system.DetectionResult{}); err != nil {
		t.Fatalf("second RunInstall() error = %v", err)
	}
	second, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("second install rewrote config.toml\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

// TestCodeGraphInstallThenSyncConverges pins that CodeGraph guidance, which
// every install now writes, survives install and the following sync even when
// persona replaces the prompt file earlier in the same run and no CodeGraph
// MCP wiring exists.
func TestCodeGraphInstallThenSyncConverges(t *testing.T) {
	for _, agent := range []model.AgentID{model.AgentClaudeCode, model.AgentCodex} {
		t.Run(string(agent), func(t *testing.T) {
			home := convergenceTestHome(t)
			previous := installCodeGraph
			t.Cleanup(func() { installCodeGraph = previous })
			installCodeGraph = func(id model.CommunityToolID, _, homeDir string, _ communitytool.Runner, _ communitytool.Detector) (communitytool.Result, error) {
				_, err := communitytool.InjectCodeGraphGuidance(homeDir)
				return communitytool.Result{Tool: id}, err
			}

			if _, err := RunInstall([]string{"--agent", string(agent), "--preset", "full"}, system.DetectionResult{}); err != nil {
				t.Fatalf("RunInstall() error = %v", err)
			}
			if !communitytool.HasManagedCodeGraphGuidance(home) {
				t.Fatal("CodeGraph guidance missing after install")
			}
			installed := snapshotManagedTree(t, home)

			result, err := RunSync(nil)
			if err != nil {
				t.Fatalf("RunSync() error = %v", err)
			}
			assertSameManagedTree(t, "sync after CodeGraph install", installed, snapshotManagedTree(t, home))
			if result.FilesChanged != 0 {
				t.Errorf("sync after CodeGraph install changed %d files: %v", result.FilesChanged, result.ChangedFiles)
			}
		})
	}
}
