package agentguidance_test

import (
	"os"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/components/agentguidance"
	"github.com/gentleman-programming/gentle-ai/v4/internal/components/reviewassets"
	"github.com/gentleman-programming/gentle-ai/v4/internal/testenv"
)

// TestMain wires the production review contract source the installer
// registers, so rendered prompts in this package match what users receive.
// It also neutralizes ambient agent runtime-dir overrides (OPENCODE_CONFIG_DIR
// and friends): this package's many catalog.AllAgents() loops resolve agent
// config paths, and a developer shell exporting one would otherwise redirect
// these tests into the real agent home.
func TestMain(m *testing.M) {
	testenv.Isolate()
	agentguidance.SetReviewContractSource(reviewassets.ReviewExecutionContractFor)
	os.Exit(m.Run())
}
