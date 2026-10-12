package cli

import (
	"testing"
)

// installTestHome isolates an install under a temporary home with no runnable
// external tools.
func installTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	oldHome, oldRun, oldLookPath := osUserHomeDir, runCommand, cmdLookPath
	osUserHomeDir = func() (string, error) { return home, nil }
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = missingBinaryLookPath
	t.Cleanup(func() {
		osUserHomeDir, runCommand, cmdLookPath = oldHome, oldRun, oldLookPath
	})
	return home
}
