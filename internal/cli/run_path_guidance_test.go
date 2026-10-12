package cli

import (
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/verify"
)

func TestEngramPathGuidanceFish(t *testing.T) {
	msg := engramPathGuidance("/home/u/.local/bin", "/usr/bin/fish")
	if want := "set -Ux fish_user_paths /home/u/.local/bin"; !strings.Contains(msg, want) {
		t.Fatalf("engramPathGuidance(fish) missing %q: %s", want, msg)
	}
}

func TestEngramPathGuidanceZsh(t *testing.T) {
	msg := engramPathGuidance("/home/u/.local/bin", "/bin/zsh")
	if want := `export PATH="/home/u/.local/bin:$PATH"' >> ~/.zshrc`; !strings.Contains(msg, want) {
		t.Fatalf("engramPathGuidance(zsh) missing %q: %s", want, msg)
	}
}

func TestEngramPathGuidanceDefault(t *testing.T) {
	msg := engramPathGuidance("/home/u/.local/bin", "")
	if want := "Add /home/u/.local/bin to your shell PATH"; !strings.Contains(msg, want) {
		t.Fatalf("engramPathGuidance(default) missing %q: %s", want, msg)
	}
}

func TestIsInPATH(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/home/user/.local/bin")
	if !isInPATH("/home/user/.local/bin") {
		t.Fatal("isInPATH should return true for entry in PATH")
	}
	if isInPATH("/not/in/path") {
		t.Fatal("isInPATH should return false for entry not in PATH")
	}
}

func TestWithEngramPathNoteNamesDownloadDir(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	report := verify.Report{Ready: true, FinalNote: "You're ready."}

	updated := withEngramPathNote(report, "/home/u/.local/bin")

	if strings.Contains(updated.FinalNote, "go install") {
		t.Fatalf("FinalNote must not claim a go install, got: %q", updated.FinalNote)
	}
	if want := "The engram binary was installed to /home/u/.local/bin, which is not on your PATH."; !strings.Contains(updated.FinalNote, want) {
		t.Fatalf("FinalNote missing %q, got: %q", want, updated.FinalNote)
	}
	if want := `export PATH="/home/u/.local/bin:$PATH"`; !strings.Contains(updated.FinalNote, want) {
		t.Fatalf("FinalNote missing PATH guidance %q, got: %q", want, updated.FinalNote)
	}
}

func TestWithEngramPathNoteSkipsWithoutOffPathDownload(t *testing.T) {
	report := verify.Report{Ready: true, FinalNote: "You're ready."}

	updated := withEngramPathNote(report, "")

	if updated.FinalNote != report.FinalNote {
		t.Fatalf("FinalNote should be unchanged, got: %q", updated.FinalNote)
	}
}
