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
		// FallbackPaths covers the Windows stale-PATH scenario (and Linux ~/.local/bin
		// when not yet in PATH): AddToUserPath updates the registry/profile but the
		// current process does not see the change until a new shell session starts.
		FallbackPaths: func(homeDir, localAppData string) []string {
			var paths []string
			// Windows: %LOCALAPPDATA%\engram\bin\engram.exe
			if localAppData != "" {
				paths = append(paths, filepath.Join(localAppData, "engram", "bin", "engram.exe"))
			} else if homeDir != "" {
				// LOCALAPPDATA is not set (e.g. restricted environment or CI on Windows).
				// Derive the standard path from homeDir for parity with the installer.
				paths = append(paths, filepath.Join(homeDir, "AppData", "Local", "engram", "bin", "engram.exe"))
			}
			// Linux/macOS: ~/.local/bin/engram (when /usr/local/bin is not writable,
			// the binary installer places it here, which may not be in PATH yet).
			if homeDir != "" {
				paths = append(paths, filepath.Join(homeDir, ".local", "bin", "engram"))
			}
			return paths
		},
	},
}
