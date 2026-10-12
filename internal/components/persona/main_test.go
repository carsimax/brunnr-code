package persona

import (
	"os"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/testenv"
)

// TestMain neutralizes ambient agent runtime-dir overrides (OPENCODE_CONFIG_DIR)
// before any test runs: this package resolves real OpenCode adapters
// (opencodeAdapter()) whose
// config-path resolution honors these overrides directly from the environment,
// bypassing whatever t.TempDir() home a test passes in.
func TestMain(m *testing.M) {
	testenv.Isolate()
	os.Exit(m.Run())
}
