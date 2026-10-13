package system

import (
	"strings"
	"testing"
)

// TestDarwinDepsAreManual pins that macOS has no package manager to drive:
// every system dependency gets a manual hint and no install command.
func TestDarwinDepsAreManual(t *testing.T) {
	profile := PlatformProfile{OS: "darwin", Supported: true}
	wantHints := map[string]string{
		"git":  "xcode-select --install",
		"curl": "curl.se",
		"node": "https://nodejs.org",
		"go":   "https://go.dev/dl",
	}
	hints := map[string]string{
		"git":  installHintGit(profile),
		"curl": installHintCurl(profile),
		"node": installHintNode(profile),
		"go":   installHintGo(profile),
	}
	for dep, want := range wantHints {
		hint := hints[dep]
		if !strings.Contains(hint, want) {
			t.Errorf("%s darwin hint = %q, want it to contain %q", dep, hint, want)
		}
	}
}

func TestFormatMissingDepsMessageAllPresent(t *testing.T) {
	report := DependencyReport{AllPresent: true}
	msg := FormatMissingDepsMessage(report)
	if !strings.Contains(msg, "All required dependencies are present") {
		t.Fatalf("unexpected message for all present: %q", msg)
	}
}

func TestFormatMissingDepsMessageWithMissing(t *testing.T) {
	report := DependencyReport{
		Dependencies: []Dependency{
			{Name: "node", Required: true, Installed: false, InstallHint: "install node from https://nodejs.org/"},
		},
		AllPresent:      false,
		MissingRequired: []string{"node"},
	}

	msg := FormatMissingDepsMessage(report)
	if !strings.Contains(msg, "node") || !strings.Contains(msg, "install node from https://nodejs.org/") {
		t.Fatalf("missing deps message = %q", msg)
	}
}
