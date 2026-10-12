package update

import "github.com/carsimax/brunnr-code/internal/system"

// updateHint returns a platform-specific instruction string for updating the given tool.
func updateHint(tool ToolInfo, profile system.PlatformProfile) string {
	switch tool.Name {
	case "engram":
		return engramHint(profile)
	default:
		return ""
	}
}

func engramHint(profile system.PlatformProfile) string {
	return "brunnr-code upgrade (downloads pre-built binary)"
}
