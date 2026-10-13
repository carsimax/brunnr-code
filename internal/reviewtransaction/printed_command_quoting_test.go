package reviewtransaction

import (
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/pathquote"
)

// A printed command is meant to be pasted into a shell, where "$(...)" inside
// double quotes still runs. The repository path must be one literal word.
func TestPrintedAbandonCommandQuotesRepositoryAsShellWord(t *testing.T) {
	repo := "/work/$(touch pwned)/repo"
	text := compactAbandonCommandText(repo, "lineage", CompactAbandonEligibility{Revision: "rev"})
	if want := "--cwd " + pathquote.ShellWord(repo) + " "; !strings.Contains(text, want) {
		t.Fatalf("abandon command does not render the repository as a shell word; want %q in:\n%s", want, text)
	}
}
