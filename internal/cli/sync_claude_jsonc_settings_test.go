package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/backup"
	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/state"
)

func TestRunSyncAddsClaudeHooksToJSONCSettings(t *testing.T) {
	home := installTestHome(t)
	restoreBackupHome := backup.UserHomeDirFn
	backup.UserHomeDirFn = func() (string, error) { return home, nil }
	t.Cleanup(func() { backup.UserHomeDirFn = restoreBackupHome })
	if err := state.Write(home, state.InstallState{
		InstalledAgents:     []string{string(model.AgentClaudeCode)},
		SelectionConfigured: true,
	}); err != nil {
		t.Fatalf("state.Write() error = %v", err)
	}
	const userText = "{\n  // Personal settings.\n  \"model\": \"opus\", /* chosen by hand */\n"
	settings := filepath.Join(home, ".claude", "settings.json")
	mustWriteFile(t, settings, []byte(userText+"}\n"))

	if _, err := RunSync([]string{"--agents", "claude-code"}); err != nil {
		t.Fatalf("RunSync() over JSONC Claude settings error = %v", err)
	}

	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	// The setup includes the permissions overlay, which rewrites settings.json
	// as plain JSON: comments are not kept, but the user's keys are.
	if !strings.Contains(string(data), `"model": "opus"`) {
		t.Fatalf("sync did not preserve the user's JSONC settings key:\n%s", data)
	}
	for _, command := range []string{"brunnr-code review stop-hook --agent claude-code", "brunnr-code skill-registry refresh"} {
		if !strings.Contains(string(data), command) {
			t.Fatalf("sync did not install hook %q:\n%s", command, data)
		}
	}
}
