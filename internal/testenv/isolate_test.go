package testenv_test

import (
	"os"
	"testing"

	"github.com/carsimax/brunnr-code/internal/testenv"
)

// Isolate removes inherited overrides entirely, rather than setting empty
// values. Behavioral path/write coverage lives in the subprocess regressions.
func TestIsolateUnsetsKnownAgentRuntimeDirOverrides(t *testing.T) {
	for _, key := range []string{"XDG_CONFIG_HOME", "BRUNNR_CODE_HOME", "BRUNNR_CODE_ENGRAM_BIN"} {
		t.Setenv(key, t.TempDir())
	}

	testenv.Isolate()

	for _, key := range []string{"XDG_CONFIG_HOME", "BRUNNR_CODE_HOME", "BRUNNR_CODE_ENGRAM_BIN"} {
		if got, ok := os.LookupEnv(key); ok {
			t.Fatalf("Isolate() left %s = %q, want unset", key, got)
		}
	}
}
