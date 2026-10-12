package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readRepositoryFile(t *testing.T, path ...string) string {
	t.Helper()
	parts := append([]string{"..", ".."}, path...)
	data, err := os.ReadFile(filepath.Join(parts...))
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Join(path...), err)
	}
	return string(data)
}

func TestIsolatedMinisignTestPublicKeyFixture(t *testing.T) {
	fixture := strings.TrimSpace(readRepositoryFile(t, "internal", "update", "upgrade", "testdata", "minisign-test.pub"))
	const expected = "RWS5glvo7U0Evs9J03vF/Lma+BY/2PMol//qa7T4gLxl7+KLNlSIDk0X"
	if fixture != expected {
		t.Fatalf("isolated Minisign test public key = %q, want %q", fixture, expected)
	}
}
