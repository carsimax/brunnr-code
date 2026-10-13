package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/state"

	"github.com/carsimax/brunnr-code/internal/reviewtransaction"
)

// TestCloneScopeDisableReportsHowFarItReached is #3284 at the surface the
// operator reads.
//
// The clone-scope switch is machine state: a modern brunnr-code publishes it
// under the relocated switch root, and every brunnr-code installed before that
// relocation reads its own location. The disable used to report plain success
// either way, so an operator who had turned reviews off kept being blocked by
// another build on the same machine with nothing in the output to explain it.
func TestCloneScopeDisableReportsHowFarItReached(t *testing.T) {
	reviewModeHome(t)
	repo := initReviewCLIRepo(t)

	var output bytes.Buffer
	if err := RunReviewMode([]string{"disable", "--cwd", repo, "--scope", "clone", "--json"}, &output); err != nil {
		t.Fatalf("clone-scoped disable failed: %v", err)
	}
	if result := decodeReviewModeResult(t, output.Bytes()); result.Status.Reach != reviewtransaction.RDDModeReachMachine {
		t.Fatalf("disable reported reach %q, want %q", result.Status.Reach, reviewtransaction.RDDModeReachMachine)
	}
}

// TestCloneScopeDisableSaysWhenItReachedOnlyThisBuild keeps #2882's exit open
// and stops it lying at the same time. A damaged review authority tree may not
// make the documented clone-scoped disable refuse, and it may not let the
// command report a machine-wide switch it did not publish machine-wide.
func TestCloneScopeDisableSaysWhenItReachedOnlyThisBuild(t *testing.T) {
	reviewModeHome(t)
	repo := initReviewCLIRepo(t)
	damageReviewAuthorityTree(t, repo)

	var output bytes.Buffer
	if err := RunReviewMode([]string{"disable", "--cwd", repo, "--scope", "clone", "--json"}, &output); err != nil {
		t.Fatalf("clone-scoped disable failed while authority was damaged: %v", err)
	}
	result := decodeReviewModeResult(t, output.Bytes())
	if result.Status.Effective != reviewtransaction.RDDModeOff {
		t.Fatalf("disable reported %#v, want off", result.Status)
	}
	if result.Status.Reach != reviewtransaction.RDDModeReachThisBuild {
		t.Fatalf("disable reported reach %q, want %q", result.Status.Reach, reviewtransaction.RDDModeReachThisBuild)
	}

	output.Reset()
	if err := RunReviewMode([]string{"status", "--cwd", repo}, &output); err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if strings.Contains(output.String(), "note:") {
		t.Fatalf("read-only status invented a reach it never verified:\n%s", output.String())
	}

	output.Reset()
	if err := RunReviewMode([]string{"disable", "--cwd", repo, "--scope", "clone"}, &output); err != nil {
		t.Fatalf("repeated clone-scoped disable failed: %v", err)
	}
	if !strings.Contains(output.String(), "note:") ||
		!strings.Contains(output.String(), "applied for this brunnr-code only") {
		t.Fatalf("the human-readable disable claimed a machine-wide switch it did not publish:\n%s", output.String())
	}
}

// A corrupt state.json must not take the kill switch with it: a clone-scoped
// disable still works, and every other verb names the file to repair.
func TestReviewModeSurvivesCorruptGlobalState(t *testing.T) {
	home := reviewModeHome(t)
	repo := initReviewCLIRepo(t)
	statePath := state.Path(home)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := RunReviewMode([]string{"disable", "--cwd", repo, "--scope", "clone", "--json"}, &output); err != nil {
		t.Fatalf("clone-scoped disable with a corrupt state.json failed: %v", err)
	}
	if result := decodeReviewModeResult(t, output.Bytes()); result.Status.Effective != reviewtransaction.RDDModeOff {
		t.Fatalf("clone-scoped disable effective = %q, want off", result.Status.Effective)
	}

	err := RunReviewMode([]string{"status", "--cwd", repo}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), statePath) {
		t.Fatalf("status with a corrupt state.json = %v, want an error naming %s", err, statePath)
	}
}
