package update

import (
	"fmt"

	"github.com/carsimax/brunnr-code/internal/system"
)

// updateHint returns a platform-specific instruction string for updating the given tool.
func updateHint(tool ToolInfo, profile system.PlatformProfile) string {
	switch tool.Name {
	case "engram":
		return engramHint(profile)
	default:
		return ""
	}
}

func updateHintForOwnership(tool ToolInfo, profile system.PlatformProfile, ownership HomebrewOwnership) string {
	if profile.PackageManager == "brew" && ownership != HomebrewNone {
		return fmt.Sprintf("brew upgrade --%s %s", ownership, tool.Name)
	}
	return updateHint(tool, profile)
}

func engramHint(profile system.PlatformProfile) string {
	if profile.PackageManager == "brew" && homebrewPackageInstalled("engram") {
		return "brew upgrade engram"
	}
	return "brunnr-code upgrade (downloads pre-built binary)"
}
