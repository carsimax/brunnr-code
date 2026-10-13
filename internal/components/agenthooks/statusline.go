package agenthooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/assets"
	"github.com/carsimax/brunnr-code/internal/components/filemerge"
	"github.com/carsimax/brunnr-code/internal/managedownership"
	"github.com/carsimax/brunnr-code/internal/model"
)

const (
	statusLineAsset        = "claude/statusline-command.sh"
	statusLineScriptName   = "statusline-command.sh"
	statusLineVersionToken = "__BRUNNR_CODE_VERSION__"
	// statusLineTrailer ends every rendered script with the SHA-256 of the
	// bytes before it. A file whose trailer still matches is unedited managed
	// output; any user edit breaks the match, so the file is left alone. This
	// proves ownership without a separate ledger file.
	statusLineTrailer = "# brunnr-code-managed sha256:"
)

// ClaudeStatusLineScriptPath returns where the managed status line script
// lives: next to the Claude settings file.
func ClaudeStatusLineScriptPath(settingsPath string) string {
	return filepath.Join(filepath.Dir(settingsPath), statusLineScriptName)
}

// OwnsClaudeStatusLineScript reports whether data is an unedited script that
// Brunnr-Code rendered.
func OwnsClaudeStatusLineScript(data []byte) bool {
	body := bytes.TrimSuffix(data, []byte("\n"))
	i := bytes.LastIndexByte(body, '\n')
	if i < 0 {
		return false
	}
	trailer := string(body[i+1:])
	return strings.HasPrefix(trailer, statusLineTrailer) &&
		trailer[len(statusLineTrailer):] == managedownership.Hash(body[:i+1])
}

// ClaudeStatusLineReferences reports whether a decoded statusLine value runs
// the managed script.
func ClaudeStatusLineReferences(statusLine any, scriptPath string) bool {
	m, ok := statusLine.(map[string]any)
	command, _ := m["command"].(string)
	return ok && (command == claudeStatusLineCommand(scriptPath) || strings.Contains(command, scriptPath))
}

func claudeStatusLineCommand(scriptPath string) string {
	return "zsh '" + strings.ReplaceAll(scriptPath, "'", `'\''`) + "'"
}

func renderClaudeStatusLine(version string) []byte {
	body := strings.ReplaceAll(assets.MustRead(statusLineAsset), statusLineVersionToken, version)
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return []byte(body + statusLineTrailer + managedownership.Hash([]byte(body)) + "\n")
}

// readRegularFile returns the file bytes, or nil when the file is absent. It
// refuses symlinks and other non-regular entries.
func readRegularFile(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%q is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	return data, true, err
}

// InstallClaudeStatusLine writes the managed status line script showing
// version and points Claude's statusLine at it. It never takes over a
// statusLine or script the user owns: either one present and foreign makes
// the whole install a no-op.
func InstallClaudeStatusLine(homeDir string, adapter agents.Adapter, version string) (Result, error) {
	if adapter.Agent() != model.AgentClaudeCode {
		return Result{}, nil
	}
	settingsPath := adapter.SettingsPath(homeDir)
	if settingsPath == "" {
		return Result{}, nil
	}
	scriptPath := ClaudeStatusLineScriptPath(settingsPath)

	data, _, err := readRegularFile(settingsPath)
	if err != nil {
		return Result{}, fmt.Errorf("read Claude settings: %w", err)
	}
	root, err := decodeHookSettings(data)
	if err != nil {
		return Result{}, fmt.Errorf("parse Claude settings %q: %w", settingsPath, err)
	}
	existing, hasStatusLine := root["statusLine"]
	if hasStatusLine && !ClaudeStatusLineReferences(existing, scriptPath) {
		return Result{}, nil
	}
	current, scriptExists, err := readRegularFile(scriptPath)
	if err != nil {
		return Result{}, fmt.Errorf("read Claude status line script: %w", err)
	}
	if scriptExists && !OwnsClaudeStatusLineScript(current) {
		return Result{}, nil
	}

	var result Result
	if rendered := renderClaudeStatusLine(version); !bytes.Equal(current, rendered) {
		wr, err := filemerge.WriteFileAtomic(scriptPath, rendered, 0o755)
		if err != nil {
			return Result{}, err
		}
		if wr.Changed {
			result.Changed = true
			result.Files = append(result.Files, scriptPath)
		}
	}
	if hasStatusLine {
		return result, nil
	}
	statusLine := map[string]any{"type": "command", "command": claudeStatusLineCommand(scriptPath)}
	var out []byte
	if len(bytes.TrimSpace(data)) == 0 || json.Valid(data) {
		root["statusLine"] = statusLine
		if out, err = json.MarshalIndent(root, "", "  "); err == nil {
			out = append(out, '\n')
		}
	} else {
		// JSONC keeps every byte the user wrote; the key is absent, so the
		// merge only adds it.
		var overlay []byte
		if overlay, err = json.Marshal(map[string]any{"statusLine": statusLine}); err == nil {
			out, err = filemerge.MergeJSONCObjects(data, overlay)
		}
	}
	if err != nil {
		return Result{}, fmt.Errorf("rewrite Claude settings %q: %w", settingsPath, err)
	}
	wr, err := filemerge.WriteFileAtomic(settingsPath, out, 0o644)
	if err != nil {
		return Result{}, err
	}
	if wr.Changed {
		result.Changed = true
		result.Files = append(result.Files, settingsPath)
	}
	return result, nil
}
