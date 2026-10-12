package upgrade

import (
	"io/fs"
	"sort"
	"testing"

	"github.com/carsimax/brunnr-code/internal/assets"
)

// embeddedSharedFileNames returns the names of every file embedded under
// skills/_shared. Upgrade backup coverage is asserted against this listing
// rather than a literal so a newly added shared file cannot silently miss it.
func embeddedSharedFileNames(t *testing.T) []string {
	t.Helper()

	entries, err := fs.ReadDir(assets.FS, "skills/_shared")
	if err != nil {
		t.Fatalf("ReadDir(assets.FS, skills/_shared) error = %v", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	if len(names) == 0 {
		t.Fatal("embedded skills/_shared listing is empty; the source of truth cannot be empty")
	}
	sort.Strings(names)
	return names
}
