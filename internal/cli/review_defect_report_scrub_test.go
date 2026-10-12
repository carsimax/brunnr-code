package cli

import "testing"

// The field-level privacy gate keeps the product's own public identifiers
// (contract and schema ids) and still redacts
// everything that describes a machine: absolute and $HOME-rooted paths,
// file URLs, emails, and every other environment assignment (#3443).
func TestReviewScrubDefectReportFieldKeepsPublicIdentifiersAndRedactsTheRest(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"contract id survives", "run gentle-ai review status --contract gentle-ai.review-integration/v2 --agent codex", "run gentle-ai review status --contract gentle-ai.review-integration/v2 --agent codex"},
		{"schema id survives", "schema gentle-ai.review-integration.status/v7 required", "schema gentle-ai.review-integration.status/v7 required"},
		{"other assignments still redact", "export SOME_RELAY_CONTRACT=gentle-ai.review-relay/v9 and GITHUB_TOKEN=abc", "export <redacted> and <redacted>"},
		{"absolute path still redacts", "repo at /Users/alan/work/gentle-ai failed", "repo at <redacted> failed"},
		{"home-rooted path still redacts", "read ~/.codex/config.toml first", "read ~<redacted> first"},
		{"file url still redacts", "see file:///Users/alan/x", "see fil<redacted>"},
		{"windows path still redacts", `path C:\Users\alan\repo\x.go missing`, "path <redacted> missing"},
		{"email still redacts", "by alan@example.com", "by <redacted>"},
		{"identifier next to a path keeps only the identifier", "gentle-ai.review-integration/v2 at /tmp/x", "gentle-ai.review-integration/v2 at <redacted>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reviewScrubDefectReportField(tc.in); got != tc.want {
				t.Fatalf("reviewScrubDefectReportField(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}
