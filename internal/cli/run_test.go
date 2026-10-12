package cli

import (
	"path/filepath"
	"testing"
)

// stubEngramLookPath resolves engram to a path that is never executed, with
// engram setup disabled, so the Engram component only merges its settings.
func stubEngramLookPath(t *testing.T, home string) {
	t.Helper()
	t.Setenv("BRUNNR_CODE_ENGRAM_SETUP_MODE", "off")
	originalLookPath := cmdLookPath
	cmdLookPath = func(name string) (string, error) {
		if name == "engram" {
			return filepath.Join(home, "test-engram-not-executed"), nil
		}
		return originalLookPath(name)
	}
	t.Cleanup(func() { cmdLookPath = originalLookPath })
}

func fmtRollbackCase(flow string, existing bool, edit string) string {
	if existing {
		return flow + "/existing/" + edit
	}
	return flow + "/fresh/" + edit
}
