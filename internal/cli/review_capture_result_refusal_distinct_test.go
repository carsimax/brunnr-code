package cli

import (
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
)

// TestCaptureResultHostMediatedRefusalsAreDistinguishable is #3441.
//
// `review capture-result --agent` refusals used to share one byte-identical
// sentence, so a report that quoted it could not be traced to its branch.
// Each refusal now names what the caller passed, the runtime's compiled
// transport, and the one accepted form.
func TestCaptureResultHostMediatedRefusalsAreDistinguishable(t *testing.T) {
	binding := []string{
		"--repository-context", "rctx1_" + strings.Repeat("0", 64),
		"--lineage", "distinct-host-mediated-refusals", "--target", "sha256:" + strings.Repeat("0", 64),
		"--expected-revision", "sha256:" + strings.Repeat("1", 64), "--lens", "review-reliability", "--order", "0",
	}

	for _, test := range []struct {
		name string
		argv []string
		// want is the complete refusal, asserted exactly: a substring match
		// is what let two opposite causes share one sentence unnoticed.
		want string
	}{
		{
			name: "raw input submitted with a runtime identity",
			argv: append(slices.Clone(binding), "--agent", string(model.AgentClaudeCode), "--input", "-"),
			want: `review capture-result --agent "claude-code" with --input is unavailable: ` +
				`a raw reviewer result is submitted without --agent; ` +
				`this runtime's compiled transport is "claude_prompt_carried"`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := RunReviewCaptureResult(test.argv, io.Discard)
			if err == nil {
				t.Fatal("want a refusal")
			}
			if err.Error() != test.want {
				t.Fatalf("refusal =\n\t%q\nwant\n\t%q", err.Error(), test.want)
			}
		})
	}
}
