package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
	"github.com/gentleman-programming/gentle-ai/v4/internal/reviewerprovider"
	"github.com/gentleman-programming/gentle-ai/v4/internal/reviewtransaction"
)

// stubCompiledProviderRawForTest makes the compiled claude-code runtime
// return raw for every provider capture, so provider role captures run
// through the Go-owned --execute path without spawning a model.
func stubCompiledProviderRawForTest(t *testing.T, raw []byte) {
	t.Helper()
	previous := reviewProviderAdapterFor
	reviewProviderAdapterFor = func(_ reviewerprovider.Contract, agent model.AgentID) (reviewerprovider.Adapter, error) {
		if agent != model.AgentClaudeCode {
			return nil, errors.New("unexpected runtime")
		}
		return providerTestAdapter{raw: raw}, nil
	}
	t.Cleanup(func() { reviewProviderAdapterFor = previous })
}

// roleRefuterReviewForTest builds a reviewing authority whose one captured lens carries
// a severe inferential finding, so the transaction-wide refuter batch is
// required before terminal closure.
func roleRefuterReviewForTest(t *testing.T) (string, reviewtransaction.CompactStore, reviewtransaction.CompactRecord, string) {
	t.Helper()
	repo, started, store, record := newArtifactReview(t, false)
	result := admittedReviewerResultForTest(t, repo, record, record.State.SelectedLenses[0], 0)
	result.Findings = []facadeFinding{{
		ID: "R3-001", Location: "tracked.txt:1", Severity: "CRITICAL", Claim: "candidate failure",
		ProofRefs: []string{"tracked.txt:1 candidate-specific proof"}, EvidenceClass: reviewtransaction.EvidenceInferential,
		CausalDisposition: reviewtransaction.CausalBehaviorActivated,
	}}
	input := filepath.Join(t.TempDir(), "result.json")
	writeReviewCLIJSON(t, input, result)
	if err := RunReviewCaptureResult([]string{
		"--cwd", repo, "--lineage", started.LineageID, "--target", record.State.InitialSnapshot.Identity,
		"--lens", record.State.SelectedLenses[0], "--order", "0", "--input", input,
	}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	updated, loadErr := store.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	record = updated
	handle, err := reviewtransaction.DeriveReviewRepositoryContextHandle(t.Context(), repo, reviewtransaction.ReviewRepositoryContextBinding{
		LineageID: record.State.LineageID, TargetIdentity: record.State.InitialSnapshot.Identity, Revision: record.State.CapturePhaseRevision,
	})
	if err != nil {
		t.Fatal(err)
	}
	return repo, store, record, handle
}

func roleRefuterBindingForTest(repo string, record reviewtransaction.CompactRecord, handle string) []string {
	return []string{
		"--cwd", repo,
		"--repository-context", handle, "--lineage", record.State.LineageID,
		"--target", record.State.InitialSnapshot.Identity, "--expected-revision", record.State.CapturePhaseRevision,
	}
}

func roleRefuterRawResultForTest(t *testing.T, repo string, store reviewtransaction.CompactStore, record reviewtransaction.CompactRecord) []byte {
	t.Helper()
	request, err := reviewProviderNewRefuterRequest(t.Context(), repo, store.Dir, record.State, record.State.CapturePhaseRevision)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(facadeRefuterResult{RequestHash: request.RequestHash, Results: []facadeRefuterOutcome{{
		FindingID: "R3-001", Outcome: reviewtransaction.OutcomeCorroborated, ProofRefs: []string{"independent reproduction"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestHostileProviderRoleCaptureTransitionsFailClosedWithoutPanic decodes
// hostile role-capture transitions -- zero arguments, and a truncated
// argument vector -- and requires a typed refusal from Validate(), never a
// panic from unguarded slice arithmetic over the argument count.
func TestHostileProviderRoleCaptureTransitionsFailClosedWithoutPanic(t *testing.T) {
	for _, test := range []struct {
		name      string
		operation string
		arguments string
	}{
		{name: "refuter with zero arguments", operation: "review.capture-refuter", arguments: `[]`},
		{name: "validation with zero arguments", operation: "review.capture-validation", arguments: `[]`},
		{
			name: "refuter with a truncated argument vector", operation: "review.capture-refuter",
			arguments: `[{"name":"lineage","value":"hostile"}]`,
		},
		{
			name: "validation with a truncated argument vector", operation: "review.capture-validation",
			arguments: `[{"name":"lineage","value":"hostile"}]`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := `{"kind":"collect","reason_code":"provider_refuter_required","collect":{"inputs":[{` +
				`"name":"provider_refuter","schema":"` + reviewRefuterSchemaID + `",` +
				`"capture_operation":"` + test.operation + `","arguments":` + test.arguments + `}]}}`
			var transition ReviewNextTransition
			if err := json.Unmarshal([]byte(payload), &transition); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("hostile role capture transition panicked: %v", recovered)
				}
			}()
			if err := transition.Validate(); err == nil {
				t.Fatal("hostile role capture transition validated")
			}
		})
	}
}

func TestNegotiatedStatusOffersCurrentValidationCaptureForOtherRuntimes(t *testing.T) {
	reviewEnabledHome(t)
	repo, lineage, _ := providerCorrectionReadyWithoutVerificationEvidence(t)

	// claude-code keeps the current targeted-validation collection.
	var compiled bytes.Buffer
	if err := RunReview([]string{
		"status", "--cwd", repo, "--lineage", lineage, "--contract", ReviewIntegrationContractV2,
		"--agent", string(model.AgentClaudeCode), "--next-transition",
	}, &compiled); err != nil {
		t.Fatal(err)
	}
	var compiledStatus ReviewTargetStatusResult
	decodeStrictReviewJSON(t, compiled.Bytes(), &compiledStatus)
	if compiledStatus.NextTransition == nil || compiledStatus.NextTransition.ReasonCode != "targeted_validation_required" ||
		compiledStatus.NextTransition.Collect == nil || len(compiledStatus.NextTransition.Collect.Inputs) != 1 {
		t.Fatalf("compiled validation transition changed: %#v", compiledStatus.NextTransition)
	}
	compiledInput := compiledStatus.NextTransition.Collect.Inputs[0]
	if compiledInput.CaptureOperation != reviewCaptureValidationCaptureOperation || compiledInput.ValidationRequest == nil {
		t.Fatalf("compiled validation rendering changed: %#v", compiledInput)
	}
}

func TestReviewCaptureValidationBindsFrozenRequestHash(t *testing.T) {
	reviewEnabledHome(t)
	repo, lineage, request := providerCorrectionReadyWithoutVerificationEvidence(t)
	store, err := reviewtransaction.CompactAuthoritativeStore(t.Context(), repo, lineage)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	handle, err := reviewtransaction.DeriveReviewRepositoryContextHandle(t.Context(), repo, reviewtransaction.ReviewRepositoryContextBinding{LineageID: request.LineageID, TargetIdentity: request.CorrectionTargetIdentity, Revision: request.ExpectedRevision})
	if err != nil {
		t.Fatal(err)
	}
	binding := []string{
		"--cwd", repo,
		"--repository-context", handle, "--lineage", lineage,
		"--target", request.CorrectionTargetIdentity, "--expected-revision", record.State.CapturePhaseRevision,
	}
	stale := append(slices.Clone(binding), "--request-hash", "sha256:"+strings.Repeat("0", 64),
		"--agent", string(model.AgentClaudeCode), "--execute=true")
	err = RunReview(append([]string{"capture-validation"}, stale...), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "request hash does not match") {
		t.Fatalf("stale validation request hash refusal = %v", err)
	}
	missing := append(slices.Clone(binding), "--agent", string(model.AgentClaudeCode), "--execute=true")
	err = RunReview(append([]string{"capture-validation"}, missing...), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "--request-hash") {
		t.Fatalf("missing validation request hash refusal = %v", err)
	}
}

func TestRefuterRequestCarriesTheFindingClaimText(t *testing.T) {
	reviewEnabledHome(t)
	repo, store, record, _ := roleRefuterReviewForTest(t)
	request, err := reviewProviderNewRefuterRequest(t.Context(), repo, store.Dir, record.State, record.State.CapturePhaseRevision)
	if err != nil {
		t.Fatal(err)
	}
	prompt := string(request.Invocation.Prompt())
	if !strings.Contains(prompt, `"finding_id":"R3-001"`) || !strings.Contains(prompt, `"claim":"candidate failure"`) {
		t.Fatalf("refuter request omits the finding's claim text; the refuter can only return inconclusive:\n%s", prompt)
	}
}

func recordHasAdmittedRole(state reviewtransaction.CompactState, role reviewtransaction.CompactRole) bool {
	for _, entry := range state.AdmittedRoleResults {
		if entry.Role == role {
			return true
		}
	}
	return false
}

// collectInputArgumentTokensForTest renders a collect input's provider-issued
// argument tokens in order, exactly as a host runs them.
func collectInputArgumentTokensForTest(input ReviewTransitionInput) []string {
	tokens := make([]string, 0, len(input.Arguments))
	for _, argument := range input.Arguments {
		tokens = append(tokens, reviewTransitionArgumentToken(argument))
	}
	return tokens
}
