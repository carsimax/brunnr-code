package system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAddToUserPathAlreadyPresent verifies that if the directory is already in PATH,
// AddToUserPath returns nil and does not duplicate it.
func TestAddToUserPathAlreadyPresent(t *testing.T) {
	// Set up a PATH that already contains the target dir.
	targetDir := filepath.Join(t.TempDir(), "already-present")
	original := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", original) })

	os.Setenv("PATH", targetDir+string(os.PathListSeparator)+original)

	err := AddToUserPath(targetDir)
	if err != nil {
		t.Fatalf("AddToUserPath returned unexpected error: %v", err)
	}

	// PATH should not have duplicates.
	currentPath := os.Getenv("PATH")
	count := 0
	for _, p := range filepath.SplitList(currentPath) {
		if strings.EqualFold(filepath.Clean(p), filepath.Clean(targetDir)) {
			count++
		}
	}
	if count > 1 {
		t.Fatalf("expected dir to appear at most once in PATH, got %d occurrences", count)
	}
}

// TestAddToProcessPathAddsToProcessEnv verifies the process-local PATH update.
func TestAddToProcessPathAddsToProcessEnv(t *testing.T) {
	targetDir := filepath.Join(t.TempDir(), "new-bin-dir")
	original := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", original) })

	// Ensure target is NOT currently in PATH.
	os.Setenv("PATH", strings.ReplaceAll(original, targetDir, ""))

	err := addToProcessPath(targetDir)
	if err != nil {
		t.Fatalf("addToProcessPath returned unexpected error: %v", err)
	}

	// The directory must now be in the process PATH.
	found := false
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if strings.EqualFold(filepath.Clean(p), filepath.Clean(targetDir)) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %q to be present in process PATH after AddToUserPath, got: %s", targetDir, os.Getenv("PATH"))
	}
}

// TestAddToUserPathPrependsToProcessPath pins AddToUserPath to the process PATH:
// the new directory becomes the first entry so the fresh binary wins lookups.
func TestAddToUserPathPrependsToProcessPath(t *testing.T) {
	targetDir := filepath.Join(t.TempDir(), "bin")
	t.Setenv("PATH", filepath.Join(t.TempDir(), "tools"))

	if err := AddToUserPath(targetDir); err != nil {
		t.Fatalf("AddToUserPath() error = %v", err)
	}

	entries := filepath.SplitList(os.Getenv("PATH"))
	if len(entries) != 2 || entries[0] != targetDir {
		t.Fatalf("PATH entries = %v, want %q first of two", entries, targetDir)
	}
}
