// Package legacyassets enumerates retired managed paths for snapshots and rollback,
// and retires the SDD agents it can prove a release rendered. It never
// installs or renders SDD assets.
package legacyassets

import (
	"path/filepath"
	"strings"

	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
)

// commandNames is the fixed inventory of eleven historical slash commands.
var commandNames = [...]string{
	"sdd-init", "sdd-new", "sdd-continue", "sdd-status", "sdd-explore",
	"sdd-research", "sdd-ff", "sdd-apply", "sdd-verify", "sdd-archive", "sdd-onboard",
}

// SlashCommandPaths inventories historical commands; Claude has both namespaced
// and earlier unprefixed files. Enumeration does not create or delete files.
func SlashCommandPaths(agent model.AgentID, commandsDir string) []string {
	paths := make([]string, 0, 2*len(commandNames))
	for _, name := range commandNames {
		if agent == model.AgentClaudeCode {
			paths = append(paths, filepath.Join(commandsDir, "gentle-"+name+".md"))
		}
		paths = append(paths, filepath.Join(commandsDir, name+".md"))
	}
	return paths
}

// IsLegacyClaudeCommandPath matches exactly the retired unprefixed Claude files.
func IsLegacyClaudeCommandPath(path string) bool {
	path = filepath.Clean(path)
	dir := filepath.Dir(path)
	if filepath.Base(dir) != "commands" || filepath.Base(filepath.Dir(dir)) != ".claude" {
		return false
	}
	name := strings.TrimSuffix(filepath.Base(path), ".md")
	if filepath.Base(path) != name+".md" {
		return false
	}
	for _, command := range commandNames {
		if name == command {
			return true
		}
	}
	return false
}
