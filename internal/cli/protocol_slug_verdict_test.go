package cli

import (
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
)

// TestComputeSlugSlimVerdictsFollowsIsSlim pins JD-017: the per-slug
// forwarding verdict uses AND (safest-wins) semantics across every adapter
// sharing a slug; with one adapter per slug the verdict is that adapter's.
func TestComputeSlugSlimVerdictsFollowsIsSlim(t *testing.T) {
	agentIDs := []model.AgentID{model.AgentClaudeCode}

	tests := []struct {
		name        string
		isSlim      func(model.AgentID) bool
		wantVerdict bool
	}{
		{
			name: "slim -> slim",
			isSlim: func(model.AgentID) bool {
				return true
			},
			wantVerdict: true,
		},
		{
			name: "full -> full",
			isSlim: func(model.AgentID) bool {
				return false
			},
			wantVerdict: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdicts := computeSlugSlimVerdicts(agentIDs, tt.isSlim)
			got, ok := verdicts["claude-code"]
			if !ok {
				t.Fatalf("computeSlugSlimVerdicts() missing verdict for slug %q", "claude-code")
			}
			if got != tt.wantVerdict {
				t.Fatalf("computeSlugSlimVerdicts() verdict = %v, want %v", got, tt.wantVerdict)
			}
		})
	}
}

// TestComputeSlugSlimVerdictsSkipsAgentsWithoutSetupSlug ensures agents
// without an engram setup slug are excluded from the verdict map entirely,
// matching the pre-extraction inline loop.
func TestComputeSlugSlimVerdictsSkipsAgentsWithoutSetupSlug(t *testing.T) {
	agentIDs := []model.AgentID{model.AgentID("unknown-runtime")}

	verdicts := computeSlugSlimVerdicts(agentIDs, func(model.AgentID) bool {
		return true
	})
	if len(verdicts) != 0 {
		t.Fatalf("computeSlugSlimVerdicts() = %v, want empty map for an agent with no setup slug", verdicts)
	}
}
