// Package agenthooks owns runtime hook installation independent of optional components.
package agenthooks

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/components/filemerge"
	"github.com/carsimax/brunnr-code/internal/model"
)

type Result struct {
	Changed bool
	Files   []string
	// Missing names the tools a skipped install needs on PATH.
	Missing []string
}

const claudeSkillRegistryCommand = `brunnr-code skill-registry refresh --quiet --no-gitignore --cwd "${CLAUDE_PROJECT_DIR:-$PWD}" || true`

// InstallSkillRegistry installs the skill-registry startup refresh hooks.
func InstallSkillRegistry(homeDir string, adapter agents.Adapter) (Result, error) {
	var path, event, command string
	switch adapter.Agent() {
	case model.AgentCodex:
		path = filepath.Join(adapter.GlobalConfigDir(homeDir), "hooks.json")
		event = "SessionStart"
		command = `brunnr-code skill-registry refresh --quiet --no-gitignore --cwd "$PWD" || true`
	case model.AgentClaudeCode:
		path = adapter.SettingsPath(homeDir)
		event = "UserPromptSubmit"
		command = claudeSkillRegistryCommand
	default:
		return Result{}, nil
	}
	if path == "" {
		return Result{}, nil
	}
	var data []byte
	info, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return Result{}, err
	}
	if err == nil {
		if !info.Mode().IsRegular() {
			return Result{}, fmt.Errorf("hook settings %q is not a regular file", path)
		}
		if data, err = os.ReadFile(path); err != nil {
			return Result{}, err
		}
	}
	root, err := decodeHookSettings(data)
	if err != nil {
		return Result{}, fmt.Errorf("parse hook settings %q: %w", path, err)
	}
	hooks, ok := root["hooks"].(map[string]any)
	if _, exists := root["hooks"]; exists && !ok {
		return Result{}, fmt.Errorf("hook settings %q has unsupported hooks shape: want object", path)
	}
	if !ok {
		hooks = map[string]any{}
	}
	entries, ok := hooks[event].([]any)
	if _, exists := hooks[event]; exists && !ok {
		return Result{}, fmt.Errorf("hook settings %q has unsupported hooks.%s shape: want array", path, event)
	}
	if claudeHookListContains(entries, command) {
		return Result{Files: []string{path}}, nil
	}
	hook := map[string]any{"type": "command", "command": command}
	entry := map[string]any{"matcher": "", "hooks": []any{hook}}
	if adapter.Agent() == model.AgentCodex {
		entry["matcher"] = "startup|resume|clear|compact"
		hook["timeout"] = 30
		hook["statusMessage"] = "Refreshing skill registry"
	}
	hooks[event] = append(entries, entry)
	out, err := encodeHookSettings(data, root, hooks)
	if err != nil {
		return Result{}, fmt.Errorf("rewrite hook settings %q: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Result{}, err
	}
	wr, err := filemerge.WriteFileAtomic(path, out, 0o644)
	if err != nil {
		return Result{}, err
	}
	return Result{Changed: wr.Changed, Files: []string{path}}, nil
}
