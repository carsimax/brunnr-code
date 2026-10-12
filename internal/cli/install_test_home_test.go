package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/components/gga"
)

func TestInstallTestHomeIsolatesGGA(t *testing.T) {
	inherited := t.TempDir()
	t.Setenv("APPDATA", inherited)
	t.Run("isolated home", func(t *testing.T) {
		home := installTestHome(t)
		want := filepath.Join(home, "AppData", "Roaming")
		if got := os.Getenv("APPDATA"); got != want {
			t.Errorf("APPDATA = %q, want %q", got, want)
		}
		for _, path := range []string{gga.ConfigPath(home), gga.AgentsTemplatePath(home)} {
			rel, err := filepath.Rel(home, path)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				t.Errorf("GGA path %q escapes isolated home %q", path, home)
			}
		}
	})
	if got := os.Getenv("APPDATA"); got != inherited {
		t.Errorf("APPDATA after cleanup = %q, want %q", got, inherited)
	}
}

// installTestHome isolates an install under a temporary home with no runnable
// external tools.
func installTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	// GGA resolves its Windows config from APPDATA, not osUserHomeDir.
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	oldHome, oldRun, oldLookPath := osUserHomeDir, runCommand, cmdLookPath
	osUserHomeDir = func() (string, error) { return home, nil }
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = missingBinaryLookPath
	t.Cleanup(func() {
		osUserHomeDir, runCommand, cmdLookPath = oldHome, oldRun, oldLookPath
	})
	return home
}
