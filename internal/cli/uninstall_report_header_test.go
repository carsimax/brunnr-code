package cli

import (
	"strings"
	"testing"

	componentuninstall "github.com/gentleman-programming/gentle-ai/v4/internal/components/uninstall"
	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
)

// A batch that failed for one agent must not open with "complete". The user
// reads the header before the manual-cleanup detail printed further down.
func TestRenderUninstallReportHeaderNamesAPartialBatch(t *testing.T) {
	report := RenderUninstallReport(componentuninstall.Result{
		FailedAgents: []model.AgentID{model.AgentCodex},
	})
	header := strings.SplitN(report, "\n", 2)[0]
	if !strings.Contains(header, "partially complete") {
		t.Fatalf("header = %q, want it to name the batch as partial", header)
	}
	if !strings.Contains(header, "Codex") && !strings.Contains(header, "codex") {
		t.Fatalf("header = %q, want it to name the failed agent", header)
	}
}

func TestRenderUninstallReportHeaderStaysCompleteWhenNothingFailed(t *testing.T) {
	report := RenderUninstallReport(componentuninstall.Result{})
	header := strings.SplitN(report, "\n", 2)[0]
	if header != "Managed uninstall complete" {
		t.Fatalf("header = %q, want the unchanged complete header", header)
	}
}
