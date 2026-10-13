package system

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectFromInputsMarksSupportedMacOS(t *testing.T) {
	result := detectFromInputs("darwin", "arm64", "/bin/zsh", nil, nil)

	if !result.System.Supported || !result.System.Profile.Supported {
		t.Fatalf("expected darwin/arm64 to be supported, got system=%v profile=%v", result.System.Supported, result.System.Profile.Supported)
	}
	if result.System.OS != "darwin" || result.System.Profile.OS != "darwin" {
		t.Fatalf("expected OS darwin, got system=%q profile=%q", result.System.OS, result.System.Profile.OS)
	}
	if result.System.Arch != "arm64" {
		t.Fatalf("expected arch arm64, got %q", result.System.Arch)
	}
}

func TestDetectFromInputsMarksOtherPlatformsUnsupported(t *testing.T) {
	for _, tc := range []struct{ goos, arch string }{
		{"darwin", "amd64"},
		{"linux", "arm64"},
		{"windows", "amd64"},
	} {
		t.Run(tc.goos+"/"+tc.arch, func(t *testing.T) {
			result := detectFromInputs(tc.goos, tc.arch, "/bin/sh", nil, nil)
			if result.System.Supported || result.System.Profile.Supported {
				t.Fatalf("expected %s/%s to be unsupported", tc.goos, tc.arch)
			}
		})
	}
}

// TestGoAvailableInPlatformProfile asserts that GoAvailable in PlatformProfile
// reflects whether `go` is present in the tools map. This is the detection signal
// used by effectiveMethod to implement the go-install → binary auto-detect order.
func TestGoAvailableInPlatformProfile(t *testing.T) {
	tests := []struct {
		name        string
		tools       map[string]ToolStatus
		wantGoAvail bool
	}{
		{
			name:        "go in tools and installed → GoAvailable true",
			tools:       map[string]ToolStatus{"go": {Name: "go", Installed: true}},
			wantGoAvail: true,
		},
		{
			name:        "go in tools but not installed → GoAvailable false",
			tools:       map[string]ToolStatus{"go": {Name: "go", Installed: false}},
			wantGoAvail: false,
		},
		{
			name:        "go not in tools map → GoAvailable false",
			tools:       map[string]ToolStatus{"git": {Name: "git", Installed: true}},
			wantGoAvail: false,
		},
		{
			name:        "nil tools map → GoAvailable false",
			tools:       nil,
			wantGoAvail: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			profile := resolvePlatformProfile("darwin", "arm64", tc.tools)
			if profile.GoAvailable != tc.wantGoAvail {
				t.Fatalf("GoAvailable = %v, want %v", profile.GoAvailable, tc.wantGoAvail)
			}
		})
	}
}

func TestDetectFromInputsShellDefaultsToUnknown(t *testing.T) {
	result := detectFromInputs("darwin", "arm64", "", nil, nil)
	if result.System.Shell != "unknown" {
		t.Fatalf("Shell = %q, want %q", result.System.Shell, "unknown")
	}
}

func TestPathUnderHome(t *testing.T) {
	home := string(os.PathSeparator) + filepath.Join("home", "al")
	for path, want := range map[string]bool{
		home:                             true,
		filepath.Join(home, ".nvm", "x"): true,
		home + "ice" + string(os.PathSeparator) + "bin": false,
		filepath.Join("usr", "local"):                   false,
	} {
		if got := pathUnderHome(path, home); got != want {
			t.Errorf("pathUnderHome(%q) = %v, want %v", path, got, want)
		}
	}
}
