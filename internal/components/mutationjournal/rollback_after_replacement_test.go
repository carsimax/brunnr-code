package mutationjournal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/components/filemerge"
)

// replaceThenFail mimics the one window WriteFileAtomic documents: the rename
// published the new bytes and a later step failed, so the writer reports both
// Changed=true and an error. Only the first write is faulted, so the rollback
// that follows exercises the real writer.
func replaceThenFail(t *testing.T) {
	t.Helper()
	original := writeFileAtomic
	t.Cleanup(func() { writeFileAtomic = original })
	faulted := false
	writeFileAtomic = func(path string, data []byte, mode os.FileMode) (filemerge.WriteResult, error) {
		result, err := original(path, data, mode)
		if err != nil {
			t.Fatalf("underlying write %q: %v", path, err)
		}
		if faulted {
			return result, nil
		}
		faulted = true
		result.Changed = true
		return result, errors.New("injected parent-directory sync failure")
	}
}

// TestRestoreRemovesCreatedFileWhenWriteFailedAfterReplacement covers the same
// window for a path that did not exist before: rollback owes the caller an
// absent file, not an orphan created by a write that reported failure.
func TestRestoreRemovesCreatedFileWhenWriteFailedAfterReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "created.json")

	replaceThenFail(t)

	journal := New(root)
	if _, err := journal.WriteWithMode(path, []byte("orphan"), 0o644); err == nil {
		t.Fatal("WriteWithMode: want the injected error, got nil")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat after write: %v. The premise is that the file was created", err)
	}

	if err := journal.Restore(); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still exists after Restore (stat err = %v), want it removed", err)
	}
}
