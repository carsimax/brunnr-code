package cli

import (
	"bytes"
	"errors"
	"os"
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

type failAfterThemeStep struct {
	selected string
	observed *bool
	cause    error
}

func (s failAfterThemeStep) ID() string { return "test:fail-after-theme" }
func (s failAfterThemeStep) Run() error {
	data, err := os.ReadFile(s.selected)
	if err != nil || !bytes.Contains(data, []byte(`"theme":"brunnr"`)) {
		return errors.New("theme write was not observed before failure")
	}
	*s.observed = true
	return s.cause
}

func assertThemeSelectedOnly(t *testing.T, selected, decoy string) {
	t.Helper()
	data, err := os.ReadFile(selected)
	if err != nil || !bytes.Contains(data, []byte(`"theme":"brunnr"`)) || !bytes.Contains(data, []byte(`"user":true`)) || !bytes.Contains(data, []byte("// project settings")) {
		t.Fatalf("selected JSONC theme not installed: %s, %v", data, err)
	}
	data, err = os.ReadFile(decoy)
	if err != nil || string(data) != `{"theme":"user-owned"}` {
		t.Fatalf("decoy JSON changed: %s, %v", data, err)
	}
}

func fmtRollbackCase(flow string, existing bool, edit string) string {
	if existing {
		return flow + "/existing/" + edit
	}
	return flow + "/fresh/" + edit
}
