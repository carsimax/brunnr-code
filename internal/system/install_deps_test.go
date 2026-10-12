package system

import (
	"slices"
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
		if cmds := InstallCommandsForDep(dep, profile); cmds != nil {
			t.Errorf("%s darwin commands = %v, want nil (manual install)", dep, cmds)
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

func TestInstallCommandsForDepGitUbuntu(t *testing.T) {
	profile := PlatformProfile{OS: "linux", PackageManager: "apt", LinuxDistro: "ubuntu"}
	cmds := InstallCommandsForDep("git", profile)
	if len(cmds) != 1 {
		t.Fatalf("git ubuntu commands = %d, want 1", len(cmds))
	}
	if cmds[0][0] != "sudo" {
		t.Fatalf("git ubuntu command = %v, want sudo", cmds[0])
	}
}

func TestInstallCommandsForDepNodeUbuntuHasTwoSteps(t *testing.T) {
	profile := PlatformProfile{OS: "linux", PackageManager: "apt", LinuxDistro: "ubuntu"}
	cmds := InstallCommandsForDep("node", profile)
	if len(cmds) != 2 {
		t.Fatalf("node ubuntu commands = %d, want 2 (nodesource setup + install)", len(cmds))
	}
}

func TestInstallCommandsForDepNodeFedoraHasTwoSteps(t *testing.T) {
	profile := PlatformProfile{OS: "linux", PackageManager: "dnf", LinuxDistro: LinuxDistroFedora}
	cmds := InstallCommandsForDep("node", profile)
	if len(cmds) != 2 {
		t.Fatalf("node fedora commands = %d, want 2 (nodesource setup + install)", len(cmds))
	}
	if cmds[0][0] != "bash" || !strings.Contains(cmds[0][2], "rpm.nodesource.com/setup_lts.x") {
		t.Fatalf("node fedora step 1 = %v, want nodesource setup", cmds[0])
	}
	if cmds[1][0] != "sudo" || cmds[1][1] != "dnf" || cmds[1][4] != "nodejs" {
		t.Fatalf("node fedora step 2 = %v, want sudo dnf install -y nodejs", cmds[1])
	}
}

func TestInstallCommandsForDepNpmReturnsNil(t *testing.T) {
	profile := PlatformProfile{OS: "darwin"}
	cmds := InstallCommandsForDep("npm", profile)
	if cmds != nil {
		t.Fatalf("npm commands = %v, want nil (comes with node)", cmds)
	}
}

func TestInstallCommandsForDepUnknownReturnsNil(t *testing.T) {
	profile := PlatformProfile{OS: "darwin"}
	cmds := InstallCommandsForDep("unknown_tool", profile)
	if cmds != nil {
		t.Fatalf("unknown tool commands = %v, want nil", cmds)
	}
}

func TestInstallCommandsForDepGitArchUsesPacman(t *testing.T) {
	profile := PlatformProfile{OS: "linux", PackageManager: "pacman", LinuxDistro: "arch"}
	cmds := InstallCommandsForDep("git", profile)
	if len(cmds) != 1 {
		t.Fatalf("git arch commands = %d, want 1", len(cmds))
	}
	if cmds[0][0] != "sudo" || cmds[0][1] != "pacman" {
		t.Fatalf("git arch command = %v, want sudo pacman", cmds[0])
	}
}

func TestInstallCommandsForDepGitFedoraUsesDnf(t *testing.T) {
	profile := PlatformProfile{OS: "linux", PackageManager: "dnf", LinuxDistro: LinuxDistroFedora}
	cmds := InstallCommandsForDep("git", profile)
	if len(cmds) != 1 {
		t.Fatalf("git fedora commands = %d, want 1", len(cmds))
	}
	if cmds[0][0] != "sudo" || cmds[0][1] != "dnf" {
		t.Fatalf("git fedora command = %v, want sudo dnf", cmds[0])
	}
}

func TestInstallCommandsForDepSilverblueUsesRpmOstree(t *testing.T) {
	profile := PlatformProfile{OS: "linux", PackageManager: "rpm-ostree", LinuxDistro: LinuxDistroFedora}
	tests := []struct {
		dep     string
		wantCmd []string
	}{
		{dep: "git", wantCmd: []string{"rpm-ostree", "install", "-y", "--apply-live", "git"}},
		{dep: "curl", wantCmd: []string{"rpm-ostree", "install", "-y", "--apply-live", "curl"}},
		{dep: "node", wantCmd: []string{"rpm-ostree", "install", "-y", "--apply-live", "nodejs", "npm"}},
		{dep: "go", wantCmd: []string{"rpm-ostree", "install", "-y", "--apply-live", "golang"}},
	}

	for _, tt := range tests {
		t.Run(tt.dep, func(t *testing.T) {
			cmds := InstallCommandsForDep(tt.dep, profile)
			if len(cmds) != 1 {
				t.Fatalf("commands for %q = %d, want 1", tt.dep, len(cmds))
			}
			if !slices.Equal(cmds[0], tt.wantCmd) {
				t.Fatalf("command for %q = %v, want %v", tt.dep, cmds[0], tt.wantCmd)
			}
		})
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

func TestInstallCommandsForDepGitWindows(t *testing.T) {
	profile := PlatformProfile{OS: "windows", PackageManager: "winget"}
	cmds := InstallCommandsForDep("git", profile)
	if len(cmds) != 1 {
		t.Fatalf("git windows commands = %d, want 1", len(cmds))
	}
	if cmds[0][0] != "winget" {
		t.Fatalf("git windows command = %v, want winget", cmds[0])
	}
}

func TestInstallCommandsForDepNodeWindows(t *testing.T) {
	profile := PlatformProfile{OS: "windows", PackageManager: "winget"}
	cmds := InstallCommandsForDep("node", profile)
	if len(cmds) != 1 {
		t.Fatalf("node windows commands = %d, want 1", len(cmds))
	}
	if cmds[0][0] != "winget" {
		t.Fatalf("node windows command = %v, want winget", cmds[0])
	}
}

func TestInstallCommandsForDepCurlWindowsReturnsNil(t *testing.T) {
	profile := PlatformProfile{OS: "windows", PackageManager: "winget"}
	cmds := InstallCommandsForDep("curl", profile)
	if cmds != nil {
		t.Fatalf("curl on windows = %v, want nil (pre-installed)", cmds)
	}
}

func TestInstallCommandsFullMatrix(t *testing.T) {
	profiles := []PlatformProfile{
		{OS: "linux", PackageManager: "apt", LinuxDistro: "ubuntu"},
		{OS: "linux", PackageManager: "pacman", LinuxDistro: "arch"},
		{OS: "linux", PackageManager: "dnf", LinuxDistro: LinuxDistroFedora},
		{OS: "linux", PackageManager: "rpm-ostree", LinuxDistro: LinuxDistroFedora},
	}

	deps := []string{"git", "curl", "node", "go"}

	for _, profile := range profiles {
		for _, dep := range deps {
			t.Run(profile.OS+"/"+profile.PackageManager+"/"+dep, func(t *testing.T) {
				cmds := InstallCommandsForDep(dep, profile)
				if cmds == nil {
					t.Fatalf("InstallCommandsForDep(%q, %s/%s) = nil", dep, profile.OS, profile.PackageManager)
				}
				if len(cmds) == 0 {
					t.Fatalf("InstallCommandsForDep(%q) returned empty slice", dep)
				}
				for _, cmd := range cmds {
					if len(cmd) == 0 {
						t.Fatalf("empty command in sequence for %q", dep)
					}
				}
			})
		}
	}
}
