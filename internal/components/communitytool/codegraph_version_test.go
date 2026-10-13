package communitytool

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/model"
)

func TestParseCodeGraphVersion(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
		wantOK bool
	}{
		{name: "banner form printed by the upstream CLI", output: "┌  CodeGraph v0.9.3\n", want: "0.9.3", wantOK: true},
		{name: "bare semver", output: "1.4.1\n", want: "1.4.1", wantOK: true},
		{name: "v prefix", output: "v1.4.1", want: "1.4.1", wantOK: true},
		{name: "two component version", output: "codegraph 2.0", want: "2.0", wantOK: true},
		{name: "no version anywhere", output: "unknown flag: --version", want: "", wantOK: false},
		{name: "empty output", output: "", want: "", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseCodeGraphVersion(tt.output)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("parseCodeGraphVersion(%q) = (%q, %v), want (%q, %v)", tt.output, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestCodeGraphVersionLess(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{name: "the upstream floor is 1.6.2", a: codeGraphUpstreamVersion, b: "1.6.2", want: false},
		{name: "reported 0.9.3 predates the floor", a: "0.9.3", b: codeGraphUpstreamVersion, want: true},
		{name: "previous 1.4.1 contract predates the floor", a: "1.4.1", b: codeGraphUpstreamVersion, want: true},
		{name: "older patch predates the floor", a: "1.6.1", b: codeGraphUpstreamVersion, want: true},
		{name: "exact floor version is not older", a: "1.6.2", b: codeGraphUpstreamVersion, want: false},
		{name: "newer patch is not older", a: "1.6.3", b: codeGraphUpstreamVersion, want: false},
		{name: "newer major is not older", a: "2.0.0", b: codeGraphUpstreamVersion, want: false},
		{name: "minor compares numerically not lexicographically", a: "1.10.0", b: codeGraphUpstreamVersion, want: false},
		{name: "shorter version pads with zeroes", a: "1.6", b: codeGraphUpstreamVersion, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codeGraphVersionLess(tt.a, tt.b); got != tt.want {
				t.Fatalf("codeGraphVersionLess(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// installHomeWithTwoNativeTargets mirrors the reporter's machine: an already
// available CodeGraph CLI plus two detected native-target agents, one of which
// is not yet wired.
func installHomeWithTwoNativeTargets(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	mustWrite(t, filepath.Join(home, ".claude", "settings.json"), `{}`)
	mustWrite(t, filepath.Join(home, ".codex", "config.toml"), "")
	return home
}

// stubCodeGraphVersionSequence makes successive probes return the given
// versions in order; the last entry repeats. An empty string reads as an
// undeterminable version.
func stubCodeGraphVersionSequence(t *testing.T, versions ...string) *[]string {
	t.Helper()
	previous := codeGraphInstalledVersion
	probed := make([]string, 0, len(versions))
	codeGraphInstalledVersion = func(cliPath string) (string, bool) {
		probed = append(probed, cliPath)
		version := versions[len(versions)-1]
		if len(probed) <= len(versions) {
			version = versions[len(probed)-1]
		}
		return version, version != ""
	}
	t.Cleanup(func() { codeGraphInstalledVersion = previous })
	return &probed
}

// wireBothNativeTargets simulates a successful targeted `codegraph install`.
func wireBothNativeTargets(t *testing.T, home string) {
	t.Helper()
	mustWrite(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"codegraph":{"command":"codegraph","args":["serve","--mcp"]}}}`)
	mustWrite(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.codegraph]\ncommand = \"codegraph\"\nargs = [\"serve\", \"--mcp\"]\n")
}

func TestInstallEnforcesTheUpstreamFloorBeforeTargetedInstall(t *testing.T) {
	const targeted = "codegraph install --target claude,codex --location global --yes"
	tests := []struct {
		name         string
		versions     []string
		failUpgrade  bool
		wantCommands []string
		wantErr      []string
	}{
		{
			name:         "floor version installs targeted without upgrading",
			versions:     []string{"1.6.2"},
			wantCommands: []string{targeted},
		},
		{
			name:         "newer version installs targeted without upgrading",
			versions:     []string{"1.7.0"},
			wantCommands: []string{targeted},
		},
		{
			name:         "older version is upgraded then installed targeted",
			versions:     []string{"1.4.1", "1.6.2"},
			wantCommands: []string{"codegraph upgrade", targeted},
		},
		{
			name:         "undeterminable version is upgraded then installed targeted",
			versions:     []string{"", "1.6.2"},
			wantCommands: []string{"codegraph upgrade", targeted},
		},
		{
			name:         "failed upgrade stops before install",
			versions:     []string{"1.4.1"},
			failUpgrade:  true,
			wantCommands: []string{"codegraph upgrade"},
			wantErr:      []string{"1.4.1", "1.6.2", "codegraph upgrade", "@colbymchenry/codegraph@latest"},
		},
		{
			name:         "still older after upgrade stops before install",
			versions:     []string{"1.4.1", "1.5.0"},
			wantCommands: []string{"codegraph upgrade"},
			wantErr:      []string{"1.5.0", "1.6.2", "@colbymchenry/codegraph@latest"},
		},
		{
			name:         "still undeterminable after upgrade stops before install",
			versions:     []string{"", ""},
			wantCommands: []string{"codegraph upgrade"},
			wantErr:      []string{"1.6.2", "@colbymchenry/codegraph@latest"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := installHomeWithTwoNativeTargets(t)
			stubCodeGraphVersionSequence(t, tt.versions...)
			var ran []string
			result, err := InstallWithHome(model.CommunityToolCodeGraph, "/work/project", home, RunnerFunc(func(name string, args ...string) error {
				ran = append(ran, strings.Join(append([]string{name}, args...), " "))
				if len(args) > 0 && args[0] == "upgrade" {
					if tt.failUpgrade {
						return errors.New("network unreachable")
					}
					return nil
				}
				wireBothNativeTargets(t, home)
				return nil
			}), DetectorFunc(func(string) (string, error) { return "/bin/codegraph", nil }))

			if !reflect.DeepEqual(ran, tt.wantCommands) || !reflect.DeepEqual(result.CommandsRun, tt.wantCommands) {
				t.Fatalf("ran = %#v, CommandsRun = %#v, want %#v", ran, result.CommandsRun, tt.wantCommands)
			}
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("InstallWithHome() error = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("InstallWithHome() error = nil, want the floor violation surfaced")
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %q, want it to mention %q", err, want)
				}
			}
			if _, statErr := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(statErr) {
				t.Fatalf("~/.claude.json stat = %v, want no wiring after a floor failure", statErr)
			}
		})
	}
}

// An older CLI must be upgraded even when the agents already look wired:
// Brunnr owns upgrades, so a satisfied status must not hide CLI drift.
func TestInstallUpgradesAnOlderCLIEvenWhenAgentsAreAlreadyWired(t *testing.T) {
	home := installHomeWithTwoNativeTargets(t)
	wireBothNativeTargets(t, home)
	if _, err := InjectCodeGraphGuidance(home); err != nil {
		t.Fatal(err)
	}
	stubCodeGraphVersionSequence(t, "1.4.1", "1.6.2")

	result, err := InstallWithHome(model.CommunityToolCodeGraph, "/work/project", home, RunnerFunc(func(string, ...string) error {
		return nil
	}), DetectorFunc(func(string) (string, error) { return "/bin/codegraph", nil }))
	if err != nil {
		t.Fatalf("InstallWithHome() error = %v", err)
	}
	want := []string{"codegraph upgrade", "codegraph install --target claude,codex --location global --yes"}
	if !reflect.DeepEqual(result.CommandsRun, want) {
		t.Fatalf("CommandsRun = %#v, want %#v", result.CommandsRun, want)
	}
	if !hasManualActionContaining(result.ManualActions, "upgraded") {
		t.Fatalf("ManualActions = %#v, want the upgrade reported", result.ManualActions)
	}
}

func TestInstallProbesTheResolvedCLIPathNotABareName(t *testing.T) {
	home := installHomeWithTwoNativeTargets(t)
	probed := stubCodeGraphVersionSequence(t, codeGraphUpstreamVersion)

	if _, err := InstallWithHome(model.CommunityToolCodeGraph, "/work/project", home, RunnerFunc(func(string, ...string) error {
		wireBothNativeTargets(t, home)
		return nil
	}), DetectorFunc(func(string) (string, error) { return "/opt/tools/codegraph", nil })); err != nil {
		t.Fatalf("InstallWithHome() error = %v", err)
	}

	if !reflect.DeepEqual(*probed, []string{"/opt/tools/codegraph"}) {
		t.Fatalf("probed = %#v, want the detector-resolved CLI path", *probed)
	}
}

func TestInstallSkipsTheVersionProbeWhenTheCLIIsNotYetInstalled(t *testing.T) {
	home := installHomeWithTwoNativeTargets(t)
	probed := stubCodeGraphVersionSequence(t, "0.9.3")

	available := false
	if _, err := InstallWithHome(model.CommunityToolCodeGraph, "/work/project", home, RunnerFunc(func(string, ...string) error {
		available = true
		wireBothNativeTargets(t, home)
		return nil
	}), DetectorFunc(func(string) (string, error) {
		if !available {
			return "", errors.New("codegraph not found in PATH")
		}
		return "/bin/codegraph", nil
	})); err != nil {
		t.Fatalf("InstallWithHome() error = %v", err)
	}

	if len(*probed) != 0 {
		t.Fatalf("probes = %#v, want none: a freshly installed @latest CLI already meets the floor", *probed)
	}
}

func hasManualActionContaining(actions []string, needle string) bool {
	for _, action := range actions {
		if strings.Contains(action, needle) {
			return true
		}
	}
	return false
}
