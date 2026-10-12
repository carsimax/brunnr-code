package persona_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/carsimax/brunnr-code/internal/components/persona"
)

func TestOutputStylePathIsPrincipalEngineer(t *testing.T) {
	dir := t.TempDir()
	if got, want := persona.OutputStylePath(dir), filepath.Join(dir, "principal-engineer.md"); got != want {
		t.Fatalf("OutputStylePath(%q) = %q, want %q", dir, got, want)
	}
	if got := persona.OutputStylePath(""); got != "" {
		t.Fatalf("OutputStylePath(\"\") = %q, want empty", got)
	}
}

func TestLegacyOutputStylePathsIncludeRetiredNeutralStyle(t *testing.T) {
	dir := t.TempDir()
	if got, want := persona.LegacyOutputStylePaths(dir), []string{filepath.Join(dir, "neutral.md")}; !slices.Equal(got, want) {
		t.Fatalf("LegacyOutputStylePaths(%q) = %v, want %v", dir, got, want)
	}
	if got := persona.LegacyOutputStylePaths(""); len(got) != 0 {
		t.Fatalf("LegacyOutputStylePaths(\"\") = %v, want empty", got)
	}
}
