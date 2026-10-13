package agenthooks

import (
	"fmt"
	"os"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/components/filemerge"
	"github.com/carsimax/brunnr-code/internal/model"
)

// BranchGuardHookCommand is the exact command of the managed PreToolUse hook
// that denies file edits on the default branch. Install deduplicates and
// uninstall removes the hook by this string.
const BranchGuardHookCommand = "brunnr-code hook branch-guard --agent " + string(model.AgentClaudeCode)

// branchGuardMatcher lists the Claude Code tools that edit files. Plain
// names joined by "|" are matched exactly, not as a regex.
const branchGuardMatcher = "Edit|Write|MultiEdit|NotebookEdit"

// InstallRetainedClaudeHooks installs the Claude Code review hooks and the
// default-branch write guard.
func InstallRetainedClaudeHooks(homeDir string, adapter agents.Adapter) (Result, error) {
	if adapter.Agent() != model.AgentClaudeCode {
		return Result{}, nil
	}
	path := adapter.SettingsPath(homeDir)
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
		return Result{}, fmt.Errorf("parse Claude settings %q: %w", path, err)
	}
	raw, exists := root["hooks"]
	hooks, ok := raw.(map[string]any)
	if exists && !ok {
		return Result{}, fmt.Errorf("Claude settings %q has unsupported hooks shape: want object", path)
	}
	if !ok {
		hooks = map[string]any{}
	}
	command := fmt.Sprintf("brunnr-code review stop-hook --agent %s", adapter.Agent())
	entries := []struct {
		key, matcher, command string
		timeout               int
	}{
		{"Stop", "", command, 60},
		{"SessionStart", "startup|resume|clear|compact", command, 30},
		{"PreToolUse", branchGuardMatcher, BranchGuardHookCommand, 10},
	}
	changed := false
	for _, e := range entries {
		raw, exists := hooks[e.key]
		list, ok := raw.([]any)
		if exists && !ok {
			return Result{}, fmt.Errorf("Claude settings %q has unsupported hooks.%s shape: want array", path, e.key)
		}
		if claudeHookListContains(list, e.command) {
			continue
		}
		hook := map[string]any{"type": "command", "command": e.command, "timeout": e.timeout}
		hooks[e.key] = append(list, map[string]any{"matcher": e.matcher, "hooks": []any{hook}})
		changed = true
	}
	if !changed {
		return Result{Files: []string{path}}, nil
	}
	out, err := encodeHookSettings(data, root, hooks)
	if err != nil {
		return Result{}, fmt.Errorf("rewrite Claude settings %q: %w", path, err)
	}
	wr, err := filemerge.WriteFileAtomic(path, out, 0644)
	if err != nil {
		return Result{}, err
	}
	return Result{Changed: wr.Changed, Files: []string{path}}, nil
}

func claudeHookListContains(entries []any, command string) bool {
	for _, entry := range entries {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		hooks, ok := item["hooks"].([]any)
		if !ok {
			continue
		}
		for _, hook := range hooks {
			h, ok := hook.(map[string]any)
			if ok && h["command"] == command {
				return true
			}
		}
	}
	return false
}
