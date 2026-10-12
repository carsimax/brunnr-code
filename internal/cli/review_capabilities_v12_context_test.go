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
		"fixtures/capabilities.fixture.json":      "b3ca822189a236f2d891628c665ca23e308bf5185a1701e1f07231bd970461bb",
		"fixtures/capabilities-v1.1.fixture.json": "f81797013153089dda7c40fdfab359b6db6f62611ec0cfe9daee8804a76c6f6d",
		"fixtures/capabilities-v1.2.fixture.json": "4d39e7bcc96cdf5aa7fb2202fc8fedcc5afde5d96e98cc10f6f8acaf13b77ab9",
		"schemas/capabilities.schema.json":        "ad333177494a251beac153f74bd751fa77126a9968aad69e64fc2abf15cff0f7",
		"schemas/capabilities-v1.1.schema.json":   "7ddcace2a7b724c92a0ad07be44bbd22428a8d4c5272f23be643c9838ac418c7",
		"schemas/capabilities-v1.2.schema.json":   "73ca4ef714e093644cc04a8229b6246775c881fd221a2121ea33f4691ba271a7",
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
