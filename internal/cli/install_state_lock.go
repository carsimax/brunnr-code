package cli

import "github.com/carsimax/brunnr-code/internal/statecoord"

func withInstallStateLock(homeDir string, operation func() error) error {
	return statecoord.WithLock(homeDir, operation)
}
