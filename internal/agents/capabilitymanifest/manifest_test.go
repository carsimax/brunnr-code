package capabilitymanifest

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/catalog"
	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
)

func TestCanonicalImplementationRoutingBoundaries(t *testing.T) {
	t.Parallel()

	got := CanonicalImplementationRouting()
	want := ImplementationRoutingFacts{
		DirectInline: DirectInlineFacts{
			MaxEvidenceBatches:                       1,
			MaxEvidenceCalls:                         3,
			ApproxEvidenceTokens:                     10000,
			EvidenceMustUseBoundedRanges:             true,
			MaxMechanicalWriteFiles:                  1,
			MechanicalWriteMustBeAlreadyUnderstood:   true,
			MechanicalWriteMustNotRequireResearch:    true,
			MechanicalWriteMustNotHaveOpenDesignWork: true,
		},
		DelegatedDirect: DelegatedDirectFacts{
			ApproxSequentialLookupLimit:    5,
			DelegateWhenLongSessionMapping: true,
			ApproxHandoffTokens:            2000,
			MaxParentSpotChecks:            1,
			ApproxParentContextTokens:      150000,
			ContextBackstopIsAdvisory:      true,
			WriterMinNonTrivialFiles:       2,
			DelegateWhenReadPreparesWrite:  true,
			DelegateWhenBroadResearch:      true,
		},
		SDD: SDDProposalFacts{
			ProposeWhenSubstantialOrAmbiguous:     true,
			DurableArtifactsMustReduceUncertainty: true,
			SelectionPolicy:                       SDDSelectionExplicitRequestOrAcceptedProposal,
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CanonicalImplementationRouting() = %#v, want %#v", got, want)
	}
}

func TestManifestRejectsWeakenedRoutingFacts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		weaken func(*AgentCapabilityManifest)
	}{
		{
			name: "legacy minimum file count is rejected",
			weaken: func(manifest *AgentCapabilityManifest) {
				manifest.ImplementationRouting.DirectInline.MinUnderstandingFiles = 1
			},
		},
		{
			name: "legacy maximum file count is rejected",
			weaken: func(manifest *AgentCapabilityManifest) {
				manifest.ImplementationRouting.DirectInline.MaxUnderstandingFiles = 4
			},
		},
		{
			name: "legacy mapping file count is rejected",
			weaken: func(manifest *AgentCapabilityManifest) {
				manifest.ImplementationRouting.DelegatedDirect.MappingMinUnderstandingFiles = 5
			},
		},
		{
			name:   "multiple inline batches",
			weaken: func(m *AgentCapabilityManifest) { m.ImplementationRouting.DirectInline.MaxEvidenceBatches = 2 },
		},
		{
			name:   "four inline calls",
			weaken: func(m *AgentCapabilityManifest) { m.ImplementationRouting.DirectInline.MaxEvidenceCalls = 4 },
		},
		{
			name:   "larger inline evidence",
			weaken: func(m *AgentCapabilityManifest) { m.ImplementationRouting.DirectInline.ApproxEvidenceTokens = 20000 },
		},
		{
			name: "unbounded reads",
			weaken: func(m *AgentCapabilityManifest) {
				m.ImplementationRouting.DirectInline.EvidenceMustUseBoundedRanges = false
			},
		},
		{
			name: "longer sequential exploration",
			weaken: func(m *AgentCapabilityManifest) {
				m.ImplementationRouting.DelegatedDirect.ApproxSequentialLookupLimit = 6
			},
		},
		{
			name: "long-session mapping stays inline",
			weaken: func(m *AgentCapabilityManifest) {
				m.ImplementationRouting.DelegatedDirect.DelegateWhenLongSessionMapping = false
			},
		},
		{
			name:   "larger mapper handoff",
			weaken: func(m *AgentCapabilityManifest) { m.ImplementationRouting.DelegatedDirect.ApproxHandoffTokens = 10000 },
		},
		{
			name:   "parent repeats mapped evidence",
			weaken: func(m *AgentCapabilityManifest) { m.ImplementationRouting.DelegatedDirect.MaxParentSpotChecks = 2 },
		},
		{
			name: "later context backstop",
			weaken: func(m *AgentCapabilityManifest) {
				m.ImplementationRouting.DelegatedDirect.ApproxParentContextTokens = 200000
			},
		},
		{
			name: "claims mechanical context enforcement",
			weaken: func(m *AgentCapabilityManifest) {
				m.ImplementationRouting.DelegatedDirect.ContextBackstopIsAdvisory = false
			},
		},
		{
			name:   "multiple mechanical write files",
			weaken: func(m *AgentCapabilityManifest) { m.ImplementationRouting.DirectInline.MaxMechanicalWriteFiles = 2 },
		},
		{
			name: "mechanical write not already understood",
			weaken: func(m *AgentCapabilityManifest) {
				m.ImplementationRouting.DirectInline.MechanicalWriteMustBeAlreadyUnderstood = false
			},
		},
		{
			name: "mechanical write requires research",
			weaken: func(m *AgentCapabilityManifest) {
				m.ImplementationRouting.DirectInline.MechanicalWriteMustNotRequireResearch = false
			},
		},
		{
			name: "mechanical write has open design work",
			weaken: func(m *AgentCapabilityManifest) {
				m.ImplementationRouting.DirectInline.MechanicalWriteMustNotHaveOpenDesignWork = false
			},
		},
		{
			name: "writer starts after two non-trivial files",
			weaken: func(manifest *AgentCapabilityManifest) {
				manifest.ImplementationRouting.DelegatedDirect.WriterMinNonTrivialFiles = 3
			},
		},
		{
			name: "read preparing write no longer delegates",
			weaken: func(manifest *AgentCapabilityManifest) {
				manifest.ImplementationRouting.DelegatedDirect.DelegateWhenReadPreparesWrite = false
			},
		},
		{
			name: "broad research no longer delegates",
			weaken: func(manifest *AgentCapabilityManifest) {
				manifest.ImplementationRouting.DelegatedDirect.DelegateWhenBroadResearch = false
			},
		},
		{
			name: "substantial ambiguity no longer proposes SDD",
			weaken: func(manifest *AgentCapabilityManifest) {
				manifest.ImplementationRouting.SDD.ProposeWhenSubstantialOrAmbiguous = false
			},
		},
		{
			name: "SDD proposal need not reduce durable uncertainty",
			weaken: func(manifest *AgentCapabilityManifest) {
				manifest.ImplementationRouting.SDD.DurableArtifactsMustReduceUncertainty = false
			},
		},
		{
			name: "SDD selection bypasses explicit consent",
			weaken: func(manifest *AgentCapabilityManifest) {
				manifest.ImplementationRouting.SDD.SelectionPolicy = "automatic"
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			manifest := MustForAgent(model.AgentClaudeCode)
			test.weaken(&manifest)
			if err := manifest.Validate(); err == nil {
				t.Fatal("Validate() = nil, want non-canonical routing rejection")
			}
		})
	}
}

func TestEveryManifestKeepsWorkRoutingDormantAndHashesCanonically(t *testing.T) {
	t.Parallel()

	const wantRoutingDigest = "sha256:1c343ca2792be730e02b35f1655a19884a2672dc0d2915ef2afb33b6ac01bc1c"
	// Digests pin the three providers with an enforceable fresh-reviewer
	// boundary: Claude Code's generated reviewer has no live tools, OpenCode
	// relays one ordinary task through Go-owned admission, and Codex's
	// provider subprocess reaches the same contract.
	wantManifestDigests := map[model.AgentID]string{
		model.AgentClaudeCode: "sha256:0644de1b6539cffee24ed3d673b450bf1f460fe5db7e8f05c5a0911aace8b280",
		model.AgentCodex:      "sha256:b47855dc0acdae65aa2215eba09135087d32a890814dc73baab13595ecb6602b",
		model.AgentOpenCode:   "sha256:c4889e14ddd1f82160d8d5bb49bcdf29bdac5fe367218adf6458a80bc9d5af9d",
	}

	for agent, wantDigest := range wantManifestDigests {
		agent := agent
		wantDigest := wantDigest
		t.Run(string(agent), func(t *testing.T) {
			t.Parallel()

			manifest := MustForAgent(agent)
			if err := manifest.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if manifest.Contracts.WorkRoutingV1.Exposure != ContractExposureDormant {
				t.Fatalf("work-routing exposure = %q, want %q", manifest.Contracts.WorkRoutingV1.Exposure, ContractExposureDormant)
			}
			if manifest.Advertises(ContractWorkRoutingV1) {
				t.Fatal("work-routing must remain unadvertised before final activation")
			}
			wantImmutableExecutor := agent == model.AgentClaudeCode || agent == model.AgentOpenCode || agent == model.AgentCodex
			if got := manifest.Advertises(ContractImmutableReviewExecutorV1); got != wantImmutableExecutor {
				t.Fatalf("immutable reviewer execution advertised = %t, want %t", got, wantImmutableExecutor)
			}
			wantExposure := ContractExposureDormant
			if wantImmutableExecutor {
				wantExposure = ContractExposureAdvertised
			}
			if got := manifest.Contracts.ImmutableReviewExecutorV1.Exposure; got != wantExposure {
				t.Fatalf("immutable reviewer execution exposure = %q, want %q", got, wantExposure)
			}

			payload, err := manifest.CanonicalJSON()
			if err != nil {
				t.Fatalf("CanonicalJSON() error = %v", err)
			}
			// Keep the v1 capability envelope and adapter claims, but never
			// serialize the retired file-count policy as active routing facts.
			for _, retired := range []string{"minUnderstandingFiles", "maxUnderstandingFiles", "mappingMinUnderstandingFiles"} {
				if strings.Contains(string(payload), retired) {
					t.Errorf("canonical JSON retains %s", retired)
				}
			}
			if manifest.SchemaVersion != SchemaV1 {
				t.Error("routing update changed capability schema")
			}
			var roundTrip AgentCapabilityManifest
			if err := json.Unmarshal(payload, &roundTrip); err != nil {
				t.Fatalf("Unmarshal(CanonicalJSON()) error = %v", err)
			}
			if roundTrip != manifest {
				t.Fatalf("canonical JSON round trip = %#v, want %#v", roundTrip, manifest)
			}

			gotDigest, err := roundTrip.Digest()
			if err != nil {
				t.Fatalf("Digest() error = %v", err)
			}
			if gotDigest != wantDigest {
				t.Fatalf("Digest() = %q, want %q", gotDigest, wantDigest)
			}

			gotRoutingDigest, err := manifest.RoutingDigest()
			if err != nil {
				t.Fatalf("RoutingDigest() error = %v", err)
			}
			if gotRoutingDigest != wantRoutingDigest {
				t.Fatalf("RoutingDigest() = %q, want %q", gotRoutingDigest, wantRoutingDigest)
			}
		})
	}
}

// TestEveryManifestDigestStaysByteStable pins every row at the
// evidence-budget routing baseline without changing review transport claims.
func TestEveryManifestDigestStaysByteStable(t *testing.T) {
	t.Parallel()

	wantDigests := map[model.AgentID]string{
		model.AgentClaudeCode: "sha256:0644de1b6539cffee24ed3d673b450bf1f460fe5db7e8f05c5a0911aace8b280",
		model.AgentCodex:      "sha256:b47855dc0acdae65aa2215eba09135087d32a890814dc73baab13595ecb6602b",
		model.AgentOpenCode:   "sha256:c4889e14ddd1f82160d8d5bb49bcdf29bdac5fe367218adf6458a80bc9d5af9d",
	}

	agents := make([]model.AgentID, 0, len(wantDigests))
	for agent := range wantDigests {
		agents = append(agents, agent)
	}

	if got := len(agents); got != 3 {
		t.Fatalf("want 3 agents, got %d", got)
	}

	for _, agent := range agents {
		agent := agent
		wantDigest := wantDigests[agent]
		t.Run(string(agent), func(t *testing.T) {
			t.Parallel()

			manifest := MustForAgent(agent)
			gotDigest, err := manifest.Digest()
			if err != nil {
				t.Fatalf("Digest() error = %v", err)
			}
			if gotDigest != wantDigest {
				t.Fatalf("Digest() = %q, want %q (byte-stable contract)", gotDigest, wantDigest)
			}
		})
	}
}

func TestReviewTransportAdvertisementIsClosedCatalogSet(t *testing.T) {
	const wantExposed = 3

	exposed := 0
	for _, agent := range catalog.AllAgents() {
		t.Run(string(agent.ID), func(t *testing.T) {
			manifest := MustForAgent(agent.ID)
			want := agent.ID == model.AgentClaudeCode ||
				agent.ID == model.AgentOpenCode ||
				agent.ID == model.AgentCodex
			if got := manifest.Advertises(ContractReviewTransportV1); got != want {
				t.Fatalf("review transport advertised = %t, want %t", got, want)
			}
			if want {
				exposed++
			}
		})
	}
	if exposed != wantExposed {
		t.Fatalf("advertised review transport runtimes = %d, want %d", exposed, wantExposed)
	}
}

func TestForAgentRejectsUnknownAgent(t *testing.T) {
	t.Parallel()

	_, err := ForAgent(model.AgentID("unknown"))
	if !errors.Is(err, ErrUnsupportedAgent) {
		t.Fatalf("ForAgent() error = %v, want ErrUnsupportedAgent", err)
	}
}
