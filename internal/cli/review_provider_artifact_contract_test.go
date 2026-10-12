package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/carsimax/brunnr-code/internal/reviewtransaction"
)

func TestReviewProviderArtifactV1ContractsArePinned(t *testing.T) {
	root := filepath.Join("..", "..", "contracts", "review-integration", "v1")
	want := map[string]string{
		"fixtures/capabilities-v1.4.fixture.json": "c05c864dfdf3bc88dc43c53a59f8fca84bcd03b43495f56b9388f34997a98d23",
		"fixtures/consent.fixture.json":           "094ecda6f49497d55ff04c858a005c42f341ab064318db5519464054671bac3f",
		"fixtures/start.fixture.json":             "1491559938db09e6fea965edcc09a7b1a9a0f89c3650dace2c91ebfcab172d2d",
		// issue #2659: start-v2/status-v2 embed a freshly minted target_identity,
		// and the purified identity domain legitimately changed that hash for
		// every new snapshot. Deliberate, not drift.
		"fixtures/start-v2.fixture.json":         "d7380b3eade27f1907445186425fd397ffbfe19f112b2d9763da6a9e639729b4",
		"fixtures/status.fixture.json":           "2a6219f318398c4d97475237109e5d7a316ddda8b2ba881fa7a627cdfbf3edf8",
		"fixtures/status-v2.fixture.json":        "542652c2d4fb505f7017b4ba0c3002374994abd6e3fb0784cf99baef9f0bb80d",
		"fixtures/status-ambiguous.fixture.json": "9d43127b38c2be9b74cf10c714b757f18726f5f79593392a512bd7d93f2a6651",
		"fixtures/status-corrupted.fixture.json": "9446ac3c793e8f35819018c63d9a30c086a1a77130f5e772e75179416ba43a87",
		"fixtures/status-recover.fixture.json":   "9baaf10eccb9753c4abd9482a663caba9a24fafb39c7a557df4aa60126726262",
		"fixtures/status-unrelated.fixture.json": "66612e4b28eb3187a8e8763e9b506c847143adf666702ac4fde92a96f204850e",
		"schemas/admitted-result.schema.json":    "e3b67652232e76ba354daf5dd41867bfdadda568c9142440a95545b132ed9cf0",
		"schemas/artifact-subject.schema.json":   "0d63e18511d589b0d61756d294c043a031b55a8afb7b8ea60e9b894f978ea8d6",
		"schemas/capabilities-v1.4.schema.json":  "2a5dee5f8400745256f88c029a12ceaf60faba225dce843412ea579b00c3a308",
		"schemas/result-artifact.schema.json":    "ce1f22d7be28c9af3395e845168589940d75d3804d52a02fc19f06cb1b92c8f8",
		"schemas/start.schema.json":              "756dce3af947ad5136c4000e0ce5ff990f262b009adbe0afc74985f69b31cb1e",
		// Frozen v1 START artifacts do not project the v3 replay or retired
		// stale-burn fields.
		//
		// rdd-risk-gated S15: the risk reason enums admit the dangerous_sink
		// code and signal the classifier already publishes. Deliberate, not drift.
		//
		// rdd-risk-gated S14: the risk reason enums admit the agent_escalation
		// code and signal START publishes for --escalate-item. Deliberate, not drift.
		"schemas/start-v2.schema.json":             "4c5109ef12b1eff1fa03fbe08a4848116c259590326a83aee1dcb231ffb47e7d",
		"schemas/status.schema.json":               "468bb5b4271a3df90dc44b2a1ddf1d43e042f9b7cb23ee77d81747a019eaa74a",
		"schemas/status-v2.schema.json":            "1b5befd9232a1ba3cc2af7312f149136e038e98a55bd1c814d6db226e9fc16f3",
		"schemas/transition-execution.schema.json": "882c0914d4694084e53cf705db8ca17156cd7ae4fca6b434b1888beff66183e3",
	}
	for name, expected := range want {
		payload, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(payload)
		if actual := hex.EncodeToString(digest[:]); actual != expected {
			t.Fatalf("%s digest = %s, want %s", name, actual, expected)
		}
	}
}

func TestReviewProviderArtifactV20ContractsArePinned(t *testing.T) {
	root := filepath.Join("..", "..", "contracts", "review-integration", "v2")
	want := map[string]string{
		"fixtures/capabilities.fixture.json": "e7ad03059d85f4fc80365ac894bae05c919dbceb1962f31231aa4476bcb1eb0a",
		"fixtures/consent.fixture.json":      "d03b4f2cb41a06965fff0d452bedafee3a878f721380edbe646a7ed4853c7586",
		// issue #3922 / #4199: the native-git reviewer_result
		// collect input no longer inlines changed_path_manifest -- it is
		// already committed to by artifact_subject.changed_path_manifest_sha256
		// -- so this fixture legitimately dropped that array. Deliberate, not
		// drift.
		"fixtures/status.fixture.json":     "33693704daf97aeb68fce3cc4adea635f4f2bdca6151b2898f923c557e639ff9",
		"schemas/capabilities.schema.json": "bcfd7ebacdc01f0bc56da9b5a9a9124110c2a88667bb8cd8c279fd4e2c144f81",
		"schemas/consent.schema.json":      "22161f77fdd6aac1fde700b1219f138e0e4e8974f12d8723a75d4b06b8e860d7",
		"schemas/status.schema.json":       "2636edf15fa10757a2bc01d0d09bdd143fc9686e0b2391c1f053cdc216041b38",
	}
	for name, expected := range want {
		payload, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(payload)
		if actual := hex.EncodeToString(digest[:]); actual != expected {
			t.Fatalf("%s digest = %s, want %s", name, actual, expected)
		}
	}
}

func TestReviewProviderArtifactV21ContractsArePinned(t *testing.T) {
	root := filepath.Join("..", "..", "contracts", "review-integration", "v2")
	want := map[string]string{
		"fixtures/capabilities-v2.1.fixture.json": "c53be1906a17b9ead9ef8999ec2a1add1f7370ec258d1ad85953e9c3f53de247",
		// issue #2659: consent-v3 embeds a freshly minted target_identity;
		// the purified identity domain legitimately changed that hash.
		// Deliberate, not drift.
		"fixtures/consent-v3.fixture.json":      "a4d904f4532b67547cba465c01e9aaaf753f60ccf22af65b494df62dccd29c31",
		"schemas/capabilities-v2.1.schema.json": "46a9b0acd62bc81e94575cdc3c245ec33c236614510f0425b3417875f9258fb4",
		// Cross-lane battery conformance fix: the schema pinned the choice
		// invocations to `--agent claude-code`, but the live emitter omits the
		// agent token when the caller declared no runtime (the pinned fixture
		// itself carries no --agent), and #2676 binds the declared runtime
		// (claude-code, codex) when there is one. The schema now
		// follows the emitter. The agent enum also admits "pi": the Pi host
		// relay drives consent with its own declared runtime identity, which
		// the emitter legitimately publishes once the relay handshake is
		// declared. Deliberate, not drift.
		"schemas/consent-v3.schema.json": "bc27e6e89a7686830cb38d57cb86c5d83ca398d5f88e19e811ea96a889136512",
		"schemas/status.schema.json":     "2636edf15fa10757a2bc01d0d09bdd143fc9686e0b2391c1f053cdc216041b38",
	}
	for name, expected := range want {
		payload, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(payload)
		if actual := hex.EncodeToString(digest[:]); actual != expected {
			t.Fatalf("%s digest = %s, want %s", name, actual, expected)
		}
	}
}

func TestReviewProviderArtifactV25StatusContractsArePinned(t *testing.T) {
	root := filepath.Join("..", "..", "contracts", "review-integration", "v2")
	want := map[string]string{
		"fixtures/status-v5.fixture.json": "cc3257f7aaa36f7765558963325969e9885dde7384f2cd40c002ec5bcdd26b14",
		// Cross-lane battery conformance fix: live negotiated STATUS publishes
		// the top-level repository_context reference (review_status_contract.go's
		// ReviewTargetStatusResult, populated since the recovered-units merges),
		// which the schema did not admit; and targeted_validation_required also
		// arrives as the provider-task (external.run_provider_role) and pi
		// host-relay (review.capture-validation) shapes, which the schema
		// rejected as missing the generic submission; and the negotiated-route
		// disposition preview (ReviewRepairDispositionProviderInputs) is real
		// optional emitter output the strict schema must admit; and the pi
		// host-relay materialize path renders the reviewer_result collect
		// input with a capture-result submission descriptor, which the
		// submission oneOf and the no-submission allOf rule both rejected.
		// Deliberate, not drift.
		//
		// issue #3922 / #4199: the review.capture-result `then`
		// clause no longer requires changed_path_manifest -- it stays an
		// allowed property, but the native-git transport no longer needs to
		// inline it since artifact_subject.changed_path_manifest_sha256 already
		// commits to it. Deliberate, not drift.
		//
		// rdd-risk-gated S15: the risk reason enums admit the dangerous_sink
		// code and signal the classifier already publishes. Deliberate, not drift.
		//
		// rdd-risk-gated S14: the risk reason enums admit the agent_escalation
		// code and signal START publishes for --escalate-item. Deliberate, not drift.
		"schemas/start.schema.json":     "56fb4fa02418cf080f47dcc4219171af1a40ceddc70f17e92363d56f1473f327",
		"schemas/status-v5.schema.json": "d076b806b085929b9872d8e73d0732ca0c40ff89f78734c206bbaa64f5699d67",
	}
	for name, expected := range want {
		payload, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(payload)
		if actual := hex.EncodeToString(digest[:]); actual != expected {
			t.Fatalf("%s digest = %s, want %s", name, actual, expected)
		}
	}
}

func TestReviewProviderArtifactV24IntendedUntrackedContractsArePinned(t *testing.T) {
	root := filepath.Join("..", "..", "contracts", "review-integration", "v2")
	want := map[string]string{
		"schemas/capabilities-v2.4.schema.json": "5bf7ddc9159e1a9cfad9a6d9691607da223a0d1db057510cbf91094f326cfe3b",
		// Issue #4821: an absolute document ID restores sibling $ref resolution;
		// the wire envelope identity remains unchanged. Deliberate, not drift.
		"schemas/intended-untracked-selection.schema.json": "f01038e08bca0c9ea280091751c1dac39adfc899a3387840fa9e5de2bf2eafae",
		"schemas/status-v6.schema.json":                    "958f59b3ce0e846a027ad838dce382d52333ff06c5a1d8ffcad6523a7d7f8a3a",
	}
	for name, expected := range want {
		payload, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(payload)
		if actual := hex.EncodeToString(digest[:]); actual != expected {
			t.Fatalf("%s digest = %s, want %s", name, actual, expected)
		}
	}
}

// TestReviewProviderArtifactStatusV7ContractsArePinned pins the schemas issue
// #4040's fix first published: status/v7, which publishes
// eligible_untracked_inventory unconditionally at the top level, and
// capabilities-v2.5, which advertises status/v7 (design decision 5).
func TestReviewProviderArtifactStatusV7ContractsArePinned(t *testing.T) {
	root := filepath.Join("..", "..", "contracts", "review-integration", "v2")
	want := map[string]string{
		// issue #3928: the root action enum gained "collect" and "execute", the
		// v7 envelope's projection of a live transaction whose next_transition
		// is mandatory. Deliberate, not drift.
		//
		// issues #3299, #4170: a stale managed-asset digest now fails STATUS's
		// own preflight, before a START is ever offered, as a typed
		// managed_assets_outdated "stop" that carries the exact
		// candidate-preserving `brunnr-code sync` continuation (see
		// failure.schema.json#/$defs/managed_assets_continuation). Deliberate,
		// not drift.
		// issue #3442: next_transition gained a third oneOf branch for the
		// unachievable_lens_slot stop, carrying one unachievable_lens_slots
		// entry per declared slot (lens, subject_hash, reason, and the exact
		// review.capture-unachievable --withdraw=true command), so a
		// restarted orchestrator that lost the pre-stop collect offer can
		// still recover the binding its withdraw needs. Deliberate, not drift.
		//
		// issue #4226: status/v7 documents the CompactEscalationEvidence contract
		// on terminal escalation. Deliberate, not drift.
		"schemas/status-v7.schema.json":         "c06a75193efb7f14c7c3ab50956f442190ed4579c01f2dec1341d4157ba22a99",
		"schemas/capabilities-v2.5.schema.json": "eca86e85fede1db7327edbe052bc13c6eb23318bbdd3a7858e0b90934cbe31ef",
	}
	for name, expected := range want {
		payload, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(payload)
		if actual := hex.EncodeToString(digest[:]); actual != expected {
			t.Fatalf("%s digest = %s, want %s", name, actual, expected)
		}
	}
}

// TestManagedAssetsContinuationSchemaRuleIsExercised is the RED-first proof
// for the corroborated review finding on #3299/#4170: failure.schema.json's
// if/then rule tying `continuation` to `code: managed_assets_outdated` (and
// forbidding it on every other code), and status-v9.schema.json's dedicated
// oneOf branch for the same reason code, were published without ever
// validating a real produced envelope against either rule in either
// direction. It exercises both published schemas against three shapes each:
// the real envelope a stale managed-asset digest produces (must pass), the
// same envelope with `continuation` stripped (must fail the rule), and the
// same envelope with `continuation` attached to an unrelated code/reason
// (must fail the rule).
func TestManagedAssetsContinuationSchemaRuleIsExercised(t *testing.T) {
	home, repo := reviewEnabledHome(t), initReviewCLIRepo(t)
	writeReviewStartCandidate(t, repo, "docs/schema-rule.md", "# Candidate\n", 0o644)
	staleManagedReviewerAssets(t, home)

	// --- failure.schema.json, exercised against the real START preflight failure ---
	var startOutput bytes.Buffer
	if err := RunReview(boundNegotiatedStartArgs(t, []string{
		"start", "--contract", ReviewIntegrationContractV2, "--cwd", repo, "--agent", "claude-code", "--consent", "granted",
	}), &startOutput); err == nil {
		t.Fatalf("stale managed assets START succeeded: %s", startOutput.String())
	}
	failureSchema := compileWholePublishedReviewSchema(t, "v2", "failure.schema.json")

	var failureDoc map[string]any
	if err := json.Unmarshal(startOutput.Bytes(), &failureDoc); err != nil {
		t.Fatal(err)
	}
	if failureDoc["code"] != "managed_assets_outdated" || failureDoc["continuation"] == nil {
		t.Fatalf("baseline stale managed assets failure = %#v", failureDoc)
	}

	// (a) the real envelope validates.
	validatePublishedReviewSchema(t, failureSchema, startOutput.Bytes())

	// (b) the same envelope with continuation removed must fail: the code
	// still claims managed_assets_outdated, and the rule requires it.
	withoutContinuation := decodeJSONObjectCopy(t, startOutput.Bytes())
	delete(withoutContinuation, "continuation")
	if err := failureSchema.Validate(withoutContinuation); err == nil {
		t.Fatal("failure.schema.json accepted a managed_assets_outdated code with no continuation")
	}

	// (c) the same continuation attached to an unrelated code must fail: the
	// rule's "else" branch forbids continuation on every other code.
	unrelatedCode := decodeJSONObjectCopy(t, startOutput.Bytes())
	unrelatedCode["code"] = "invalid_request"
	if err := failureSchema.Validate(unrelatedCode); err == nil {
		t.Fatal("failure.schema.json accepted a continuation on an unrelated failure code")
	}
	for _, command := range []string{
		"'/tmp/brunnr\ncode' sync --agent codex",
		"\"C:\\\\brunnr\r-code.exe\" sync --agent codex",
	} {
		multilineCommand := decodeJSONObjectCopy(t, startOutput.Bytes())
		multilineContinuation := multilineCommand["continuation"].(map[string]any)
		multilineContinuation["command"] = command
		if err := failureSchema.Validate(multilineCommand); err == nil {
			t.Fatalf("failure.schema.json accepted multiline continuation command %q", command)
		}
	}

	// --- status-v9.schema.json, exercised against the real STATUS stop transition ---
	var stopOutput bytes.Buffer
	if err := RunReview([]string{
		"status", "--cwd", repo, "--contract", ReviewIntegrationContractV2, "--agent", "claude-code", "--next-transition",
	}, &stopOutput); err != nil {
		t.Fatalf("stale managed assets STATUS: %v\n%s", err, stopOutput.String())
	}
	statusV9Schema := compileWholeNativeStatusSchema(t, "status-v9.schema.json")

	statusDoc := decodeJSONObjectCopy(t, stopOutput.Bytes())
	nextTransition, ok := statusDoc["next_transition"].(map[string]any)
	if !ok || nextTransition["kind"] != "stop" || nextTransition["reason_code"] != "managed_assets_outdated" || nextTransition["continuation"] == nil {
		t.Fatalf("baseline stale managed assets STATUS next_transition = %#v", statusDoc["next_transition"])
	}

	// (a) the real envelope validates.
	validatePublishedReviewSchema(t, statusV9Schema, stopOutput.Bytes())

	// (b) the same stop with continuation removed must fail: nothing else in
	// the oneOf admits a managed_assets_outdated stop without it.
	withoutStopContinuation := decodeJSONObjectCopy(t, stopOutput.Bytes())
	withoutTransition := decodeJSONObjectCopy(t, stopOutput.Bytes())["next_transition"].(map[string]any)
	delete(withoutTransition, "continuation")
	withoutStopContinuation["next_transition"] = withoutTransition
	if err := statusV9Schema.Validate(withoutStopContinuation); err == nil {
		t.Fatal("status-v9.schema.json accepted a managed_assets_outdated stop with no continuation")
	}

	// (c) the same continuation attached to an unrelated reason code (still a
	// stop, e.g. rdd_disabled) must fail both oneOf branches: the generic one
	// forbids the extra continuation property, and the dedicated one requires
	// reason_code const managed_assets_outdated.
	unrelatedReason := decodeJSONObjectCopy(t, stopOutput.Bytes())
	unrelatedTransition := decodeJSONObjectCopy(t, stopOutput.Bytes())["next_transition"].(map[string]any)
	unrelatedTransition["reason_code"] = "rdd_disabled"
	unrelatedReason["next_transition"] = unrelatedTransition
	if err := statusV9Schema.Validate(unrelatedReason); err == nil {
		t.Fatal("status-v9.schema.json accepted a continuation attached to an unrelated stop reason code")
	}
	for _, command := range []string{
		"'/tmp/brunnr\ncode' sync --agent codex",
		"\"C:\\\\brunnr\r-code.exe\" sync --agent codex",
	} {
		multilineCommand := decodeJSONObjectCopy(t, stopOutput.Bytes())
		multilineTransition := multilineCommand["next_transition"].(map[string]any)
		multilineContinuation := multilineTransition["continuation"].(map[string]any)
		multilineContinuation["command"] = command
		if err := statusV9Schema.Validate(multilineCommand); err == nil {
			t.Fatalf("status-v9.schema.json accepted multiline continuation command %q", command)
		}
	}
}

// decodeJSONObjectCopy decodes payload into a fresh map[string]any, so a
// caller can mutate one field without aliasing any other test's copy of the
// same bytes.
func decodeJSONObjectCopy(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func TestReviewProviderArtifactSchemasAreStrictAndBound(t *testing.T) {
	root := filepath.Join("..", "..", "contracts", "review-integration", "v1", "schemas")
	tests := []struct {
		name string
		id   string
	}{
		{name: "artifact-subject.schema.json", id: "https://github.com/carsimax/brunnr-code/contracts/review-integration/v1/schemas/artifact-subject.schema.json"},
		{name: "admitted-result.schema.json", id: "https://github.com/carsimax/brunnr-code/contracts/review-integration/v1/schemas/admitted-result.schema.json"},
		{name: "correction-plan-request.schema.json", id: reviewtransaction.CorrectionPlanRequestSchemaID},
		{name: "result-artifact-v2.schema.json", id: "https://github.com/carsimax/brunnr-code/contracts/review-integration/v1/schemas/result-artifact-v2.schema.json"},
		{name: "start-v2.schema.json", id: ReviewIntegrationStartSchemaIDV2},
		{name: "status-v2.schema.json", id: ReviewIntegrationStatusSchemaIDV2},
		{name: "transition-execution.schema.json", id: "https://github.com/carsimax/brunnr-code/contracts/review-integration/v1/schemas/transition-execution.schema.json"},
		{name: "authority-repair-assessment.schema.json", id: reviewtransaction.AuthorityRepairAssessmentSchemaID},
		{name: "repair.schema.json", id: ReviewIntegrationRepairSchemaID},
	}
	documents := make(map[string]map[string]any, len(tests))
	for _, tt := range tests {
		payload, err := os.ReadFile(filepath.Join(root, tt.name))
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(payload, &schema); err != nil {
			t.Fatal(err)
		}
		if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" || schema["$id"] != tt.id || schema["additionalProperties"] != false {
			t.Fatalf("%s header = %#v", tt.name, schema)
		}
		documents[tt.name] = schema
	}

	artifact := documents["result-artifact-v2.schema.json"]
	artifactRequired := schemaStringArray(t, artifact["required"])
	for _, field := range []string{"subject_hash", "admission_decision"} {
		if !slices.Contains(artifactRequired, field) {
			t.Fatalf("result artifact v2 omits required %q: %v", field, artifactRequired)
		}
	}
	if artifact["oneOf"] == nil {
		t.Fatal("result artifact v2 does not require exactly one provider-owned locator")
	}

	start := documents["start-v2.schema.json"]
	if !slices.Contains(schemaStringArray(t, start["required"]), "artifact_subjects") {
		t.Fatal("START v2 does not require provider-owned artifact subjects")
	}
	riskCodes := start["$defs"].(map[string]any)["risk_reason"].(map[string]any)["properties"].(map[string]any)["code"].(map[string]any)["enum"]
	codes := schemaStringArray(t, riskCodes)
	for _, code := range []string{string(reviewtransaction.RiskReasonProcessBoundary), string(reviewtransaction.RiskReasonProcessScanLimit)} {
		if !slices.Contains(codes, code) {
			t.Fatalf("START v2 rejects runtime risk reason %q: %v", code, codes)
		}
	}
	startStates := schemaStringArray(t, start["properties"].(map[string]any)["state"].(map[string]any)["enum"])
	for _, state := range []string{string(reviewtransaction.StateCorrectionRequired), string(reviewtransaction.StateValidating)} {
		if !slices.Contains(startStates, state) {
			t.Fatalf("START v2 rejects valid compact state %q: %v", state, startStates)
		}
	}

	status := documents["status-v2.schema.json"]
	transitionExecution := documents["transition-execution.schema.json"]
	transitionArtifact := transitionExecution["$defs"].(map[string]any)["transition_artifact"].(map[string]any)
	transitionRequired := schemaStringArray(t, transitionArtifact["required"])
	for _, field := range []string{"subject_hash", "admission_decision"} {
		if !slices.Contains(transitionRequired, field) {
			t.Fatalf("status v2 transition artifact omits %q: %v", field, transitionRequired)
		}
	}
	properties := transitionArtifact["properties"].(map[string]any)
	if properties["schema"].(map[string]any)["const"] != reviewResultArtifactSchema ||
		properties["admission_decision"].(map[string]any)["const"] != string(reviewtransaction.ArtifactAdmissionCompleted) {
		t.Fatalf("status v2 artifact identity = %#v", properties)
	}
	transitionInput := status["$defs"].(map[string]any)["transition_input"].(map[string]any)
	inputRules := transitionInput["allOf"].([]any)
	captureRule := inputRules[1].(map[string]any)
	captureThen := captureRule["then"].(map[string]any)
	for _, field := range []string{"artifact_subject", "candidate_diff", "changed_path_manifest"} {
		if !slices.Contains(schemaStringArray(t, captureThen["required"]), field) {
			t.Fatalf("legacy status v2 capture input omits required frozen context %q: %#v", field, captureThen)
		}
	}
	inputProperties := transitionInput["properties"].(map[string]any)
	if inputProperties["artifact_subject"].(map[string]any)["$ref"] != "artifact-subject.schema.json" ||
		inputProperties["candidate_diff"] == nil || inputProperties["base_tree"] != nil || inputProperties["candidate_tree"] != nil ||
		inputProperties["changed_path_manifest"].(map[string]any)["type"] != "array" {
		t.Fatalf("legacy status v2 capture input frozen context = %#v", inputProperties)
	}

	v2Root := filepath.Join("..", "..", "contracts", "review-integration", "v2", "schemas")
	v2Schemas := []struct {
		name string
		id   string
	}{
		{name: "artifact-subject.schema.json", id: "https://github.com/carsimax/brunnr-code/contracts/review-integration/v2/schemas/artifact-subject.schema.json"},
		{name: "admitted-result.schema.json", id: "https://github.com/carsimax/brunnr-code/contracts/review-integration/v2/schemas/admitted-result.schema.json"},
		{name: "start.schema.json", id: ReviewIntegrationStartSchemaIDV3},
		{name: "start-v4.schema.json", id: ReviewIntegrationStartSchemaIDV4},
		{name: "status.schema.json", id: ReviewIntegrationStatusSchemaIDV3},
		{name: "status-v4.schema.json", id: ReviewIntegrationStatusSchemaIDV4},
		{name: "status-v5.schema.json", id: ReviewIntegrationStatusSchemaIDV5},
		{name: "status-v6.schema.json", id: ReviewIntegrationStatusSchemaIDV6},
		{name: "status-v7.schema.json", id: ReviewIntegrationStatusSchemaIDV7},
		{name: "status-v8.schema.json", id: ReviewIntegrationStatusSchemaIDV8},
		{name: "status-v9.schema.json", id: ReviewIntegrationStatusSchemaIDV9},
		{name: "capabilities.schema.json", id: ReviewIntegrationCapabilitiesSchemaIDV2},
		{name: "capabilities-v2.1.schema.json", id: ReviewIntegrationCapabilitiesSchemaIDV21},
		{name: "capabilities-v2.2.schema.json", id: ReviewIntegrationCapabilitiesSchemaIDV22},
		{name: "capabilities-v2.3.schema.json", id: ReviewIntegrationCapabilitiesSchemaIDV23},
		{name: "capabilities-v2.4.schema.json", id: ReviewIntegrationCapabilitiesSchemaIDV24},
		{name: "capabilities-v2.5.schema.json", id: ReviewIntegrationCapabilitiesSchemaIDV25},
		{name: "capabilities-v2.6.schema.json", id: ReviewIntegrationCapabilitiesSchemaIDV26},
		{name: "intended-untracked-selection.schema.json", id: "https://github.com/carsimax/brunnr-code/contracts/review-integration/v2/schemas/intended-untracked-selection.schema.json"},
		{name: "consent.schema.json", id: ReviewIntegrationConsentSchemaIDV2},
		{name: "consent-v3.schema.json", id: ReviewIntegrationConsentSchemaIDV3},
		{name: "failure.schema.json", id: ReviewIntegrationFailureSchemaIDV2},
		{name: "operation.schema.json", id: ReviewIntegrationOperationSchemaIDV2},
		{name: "repair.schema.json", id: ReviewIntegrationRepairSchemaIDV2},
		{name: "gate-result.schema.json", id: "https://github.com/carsimax/brunnr-code/contracts/review-integration/v2/schemas/gate-result.schema.json"},
		{name: "last-event-closure.schema.json", id: "https://github.com/carsimax/brunnr-code/contracts/review-integration/v2/schemas/last-event-closure.schema.json"},
		{name: "opencode-provider-role.schema.json", id: "https://github.com/carsimax/brunnr-code/contracts/review-integration/v2/schemas/opencode-provider-role.schema.json"},
	}
	v2Documents := make(map[string]map[string]any, len(v2Schemas))
	for _, tt := range v2Schemas {
		payload, err := os.ReadFile(filepath.Join(v2Root, tt.name))
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(payload, &schema); err != nil {
			t.Fatal(err)
		}
		if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" || schema["$id"] != tt.id || schema["additionalProperties"] != false {
			t.Fatalf("v2 %s header = %#v", tt.name, schema)
		}
		v2Documents[tt.name] = schema
	}
	v2Input := v2Documents["status.schema.json"]["$defs"].(map[string]any)["transition_input"].(map[string]any)
	v2CaptureThen := v2Input["allOf"].([]any)[1].(map[string]any)["then"].(map[string]any)
	for _, field := range []string{"artifact_subject", "base_tree", "candidate_tree", "changed_path_manifest"} {
		if !slices.Contains(schemaStringArray(t, v2CaptureThen["required"]), field) {
			t.Fatalf("native Git status capture input omits %q: %#v", field, v2CaptureThen)
		}
	}
	v2Properties := v2Input["properties"].(map[string]any)
	if v2Properties["candidate_diff"] != nil || v2Properties["base_tree"] == nil || v2Properties["candidate_tree"] == nil {
		t.Fatalf("native Git status capture input = %#v", v2Properties)
	}
}

func TestReviewProviderArtifactV2FixturesValidate(t *testing.T) {
	root := filepath.Join("..", "..", "contracts", "review-integration", "v2", "fixtures")
	startPayload, err := os.ReadFile(filepath.Join(root, "start.fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var start ReviewIntegrationStartResult
	if err := json.Unmarshal(startPayload, &start); err != nil {
		t.Fatal(err)
	}
	if err := start.Validate(); err != nil {
		t.Fatalf("v2 START fixture: %v", err)
	}
	startV4Payload, err := os.ReadFile(filepath.Join(root, "start-v4.fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var startV4 ReviewIntegrationStartResult
	if err := json.Unmarshal(startV4Payload, &startV4); err != nil {
		t.Fatal(err)
	}
	if err := startV4.Validate(); err != nil {
		t.Fatalf("v4 START fixture: %v", err)
	}
	if startV4.NextTransition == nil || startV4.NextTransition.Execute == nil ||
		startV4.NextTransition.Execute.Operation != "review.status" {
		t.Fatalf("v4 START fixture continuation = %#v", startV4.NextTransition)
	}
	statusPayload, err := os.ReadFile(filepath.Join(root, "status.fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var status ReviewTargetStatusResult
	if err := json.Unmarshal(statusPayload, &status); err != nil {
		t.Fatal(err)
	}
	if err := status.Validate(); err != nil {
		t.Fatalf("v2 STATUS fixture: %v", err)
	}
	v5StatusPayload, err := os.ReadFile(filepath.Join(root, "status-v5.fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v5Status ReviewTargetStatusResult
	if err := json.Unmarshal(v5StatusPayload, &v5Status); err != nil {
		t.Fatal(err)
	}
	if err := v5Status.Validate(); err != nil {
		t.Fatalf("v5 STATUS fixture: %v", err)
	}
	consentPayload, err := os.ReadFile(filepath.Join(root, "consent.fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var consent ReviewIntegrationConsentResult
	if err := json.Unmarshal(consentPayload, &consent); err != nil {
		t.Fatal(err)
	}
	if err := consent.Validate(); err != nil {
		t.Fatalf("v2 consent fixture: %v", err)
	}
	consentV3Payload, err := os.ReadFile(filepath.Join(root, "consent-v3.fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var consentV3 ReviewIntegrationConsentResult
	if err := json.Unmarshal(consentV3Payload, &consentV3); err != nil {
		t.Fatal(err)
	}
	if err := consentV3.Validate(); err != nil || consentV3.Agent != "claude-code" {
		t.Fatalf("v2.1 consent fixture: %#v, %v", consentV3, err)
	}
}

func schemaStringArray(t *testing.T, value any) []string {
	t.Helper()
	values, ok := value.([]any)
	if !ok {
		t.Fatalf("schema value is not an array: %#v", value)
	}
	result := make([]string, len(values))
	for index, value := range values {
		text, ok := value.(string)
		if !ok {
			t.Fatalf("schema array value is not a string: %#v", value)
		}
		result[index] = text
	}
	return result
}
