package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/carsimax/brunnr-code/internal/reviewtransaction"
)

func TestReviewCapabilitiesV13AdvertisesProviderAdmissionAndRecovery(t *testing.T) {
	surface := reviewCapabilitiesStaticSurface()
	for _, want := range []ReviewCapabilityFeature{
		{Name: "opaque_repository_context", Supported: true, Requires: []string{"compact_v2_authority", "native_next_transition"}},
		{Name: "provider_artifact_admission", Supported: true, Requires: []string{"compact_v2_authority", "native_frozen_candidate_context", "opaque_repository_context"}},
		{Name: "provider_targeted_validation_request", Supported: true, Requires: []string{"compact_v2_authority", "native_next_transition"}},
		{Name: "recovered_correction_evidence", Supported: true, Requires: []string{"compact_v2_authority", "provider_targeted_validation_request"}},
		{Name: "validating_result_reopen", Supported: true, Requires: []string{"compact_v2_authority", "provider_artifact_admission"}},
	} {
		if !slices.ContainsFunc(surface.Features.Optional, func(got ReviewCapabilityFeature) bool {
			return got.Name == want.Name && got.Supported == want.Supported && slices.Equal(got.Requires, want.Requires)
		}) {
			t.Fatalf("v1.3 optional capabilities missing %#v: %#v", want, surface.Features.Optional)
		}
	}
	for _, schema := range []string{
		reviewtransaction.ArtifactSubjectSchemaV1,
		reviewtransaction.AdmittedReviewerResultSchemaV1,
		reviewtransaction.TargetedValidationRequestSchema,
	} {
		if !slices.Contains(surface.Schemas, schema) {
			t.Fatalf("v1.3 schemas do not advertise %q: %v", schema, surface.Schemas)
		}
	}
}

func TestReviewCapabilitiesV10ThroughV12ArtifactsRemainByteIdentical(t *testing.T) {
	root := filepath.Join("..", "..", "contracts", "review-integration", "v1")
	want := map[string]string{
		"fixtures/capabilities.fixture.json":      "c32c78126dfbecbc0cb4919fcfef1aeb31201833906460260f90dba50d49a29b",
		"fixtures/capabilities-v1.1.fixture.json": "9b19463a820ed1432672c313ba7d76fa063b8e45d892eb60ea48ebd9f46e0fca",
		"fixtures/capabilities-v1.2.fixture.json": "930bad2da2d1c36a14272994ed337347c632e5ca7068abc42c6d4aaa0d76bc76",
		"schemas/capabilities.schema.json":        "53e430124a4097fcff50774e609eb152588cc3f2e54a8b4f6c0234e53ea8f103",
		"schemas/capabilities-v1.1.schema.json":   "1771bb9fd6f1999ded63e6aa24e7b27bacf0289261dd71d109bbc755e9174b8b",
		"schemas/capabilities-v1.2.schema.json":   "9b62f627070114af1ad21c66b24f1ad440a7fe4560420268de0120c4c9931a99",
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
