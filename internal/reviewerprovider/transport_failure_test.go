package reviewerprovider

import (
	"strings"
	"testing"
)

// A failing child may echo its whole prompt or transcript to stderr; the
// transport error keeps only the tail, where the failure is reported.
func TestReviewerTransportFailureDetailBoundsStderr(t *testing.T) {
	stderr := strings.Repeat("candidate source line\n", 10000) + "fatal: not logged in"
	detail := reviewerTransportFailureDetail(stderr, "")
	if len(detail) > reviewerTransportFailureStderrLimit {
		t.Fatalf("detail is %d bytes, want at most %d", len(detail), reviewerTransportFailureStderrLimit)
	}
	if !strings.HasSuffix(detail, "fatal: not logged in") {
		t.Fatalf("detail lost the stderr tail: %q", detail[len(detail)-40:])
	}
}
