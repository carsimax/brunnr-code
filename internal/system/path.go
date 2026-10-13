package system

import (
	"os"
	"path/filepath"
	"strings"
)

// AddToUserPath prepends dir to the current process PATH so later steps in
// this run (engram setup, MCP injection) find a binary that was just
// installed there. It does not persist PATH: new shells pick the directory up
// from the user's own shell configuration.
func AddToUserPath(dir string) error {
	return addToProcessPath(dir)
}

// addToProcessPath prepends dir to the current process PATH if it is not already
// present.
func addToProcessPath(dir string) error {
	currentPath := os.Getenv("PATH")

	// Already present in process PATH? Skip.
	for _, p := range filepath.SplitList(currentPath) {
		if strings.EqualFold(filepath.Clean(p), filepath.Clean(dir)) {
			return nil
		}
	}

	if currentPath == "" {
		return os.Setenv("PATH", dir)
	}
	return os.Setenv("PATH", dir+string(os.PathListSeparator)+currentPath)
}
