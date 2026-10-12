package legacyassets

//go:generate go run ../../../scripts/gen-sdd-agent-digests sdd_agent_digests.go sdd_asset_digests.go

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/carsimax/brunnr-code/internal/model"
)

// SDDAgentFamilies maps each runtime that received native SDD sub-agents to
// the embedded asset directory (internal/assets/<family>/agents) releases
// rendered them from. Every release installed them in adapter.SubAgentsDir.
var SDDAgentFamilies = map[model.AgentID]string{
	model.AgentClaudeCode: "claude",
}

var (
	managedBlockOpen = regexp.MustCompile(`<!-- gentle-ai:([a-z0-9-]+) -->`)
	engramToolPair   = regexp.MustCompile(`mcp__engram__([A-Za-z0-9_]+), mcp__plugin_engram_engram__([A-Za-z0-9_]+)`)
	engramToolSingle = regexp.MustCompile(`(?:mcp__plugin_engram_engram__|mcp__engram__|\{\{ENGRAM_TOOL_PREFIX\}\})([A-Za-z0-9_]+)`)
)

// NormalizeSDDAgent maps a released template and every render of it to the
// same bytes, so ownership compares only content a user could have authored.
// It erases exactly what the SDD renderer substituted or appended:
//   - CRLF line endings become LF;
//   - complete `<!-- gentle-ai:ID -->` ... `<!-- /gentle-ai:ID -->` blocks
//     (CodeGraph guidance, language contract, remote authorization);
//   - in the frontmatter, the `model:` value, `effort:` lines and the
//     {{CLAUDE_EFFORT_FRONTMATTER}} placeholder, the Engram tool prefix
//     expansion, and the CodeGraph tool grant;
//   - trailing whitespace at the end of the document.
//
// Everything else, including description, tools, body, and unmatched
// markers, must equal a released template byte for byte.
func NormalizeSDDAgent(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = removeManagedBlocks(content)
	if body, ok := strings.CutPrefix(content, "---\n"); ok {
		if end := strings.Index(body, "\n---\n"); end >= 0 {
			lines := strings.Split(body[:end], "\n")
			kept := lines[:0]
			for _, line := range lines {
				switch {
				case line == "{{CLAUDE_EFFORT_FRONTMATTER}}", strings.HasPrefix(line, "effort:"):
					continue
				case strings.HasPrefix(line, "model:"):
					line = "model: {{MODEL}}"
				case strings.HasPrefix(line, "tools:"):
					line = engramToolPair.ReplaceAllStringFunc(line, func(pair string) string {
						match := engramToolPair.FindStringSubmatch(pair)
						if match[1] != match[2] {
							return pair
						}
						return "{{ENGRAM}}" + match[1]
					})
					line = engramToolSingle.ReplaceAllString(line, "{{ENGRAM}}$1")
					line = strings.Replace(line, ", mcp__codegraph__codegraph_explore", "", 1)
					line = strings.Replace(line, `, "@codegraph"]`, "]", 1)
				}
				kept = append(kept, line)
			}
			content = "---\n" + strings.Join(kept, "\n") + body[end:]
		}
	}
	return strings.TrimRight(content, " \t\n") + "\n"
}

func removeManagedBlocks(content string) string {
	for search := 0; ; {
		loc := managedBlockOpen.FindStringSubmatchIndex(content[search:])
		if loc == nil {
			return content
		}
		start := search + loc[0]
		closing := "<!-- /gentle-ai:" + content[search+loc[2]:search+loc[3]] + " -->"
		end := strings.Index(content[search+loc[1]:], closing)
		if end < 0 {
			search += loc[1]
			continue
		}
		end += search + loc[1] + len(closing)
		content = strings.TrimRight(content[:start], " \t\n") + "\n" + strings.TrimLeft(content[end:], "\n")
		search = 0
	}
}

// SDDAgentDigest is the registry key of content: SHA-256 of its normalization.
func SDDAgentDigest(content string) string {
	sum := sha256.Sum256([]byte(NormalizeSDDAgent(content)))
	return hex.EncodeToString(sum[:])
}

// RetiredSDDAgentFiles lists every native SDD sub-agent file name a release
// installed for agent, sorted. The shipped-bytes registry is the inventory.
func RetiredSDDAgentFiles(agent model.AgentID) []string {
	family, ok := SDDAgentFamilies[agent]
	if !ok {
		return nil
	}
	var names []string
	for key := range releasedSDDAgentDigests {
		if name, ok := strings.CutPrefix(key, family+"/"); ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// RetiredSDDAgentPaths joins RetiredSDDAgentFiles with dir for snapshots.
func RetiredSDDAgentPaths(agent model.AgentID, dir string) []string {
	if dir == "" {
		return nil
	}
	names := RetiredSDDAgentFiles(agent)
	paths := make([]string, 0, len(names))
	for _, name := range names {
		paths = append(paths, filepath.Join(dir, name))
	}
	return paths
}

// OwnsRetiredSDDAgent reports whether content is a render of a template some
// release shipped for this agent family and file name.
func OwnsRetiredSDDAgent(agent model.AgentID, name string, content []byte) bool {
	family, ok := SDDAgentFamilies[agent]
	if !ok || len(content) == 0 {
		return false
	}
	return slices.Contains(releasedSDDAgentDigests[family+"/"+name], SDDAgentDigest(string(content)))
}

// RetireResult lists the files a retirement removed and the inventory files
// it preserved because ownership could not be proven.
type RetireResult struct {
	Removed   []string
	Preserved []string
}

// RetireSDDAgents removes the retired SDD sub-agents in dir that Gentle AI
// rendered. Symlinks, non-regular files, and bytes no release rendered are
// preserved and reported.
func RetireSDDAgents(agent model.AgentID, dir string) (RetireResult, error) {
	var result RetireResult
	// A symlinked agents directory (dotfiles) is followed like every writer
	// follows it; only the agent files themselves must be regular files.
	if info, err := os.Stat(dir); os.IsNotExist(err) {
		return result, nil
	} else if err != nil {
		return result, fmt.Errorf("inspect agents directory %s: %w", dir, err)
	} else if !info.IsDir() {
		return result, nil
	}
	for _, name := range RetiredSDDAgentFiles(agent) {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return result, fmt.Errorf("inspect retired SDD agent %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			result.Preserved = append(result.Preserved, path)
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return result, fmt.Errorf("read retired SDD agent %s: %w", path, err)
		}
		if !OwnsRetiredSDDAgent(agent, name, data) {
			result.Preserved = append(result.Preserved, path)
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return result, fmt.Errorf("remove retired SDD agent %s: %w", path, err)
		}
		result.Removed = append(result.Removed, path)
	}
	slices.Sort(result.Preserved)
	return result, nil
}

// ManualActions tells the user what to do with every file retirement kept.
func (r RetireResult) ManualActions() []string {
	actions := make([]string, 0, len(r.Preserved))
	for _, path := range r.Preserved {
		actions = append(actions, fmt.Sprintf("Retired SDD agent %s was preserved: Gentle AI cannot prove it wrote this file (its content differs from every released version, or it is not a regular file), so it may contain your changes. SDD was retired in v4.0.0 and this agent is no longer maintained; if you no longer need it, move or delete it.", path))
	}
	return actions
}
