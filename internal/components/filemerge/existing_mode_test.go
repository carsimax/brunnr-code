package filemerge

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestExistingFileMode(t *testing.T) {
	dir := t.TempDir()
	private := filepath.Join(dir, "private.json")
	if err := os.WriteFile(private, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(private, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(private, link); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name string
		path string
		want fs.FileMode
	}{
		{name: "existing file keeps its mode", path: private, want: 0o600},
		{name: "missing file uses fallback", path: filepath.Join(dir, "missing.json"), want: 0o644},
		{name: "directory uses fallback", path: dir, want: 0o644},
		{name: "symlink uses fallback", path: link, want: 0o644},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExistingFileMode(tt.path, 0o644); got != tt.want {
				t.Fatalf("ExistingFileMode(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
