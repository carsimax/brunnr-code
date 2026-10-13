package permissions

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/components/filemerge"
	"github.com/carsimax/brunnr-code/internal/model"
)

type InjectionResult struct {
	Changed bool
	Files   []string
}

// TargetPath returns the file path that permission injection creates or updates
// for the adapter, or an empty string when the agent has no supported
// permission injection target. Codex has no target: brunnr-code relies on Codex's
// built-in default permissions and does not write its permissions config at all.
func TargetPath(homeDir string, adapter agents.Adapter) string {
	if agentOverlay(adapter.Agent()) == nil {
		return ""
	}
	return adapter.SettingsPath(homeDir)
}

// claudeCodeOverlayJSON sets Claude Code to bypassPermissions mode (auto-accept all).
// Valid modes: "acceptEdits", "bypassPermissions", "default", "dontAsk", "plan".
var claudeCodeOverlayJSON = []byte(`{
  "permissions": {
    "defaultMode": "bypassPermissions",
    "deny": [
      "Bash(rm -rf /)",
      "Bash(sudo rm -rf /)",
      "Bash(rm -rf ~)",
      "Bash(sudo rm -rf ~)",
      "Read(.env)",
      "Read(.env.*)",
      "Edit(.env)",
      "Edit(.env.*)",
      "Read(.ssh/*)",
      "Edit(.ssh/*)",
      "Read(.credentials/*)",
      "Edit(.credentials/*)",
      "Read(Library/Keychains/*)",
      "Edit(Library/Keychains/*)",
      "Read(.aws/credentials)",
      "Edit(.aws/credentials)",
      "Read(.config/gh/hosts.yml)",
      "Edit(.config/gh/hosts.yml)",
      "Read(**/*.pem)",
      "Edit(**/*.pem)",
      "Read(**/*.key)",
      "Edit(**/*.key)",
      "Read(**/secrets/*)",
      "Edit(**/secrets/*)"
    ]
  }
}
`)

// agentOverlay returns the correct permission overlay for the given agent,
// or nil if the agent does not support permission injection via settings.json.
func agentOverlay(id model.AgentID) []byte {
	switch id {
	case model.AgentClaudeCode:
		return claudeCodeOverlayJSON
	case model.AgentCodex:
		// Codex relies on its built-in default permissions. brunnr-code writes
		// nothing to Codex's permissions config — not a profile, and not the
		// legacy cleanup that used to strip one. Codex refuses to load a config
		// that defines a [permissions.*] profile without default_permissions,
		// so a cleanup removing the pointer while a user profile survived left
		// Codex unable to start (#1794). A user-defined profile stays until
		// its owner removes it.
		return nil
	default:
		return nil
	}
}

func Inject(homeDir string, adapter agents.Adapter) (InjectionResult, error) {
	return InjectAtPath(TargetPath(homeDir, adapter), adapter)
}

// InjectAtPath writes the permission overlay to the caller-selected settings
// path while preserving each adapter's permission capability check.
func InjectAtPath(settingsPath string, adapter agents.Adapter) (InjectionResult, error) {
	if settingsPath == "" {
		return InjectionResult{}, nil
	}

	overlay := agentOverlay(adapter.Agent())
	if overlay == nil {
		return InjectionResult{}, nil
	}
	writeResult, err := mergeJSONFile(settingsPath, overlay)
	if err != nil {
		return InjectionResult{}, err
	}

	return InjectionResult{Changed: writeResult.Changed, Files: []string{settingsPath}}, nil
}

// mergeJSONFile uses the JSONC merge so a malformed settings.json is refused
// and comments survive, as persona and agenthooks already do for this file.
func mergeJSONFile(path string, overlay []byte) (filemerge.WriteResult, error) {
	baseJSON, err := osReadFile(path)
	if err != nil {
		return filemerge.WriteResult{}, err
	}

	overlay, err = withUserDenyRules(baseJSON, overlay)
	if err != nil {
		return filemerge.WriteResult{}, err
	}
	merged, err := filemerge.MergeJSONCObjects(baseJSON, overlay)
	if err != nil {
		return filemerge.WriteResult{}, err
	}

	return filemerge.WriteFileAtomic(path, merged, filemerge.ExistingFileMode(path, 0o644))
}

// withUserDenyRules appends the user's own permissions.deny entries to the
// managed list, because the merge replaces arrays and deny rules are the only
// guard left under bypassPermissions.
func withUserDenyRules(baseJSON, overlay []byte) ([]byte, error) {
	base, err := filemerge.UnmarshalJSONObject(baseJSON)
	if err != nil {
		return overlay, nil // the merge itself refuses a malformed base
	}
	basePermissions, _ := base["permissions"].(map[string]any)
	userDeny, _ := basePermissions["deny"].([]any)
	if len(userDeny) == 0 {
		return overlay, nil
	}
	var result map[string]any
	if err := json.Unmarshal(overlay, &result); err != nil {
		return nil, fmt.Errorf("unmarshal permissions overlay: %w", err)
	}
	permissions, _ := result["permissions"].(map[string]any)
	deny, _ := permissions["deny"].([]any)
	seen := make(map[string]bool, len(deny)+len(userDeny))
	for _, rule := range deny {
		key, _ := json.Marshal(rule)
		seen[string(key)] = true
	}
	for _, rule := range userDeny {
		key, _ := json.Marshal(rule)
		if !seen[string(key)] {
			seen[string(key)] = true
			deny = append(deny, rule)
		}
	}
	permissions["deny"] = deny
	return json.Marshal(result)
}

var osReadFile = func(path string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read json file %q: %w", path, err)
	}

	return content, nil
}
