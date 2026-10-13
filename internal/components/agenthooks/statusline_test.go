package agenthooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/model"
)

func claudeStatusLineFixture(t *testing.T) (agents.Adapter, string, string, string) {
	t.Helper()
	adapter, err := agents.NewAdapter(model.AgentClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	SetStatusLineLookPathForTest(t)
	home := t.TempDir()
	settings := adapter.SettingsPath(home)
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	return adapter, home, settings, ClaudeStatusLineScriptPath(settings)
}

func readStatusLineCommand(t *testing.T, settings string) string {
	t.Helper()
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		StatusLine struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		} `json:"statusLine"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("decode settings: %v\n%s", err, data)
	}
	if root.StatusLine.Type != "command" {
		t.Fatalf("statusLine type = %q, want command:\n%s", root.StatusLine.Type, data)
	}
	return root.StatusLine.Command
}

func TestInstallClaudeStatusLine(t *testing.T) {
	for _, tc := range []struct {
		name       string
		settings   string // seeded settings.json; empty means absent
		script     string // seeded script; empty means absent
		wantOwned  bool   // installer writes the script and owns statusLine
		wantCustom string // a user statusLine field that must survive
		missing    string // a render tool absent from PATH
	}{
		{name: "fresh install", wantOwned: true},
		{name: "settings without statusLine", settings: `{"model":"opus"}`, wantOwned: true},
		{name: "statusLine already on managed script", settings: `{"statusLine":{"type":"command","command":"zsh SCRIPT","padding":2}}`, wantOwned: true, wantCustom: `"padding":2`},
		{name: "foreign statusLine", settings: `{"statusLine":{"type":"command","command":"bash ~/mine.sh"}}`},
		{name: "user script at managed path", script: "#!/bin/zsh\necho mine\n"},
		{name: "jq missing", settings: `{"model":"opus"}`, missing: "jq"},
		{name: "zsh missing", missing: "zsh"},
		{name: "jq missing keeps managed statusLine fresh", settings: `{"statusLine":{"type":"command","command":"zsh SCRIPT"}}`, missing: "jq", wantOwned: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter, home, settings, script := claudeStatusLineFixture(t)
			if tc.settings != "" {
				seed := strings.ReplaceAll(tc.settings, "SCRIPT", script)
				if err := os.WriteFile(settings, []byte(seed), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.script != "" {
				if err := os.WriteFile(script, []byte(tc.script), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if tc.missing != "" {
				SetStatusLineLookPathForTest(t, tc.missing)
			}
			beforeSettings, _ := os.ReadFile(settings)

			result, err := InstallClaudeStatusLine(home, adapter, "v1.2.3")
			if err != nil {
				t.Fatalf("install: %v", err)
			}
			if wantMissing := tc.missing != "" && !tc.wantOwned; (len(result.Missing) == 1 && result.Missing[0] == tc.missing) != wantMissing {
				t.Fatalf("Missing = %v, want [%s] only when the status line is skipped", result.Missing, tc.missing)
			}
			if result.Changed != tc.wantOwned {
				t.Fatalf("Changed = %v, want %v (%+v)", result.Changed, tc.wantOwned, result)
			}
			scriptData, scriptErr := os.ReadFile(script)
			if !tc.wantOwned {
				if after, _ := os.ReadFile(settings); string(after) != string(beforeSettings) {
					t.Fatalf("foreign settings changed:\n%s", after)
				}
				if tc.script == "" && !os.IsNotExist(scriptErr) {
					t.Fatalf("script written without owning statusLine: %v", scriptErr)
				}
				if tc.script != "" && string(scriptData) != tc.script {
					t.Fatalf("user script overwritten:\n%s", scriptData)
				}
				return
			}
			if scriptErr != nil {
				t.Fatal(scriptErr)
			}
			body := string(scriptData)
			if !strings.Contains(body, "ᛒv1.2.3") || strings.Contains(body, statusLineVersionToken) {
				t.Fatalf("version not substituted in script")
			}
			if !OwnsClaudeStatusLineScript(scriptData) {
				t.Fatal("installed script does not carry a valid ownership trailer")
			}
			if command := readStatusLineCommand(t, settings); !strings.Contains(command, script) {
				t.Fatalf("statusLine command %q does not reference %q", command, script)
			}
			settingsData, _ := os.ReadFile(settings)
			if tc.wantCustom != "" && !strings.Contains(string(settingsData), tc.wantCustom) {
				t.Fatalf("user statusLine field %s lost:\n%s", tc.wantCustom, settingsData)
			}

			again, err := InstallClaudeStatusLine(home, adapter, "v1.2.3")
			if err != nil || again.Changed {
				t.Fatalf("re-run not idempotent: %+v %v", again, err)
			}
			afterScript, _ := os.ReadFile(script)
			afterSettings, _ := os.ReadFile(settings)
			if string(afterScript) != body || string(afterSettings) != string(settingsData) {
				t.Fatal("re-run changed bytes")
			}
		})
	}
}

func TestInstallClaudeStatusLineUpgradeKeepsUserEdits(t *testing.T) {
	adapter, home, _, script := claudeStatusLineFixture(t)
	if _, err := InstallClaudeStatusLine(home, adapter, "v1"); err != nil {
		t.Fatal(err)
	}
	upgraded, err := InstallClaudeStatusLine(home, adapter, "v2")
	if err != nil || !upgraded.Changed {
		t.Fatalf("upgrade: %+v %v", upgraded, err)
	}
	data, _ := os.ReadFile(script)
	if !strings.Contains(string(data), "ᛒv2") {
		t.Fatal("managed script not upgraded")
	}

	edited := strings.Replace(string(data), "BARW=5", "BARW=8", 1)
	if err := os.WriteFile(script, []byte(edited), 0o755); err != nil {
		t.Fatal(err)
	}
	if OwnsClaudeStatusLineScript([]byte(edited)) {
		t.Fatal("edited script still reported as managed")
	}
	if result, err := InstallClaudeStatusLine(home, adapter, "v3"); err != nil || result.Changed {
		t.Fatalf("edited script touched: %+v %v", result, err)
	}
	if after, _ := os.ReadFile(script); string(after) != edited {
		t.Fatal("user edit overwritten")
	}
}

func TestInstallClaudeStatusLineSkipsCodex(t *testing.T) {
	adapter, err := agents.NewAdapter(model.AgentCodex)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	result, err := InstallClaudeStatusLine(home, adapter, "v1")
	if err != nil || result.Changed || len(result.Files) != 0 {
		t.Fatalf("Codex install = %+v %v, want no-op", result, err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("Codex install wrote files: %v", entries)
	}
}
