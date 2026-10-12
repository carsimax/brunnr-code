package cli

import (
	"bytes"
	"testing"

	"github.com/carsimax/brunnr-code/internal/reviewtransaction"
)

func TestNegotiatedStatusRoutesHistoricalScopeChangeToRecovery(t *testing.T) {
	reviewEnabledHome(t)
	repo := initReviewCLIRepo(t)
	writeReviewStartCandidate(t, repo, "docs/attempt.md", "# reviewed\n", 0o644)
	historical := seedHistoricalApprovalForRepo(t, repo, "scope-recovery-historical")
	writeReviewStartCandidate(t, repo, "docs/attempt.md", "# changed\n", 0o644)

	var output bytes.Buffer
	if err := RunReview([]string{
		"status", "--cwd", repo, "--contract", ReviewIntegrationContractV1,
		"--lineage", historical.Record.State.LineageID, "--next-transition",
	}, &output); err != nil {
		t.Fatal(err)
	}
	var status ReviewTargetStatusResult
	decodeStrictReviewJSON(t, output.Bytes(), &status)
	if status.Applicability != reviewtransaction.TargetApplicabilityUnrelated || status.Action != reviewtransaction.TargetStatusActionStart ||
		status.NextTransition == nil || status.NextTransition.Kind != reviewNextTransitionExecute ||
		status.NextTransition.ReasonCode != "fresh_target_ready" {
		t.Fatalf("historical scope status = %#v", status)
	}
}
