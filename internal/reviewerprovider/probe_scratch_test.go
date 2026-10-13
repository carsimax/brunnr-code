package reviewerprovider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A case-variant spelling of the source root names the same directory on a
// case-insensitive filesystem, so the scratch check must not be lexical.
func TestProbeScratchParentRefusesCaseVariantInsideWorkspace(t *testing.T) {
	source := filepath.Join(t.TempDir(), "Repo")
	if err := os.MkdirAll(filepath.Join(source, "tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	variant := filepath.Join(filepath.Dir(source), "rEPO")
	if _, err := os.Stat(variant); err != nil {
		t.Skip("case-sensitive filesystem: a case variant is a different directory")
	}
	// os.TempDir reads TMPDIR on Unix and TMP/TEMP on Windows.
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, filepath.Join(variant, "tmp"))
	}
	if _, err := probeScratchParent(source); err == nil || !strings.Contains(err.Error(), "inside the reviewed workspace") {
		t.Fatalf("probeScratchParent = %v, want inside-workspace refusal", err)
	}
}
