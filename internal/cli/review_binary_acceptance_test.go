package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/carsimax/brunnr-code/internal/reviewtransaction"
)

func TestReviewerPayloadBindsProviderArtifactSubject(t *testing.T) {
	reviewEnabledHome(t)
	repo, started, _, record := newArtifactReview(t, false)
	lens := record.State.SelectedLenses[0]
	input := filepath.Join(t.TempDir(), "reviewer.json")
	if err := os.WriteFile(input, reviewerPayloadWithSummaryForTest(t, repo, record, lens, 0), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := RunReviewCaptureResult([]string{
		"--cwd", repo,
		"--lineage", started.LineageID,
		"--target", record.State.InitialSnapshot.Identity,
		"--lens", lens,
		"--order", "0",
		"--input", input,
	}, &output); err != nil {
		t.Fatalf("reviewer fixture omitted its provider-owned artifact subject: %v", err)
	}
	var terminal reviewLastEventClosureResult
	decodeStrictReviewJSON(t, output.Bytes(), &terminal)
	if terminal.Operation != "review/capture-result" || terminal.State != reviewtransaction.StateApproved {
		t.Fatalf("reviewer terminal capture = %#v", terminal)
	}
}

func reviewerPayloadWithSummaryForTest(t *testing.T, repo string, record reviewtransaction.CompactRecord, lens string, order int) []byte {
	t.Helper()
	return admittedReviewerPayloadForTest(t, repo, record, lens, order,
		"checked exact target")
}
