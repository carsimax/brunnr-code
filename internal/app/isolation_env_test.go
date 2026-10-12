package app

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/backup"
	"github.com/carsimax/brunnr-code/internal/state"
	"github.com/carsimax/brunnr-code/internal/system"
)

func TestRunArgsRejectsInvalidIsolationVariablesBeforeDetection(t *testing.T) {
	for _, tc := range []struct {
		name  string
		env   string
		value string
	}{
		{"relative BRUNNR_CODE_HOME", state.HomeEnv, "relative/state"},
		{"missing BRUNNR_CODE_ENGRAM_BIN", system.EngramBinEnv, filepath.Join(t.TempDir(), "engram")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupMockHome(t, t.TempDir())
			t.Setenv(tc.env, tc.value)
			oldDetect, oldEnsure := detectSystem, ensureCurrentOSSupported
			t.Cleanup(func() { detectSystem, ensureCurrentOSSupported = oldDetect, oldEnsure })
			ensureCurrentOSSupported = func() error { return nil }
			detectSystem = func(context.Context) (system.DetectionResult, error) {
				t.Fatal("system detection ran despite an invalid isolation variable")
				return system.DetectionResult{}, nil
			}

			err := RunArgs([]string{"install", "--agent", "claude-code", "--dry-run"}, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tc.env) {
				t.Fatalf("RunArgs() error = %v, want an error naming %s", err, tc.env)
			}
		})
	}
}

func TestBackupRootFollowsGentleAIHome(t *testing.T) {
	home := t.TempDir()
	setupMockHome(t, home)
	restore := backup.BackupRootFn
	t.Cleanup(func() { backup.BackupRootFn = restore })

	t.Setenv(state.HomeEnv, "")
	applyStateHome()
	if got, err := backup.BackupRootFn(); err != nil || got != filepath.Join(home, ".brunnr-code", "backups") {
		t.Fatalf("default BackupRootFn() = %q, %v", got, err)
	}

	override := filepath.Join(t.TempDir(), "brunnr-code")
	t.Setenv(state.HomeEnv, override)
	applyStateHome()
	if got, err := backup.BackupRootFn(); err != nil || got != filepath.Join(override, "backups") {
		t.Fatalf("BackupRootFn() with %s = %q, %v", state.HomeEnv, got, err)
	}
}
