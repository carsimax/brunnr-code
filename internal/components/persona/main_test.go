package persona

import (
	"os"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/testenv"
)

// TestMain neutralizes ambient agent runtime-dir overrides before any test
// runs: real adapter config-path resolution can honor such overrides directly
// from the environment, bypassing whatever t.TempDir() home a test passes in.
func TestMain(m *testing.M) {
	testenv.Isolate()
	os.Exit(m.Run())
}
