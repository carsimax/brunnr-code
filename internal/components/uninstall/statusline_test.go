package uninstall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/components/agenthooks"
	"github.com/carsimax/brunnr-code/internal/model"
)

func TestFullAgentClaudeRemovesManagedStatusLine(t *testing.T) {
	for _, tc := range []struct {
		name        string
		install     bool   // run the real installer first
		settings    string // seeded settings, written after install when set
		editScript  bool   // user edits the installed script
		userScript  string // a user-owned script at the managed path
		wantRemoved bool   // statusLine and script both removed
		wantKeep    string // settings text that must survive
	}{
		{name: "managed install beside user settings", install: true, settings: `{"model":"opus","statusLine":{"type":"command","command":"zsh 'SCRIPT'"}}`, wantRemoved: true, wantKeep: `"model": "opus"`},
		{name: "managed install alone", install: true, wantRemoved: true},
		{name: "edited managed script", install: true, editScript: true},
		{name: "foreign statusLine and script", settings: `{"statusLine":{"type":"command","command":"zsh SCRIPT"}}`, userScript: "#!/bin/zsh\necho mine\n", wantKeep: "statusLine"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			svc, err := NewService(homeDir, t.TempDir(), "dev")
			if err != nil {
				t.Fatal(err)
			}
			adapter, _ := svc.registry.Get(model.AgentClaudeCode)
			settings := adapter.SettingsPath(homeDir)
			script := agenthooks.ClaudeStatusLineScriptPath(settings)
			if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.install {
				agenthooks.SetStatusLineLookPathForTest(t)
				if _, err := agenthooks.InstallClaudeStatusLine(homeDir, adapter, "v1"); err != nil {
					t.Fatal(err)
				}
			}
			if tc.settings != "" {
				if err := os.WriteFile(settings, []byte(strings.ReplaceAll(tc.settings, "SCRIPT", script)), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.userScript != "" {
				if err := os.WriteFile(script, []byte(tc.userScript), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if tc.editScript {
				data, _ := os.ReadFile(script)
				if err := os.WriteFile(script, append(data, "# mine\n"...), 0o755); err != nil {
					t.Fatal(err)
				}
			}

			built, err := svc.buildPlan([]model.AgentID{adapter.Agent()}, fullAgentRemovalComponents)
			if err != nil {
				t.Fatal(err)
			}
			for _, op := range built.operations {
				if op.path != settings && op.path != script {
					continue
				}
				if _, _, err := op.apply(op.path); err != nil {
					t.Fatalf("apply %s: %v", op.path, err)
				}
			}

			raw, settingsErr := os.ReadFile(settings)
			_, scriptErr := os.Stat(script)
			if tc.wantRemoved {
				if strings.Contains(string(raw), "statusLine") || !os.IsNotExist(scriptErr) {
					t.Fatalf("managed status line kept: script err %v, settings:\n%s", scriptErr, raw)
				}
			} else if scriptErr != nil || !strings.Contains(string(raw), "statusLine") {
				t.Fatalf("user status line removed: script err %v, settings:\n%s", scriptErr, raw)
			}
			if tc.wantKeep != "" && (settingsErr != nil || !strings.Contains(string(raw), tc.wantKeep)) {
				t.Fatalf("settings text %q lost: %v\n%s", tc.wantKeep, settingsErr, raw)
			}
		})
	}
}
