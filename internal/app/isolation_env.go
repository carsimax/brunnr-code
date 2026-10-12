package app

import (
	"os"

	"github.com/carsimax/brunnr-code/internal/backup"
	"github.com/carsimax/brunnr-code/internal/components/engram"
	"github.com/carsimax/brunnr-code/internal/state"
)

// validateIsolationEnv rejects an invalid BRUNNR_CODE_HOME or
// BRUNNR_CODE_ENGRAM_BIN before detection or any write, then applies BRUNNR_CODE_HOME to backup checks.
func validateIsolationEnv() error {
	for _, validate := range []func() error{state.ValidateHomeEnv, engram.ValidateCallerBinary} {
		if err := validate(); err != nil {
			return err
		}
	}
	applyStateHome()
	return nil
}

// applyStateHome points backup deletion checks at BRUNNR_CODE_HOME/backups,
// where install and sync write backups when that variable is set.
func applyStateHome() {
	if os.Getenv(state.HomeEnv) == "" {
		return
	}
	backup.BackupRootFn = func() (string, error) { return state.BackupsDir(""), nil }
}
