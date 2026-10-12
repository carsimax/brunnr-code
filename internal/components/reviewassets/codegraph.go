package reviewassets

import (
	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
	"strings"
)

// InjectCodeGraphToolGrant preserves native Markdown frontmatter and tool-free reviewers.
func InjectCodeGraphToolGrant(prompt string, agentID model.AgentID, guidance string) string {
	if strings.TrimSpace(guidance) == "" {
		return prompt
	}
	if agentID != model.AgentClaudeCode {
		return prompt
	}
	const grant = "mcp__codegraph__codegraph_explore"
	end := strings.Index(prompt, "\n---\n")
	if end < 0 {
		return prompt
	}
	lines := strings.Split(prompt[:end], "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "tools:") {
			continue
		}
		if strings.Contains(line, grant) {
			return prompt
		}
		if value := strings.TrimSpace(strings.TrimPrefix(line, "tools:")); value == "" || value == "[]" {
			return prompt
		}
		lines[i] = line + ", " + grant
		return strings.Join(lines, "\n") + prompt[end:]
	}
	return prompt
}
