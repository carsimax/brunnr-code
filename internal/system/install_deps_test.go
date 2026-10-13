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

func TestInstallHintGitUbuntu(t *testing.T) {
	profile := PlatformProfile{OS: "linux", PackageManager: "apt", LinuxDistro: "ubuntu"}
	hint := installHintGit(profile)
	if !strings.Contains(hint, "apt-get install") {
		t.Fatalf("installHintGit(ubuntu) = %q", hint)
	}
}

func TestInstallHintGitArch(t *testing.T) {
	profile := PlatformProfile{OS: "linux", PackageManager: "pacman", LinuxDistro: "arch"}
	hint := installHintGit(profile)
	if !strings.Contains(hint, "pacman -S") {
		t.Fatalf("installHintGit(arch) = %q", hint)
	}
}

func TestInstallHintGitSilverblue(t *testing.T) {
	profile := PlatformProfile{OS: "linux", PackageManager: "rpm-ostree", LinuxDistro: LinuxDistroFedora}
	hint := installHintGit(profile)
	if hint != "rpm-ostree install -y --apply-live git" {
		t.Fatalf("installHintGit(silverblue) = %q, want %q", hint, "rpm-ostree install -y --apply-live git")
	}
}

func TestInstallHintNodeUbuntu(t *testing.T) {
	profile := PlatformProfile{OS: "linux", PackageManager: "apt", LinuxDistro: "ubuntu"}
	hint := installHintNode(profile)
	if !strings.Contains(hint, "nodesource") {
		t.Fatalf("installHintNode(ubuntu) = %q, want NodeSource URL", hint)
	}
}

func TestInstallHintNodeArch(t *testing.T) {
	profile := PlatformProfile{OS: "linux", PackageManager: "pacman", LinuxDistro: "arch"}
	hint := installHintNode(profile)
	if !strings.Contains(hint, "pacman") || !strings.Contains(hint, "nodejs") {
		t.Fatalf("installHintNode(arch) = %q", hint)
	}
}

func TestInstallHintNodeFedora(t *testing.T) {
	profile := PlatformProfile{OS: "linux", PackageManager: "dnf", LinuxDistro: LinuxDistroFedora}
	hint := installHintNode(profile)
	if !strings.Contains(hint, "rpm.nodesource.com") || !strings.Contains(hint, "dnf install -y nodejs") {
		t.Fatalf("installHintNode(fedora) = %q, want NodeSource LTS setup + dnf install", hint)
	}
}

func TestInstallHintNodeSilverblue(t *testing.T) {
	profile := PlatformProfile{OS: "linux", PackageManager: "rpm-ostree", LinuxDistro: LinuxDistroFedora}
	hint := installHintNode(profile)
	if hint != "rpm-ostree install -y --apply-live nodejs npm" {
		t.Fatalf("installHintNode(silverblue) = %q, want %q", hint, "rpm-ostree install -y --apply-live nodejs npm")
	}
}

func TestInstallHintGoUbuntu(t *testing.T) {
	profile := PlatformProfile{OS: "linux", PackageManager: "apt", LinuxDistro: "ubuntu"}
	hint := installHintGo(profile)
	if !strings.Contains(hint, "apt-get install") {
		t.Fatalf("installHintGo(ubuntu) = %q", hint)
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
			{Name: "node", Required: true, Installed: false, InstallHint: "sudo apt-get install -y nodejs"},
		},
		AllPresent:      false,
		MissingRequired: []string{"node"},
	}

	msg := FormatMissingDepsMessage(report)
	if !strings.Contains(msg, "node") || !strings.Contains(msg, "sudo apt-get install -y nodejs") {
		t.Fatalf("missing deps message = %q", msg)
	}
}

func TestInstallHintGitWindows(t *testing.T) {
	profile := PlatformProfile{OS: "windows", PackageManager: "winget"}
	hint := installHintGit(profile)
	if hint != "winget install Git.Git" {
		t.Fatalf("installHintGit(windows) = %q", hint)
	}
}

func TestInstallHintNodeWindows(t *testing.T) {
	profile := PlatformProfile{OS: "windows", PackageManager: "winget"}
	hint := installHintNode(profile)
	if hint != "winget install OpenJS.NodeJS.LTS" {
		t.Fatalf("installHintNode(windows) = %q", hint)
	}
}

func TestInstallHintGoWindows(t *testing.T) {
	profile := PlatformProfile{OS: "windows", PackageManager: "winget"}
	hint := installHintGo(profile)
	if hint != "winget install GoLang.Go" {
		t.Fatalf("installHintGo(windows) = %q", hint)
	}
}

func TestInstallHintCurlWindows(t *testing.T) {
	profile := PlatformProfile{OS: "windows", PackageManager: "winget"}
	hint := installHintCurl(profile)
	if !strings.Contains(hint, "pre-installed") {
		t.Fatalf("installHintCurl(windows) = %q, want pre-installed message", hint)
	}
}
