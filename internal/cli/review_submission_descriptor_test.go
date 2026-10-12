package cli

import (
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/reviewtransaction"
)

func descriptorTestSHA(char string) string { return "sha256:" + strings.Repeat(char, 64) }

func TestCorrectionPlanDescriptorBindsOnlyCurrentCapture(t *testing.T) {
	binding := ReviewTransitionBinding{
		LineageID: "descriptor-correction", Revision: descriptorTestSHA("a"), TargetIdentity: descriptorTestSHA("b"),
		RepositoryContext: "rctx1_" + strings.Repeat("c", 64),
	}
	request := reviewtransaction.CorrectionPlanRequest{
		LineageID: binding.LineageID, ExpectedRevision: binding.Revision, TargetIdentity: binding.TargetIdentity,
		RequestHash: descriptorTestSHA("d"), CorrectionBudget: 7,
	}
	descriptor := reviewCorrectionPlanSubmission(ReviewIntegrationContractV2, binding, request)
	if descriptor == nil || descriptor.OperationToken != "capture-correction-plan" || descriptor.Value == nil ||
		descriptor.Value.Slot != "correction_lines" || descriptor.Value.Maximum != request.CorrectionBudget {
		t.Fatalf("correction-plan descriptor = %#v", descriptor)
	}
	if err := descriptor.validateCorrectionPlan(); err != nil {
		t.Fatalf("current correction-plan descriptor = %v", err)
	}
}
