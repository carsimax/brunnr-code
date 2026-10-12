package app

import (
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/reviewtransaction"
	"github.com/carsimax/brunnr-code/internal/state"
	"github.com/carsimax/brunnr-code/internal/statecoord"
)

// holdCanonicalInstallStateLock acquires the real canonical install-state lock
// for home so a subsequent write site fails fast on contention, mirroring the
// contention shape used by internal/cli/install_state_lock_test.go.
func holdCanonicalInstallStateLock(t *testing.T, home string) *reviewtransaction.AuthorityFileLock {
	t.Helper()
	lockPath, err := statecoord.LockPath(home)
	if err != nil {
		t.Fatalf("install state lock path: %v", err)
	}
	held, err := reviewtransaction.AcquireAuthorityFileLock(lockPath)
	if err != nil {
		t.Fatalf("acquire install state lock for contention: %v", err)
	}
	return held
}

// TestPersistAssignmentsReReadsLatestStateUnderLock proves persistAssignments
// (site 2, #1809) applies the assignments on top of the latest state inside
// the canonical lock instead of a stale pre-lock read, so a concurrent
// writer's changes survive.
func TestPersistAssignmentsReReadsLatestStateUnderLock(t *testing.T) {
	home := t.TempDir()
	held := holdCanonicalInstallStateLock(t, home)
	selection := model.Selection{
		CodexCarrilModelAssignments: map[string]string{"odd-verify": "test-model"},
	}
	if err := persistAssignments(home, selection); err == nil || !strings.Contains(err.Error(), "acquire install state lock") {
		t.Fatalf("contended persistAssignments error = %v, want lock acquisition failure", err)
	}
	if err := state.Write(home, state.InstallState{RDDMode: "on", Preset: "concurrent"}); err != nil {
		t.Fatal(err)
	}
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}

	if err := persistAssignments(home, selection); err != nil {
		t.Fatalf("persistAssignments() error = %v", err)
	}
	got, err := state.Read(home)
	if err != nil {
		t.Fatal(err)
	}
	if got.CodexCarrilModelAssignments["odd-verify"] != "test-model" {
		t.Fatalf("assignments were not persisted: %#v", got.CodexCarrilModelAssignments)
	}
	if got.RDDMode != "on" || got.Preset != "concurrent" {
		t.Fatalf("concurrent writer state was clobbered: %#v", got)
	}
}
