package update

import (
	"path/filepath"
)

// Tools is the static registry of managed tools that can be checked for updates.
//
// InstallMethod controls which upgrade strategy the executor uses:
//   - InstallBinary: downloaded binary from GitHub Releases
var Tools = []ToolInfo{
	{
		Name:              "engram",
		Owner:             "Gentleman-Programming",
		Repo:              "engram",
		DetectCmd:         []string{"engram", "version"},
		VersionPrefix:     "v",
		ReleaseTagPattern: `^v[0-9]+\.[0-9]+\.[0-9]+$`,
		// engram: binary download from GitHub Releases on every platform.
		InstallMethod: InstallBinary,
		// FallbackPaths covers ~/.local/bin when it is not yet in PATH: when
		// /usr/local/bin is not writable, the binary installer places engram
		// there, and the current process does not see the PATH change until a
		// new shell session starts.
		FallbackPaths: func(homeDir string) []string {
			if homeDir == "" {
				return nil
			}
			return []string{filepath.Join(homeDir, ".local", "bin", "engram")}
		},
	},
}
