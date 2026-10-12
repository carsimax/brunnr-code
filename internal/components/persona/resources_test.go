package persona_test

import (
	"path/filepath"
	"testing"

	"github.com/carsimax/brunnr-code/internal/components/persona"
)

func TestOutputStylePathIsNeutral(t *testing.T) {
	dir := t.TempDir()
	if got, want := persona.OutputStylePath(dir), filepath.Join(dir, "neutral.md"); got != want {
		t.Fatalf("OutputStylePath(%q) = %q, want %q", dir, got, want)
	}
	if got := persona.OutputStylePath(""); got != "" {
		t.Fatalf("OutputStylePath(\"\") = %q, want empty", got)
	}
}
