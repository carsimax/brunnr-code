package installcmd

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/system"
)

func TestValidateGoForModuleInstall(t *testing.T) {
	tests := []struct {
		name        string
		profile     system.PlatformProfile
		lookPath    func(string) (string, error)
		goVersion   func() ([]byte, error)
		env         map[string]string
		wantErr     bool
		errContains string
	}{
		{
			name:    "go not in PATH returns error mentioning Go 1.24+",
			profile: system.PlatformProfile{OS: "linux", PackageManager: "apt"},
			lookPath: func(file string) (string, error) {
				return "", fmt.Errorf("not found")
			},
			goVersion:   func() ([]byte, error) { return nil, nil },
			env:         map[string]string{},
			wantErr:     true,
			errContains: "Go 1.24+",
		},
		{
			name:    "go version below 1.24 returns error",
			profile: system.PlatformProfile{OS: "linux", PackageManager: "apt"},
			lookPath: func(file string) (string, error) {
				return "/usr/bin/go", nil
			},
			goVersion:   func() ([]byte, error) { return []byte("go version go1.21.0 linux/amd64"), nil },
			env:         map[string]string{},
			wantErr:     true,
			errContains: "Go 1.24+",
		},
		{
			name:    "go version 1.23 returns error",
			profile: system.PlatformProfile{OS: "linux", PackageManager: "apt"},
			lookPath: func(file string) (string, error) {
				return "/usr/bin/go", nil
			},
			goVersion:   func() ([]byte, error) { return []byte("go version go1.23.5 linux/amd64"), nil },
			env:         map[string]string{},
			wantErr:     true,
			errContains: "Go 1.24+",
		},
		{
			name:    "GO111MODULE=off on linux returns error with export fix",
			profile: system.PlatformProfile{OS: "linux", PackageManager: "apt"},
			lookPath: func(file string) (string, error) {
				return "/usr/bin/go", nil
			},
			goVersion:   func() ([]byte, error) { return []byte("go version go1.24.0 linux/amd64"), nil },
			env:         map[string]string{"GO111MODULE": "off"},
			wantErr:     true,
			errContains: "export GO111MODULE=on",
		},
		{
			name:    "GO111MODULE=off on windows returns error with powershell fix",
			profile: system.PlatformProfile{OS: "windows", PackageManager: "winget"},
			lookPath: func(file string) (string, error) {
				return `C:\Go\bin\go.exe`, nil
			},
			goVersion:   func() ([]byte, error) { return []byte("go version go1.24.0 windows/amd64"), nil },
			env:         map[string]string{"GO111MODULE": "off"},
			wantErr:     true,
			errContains: "$env:GO111MODULE",
		},
		{
			name:    "go 1.24 without GO111MODULE=off succeeds",
			profile: system.PlatformProfile{OS: "linux", PackageManager: "apt"},
			lookPath: func(file string) (string, error) {
				return "/usr/bin/go", nil
			},
			goVersion: func() ([]byte, error) { return []byte("go version go1.24.0 linux/amd64"), nil },
			env:       map[string]string{},
			wantErr:   false,
		},
		{
			name:    "go 1.25 succeeds",
			profile: system.PlatformProfile{OS: "linux", PackageManager: "apt"},
			lookPath: func(file string) (string, error) {
				return "/usr/bin/go", nil
			},
			goVersion: func() ([]byte, error) { return []byte("go version go1.25.0 linux/amd64"), nil },
			env:       map[string]string{},
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origLookPath := cmdLookPath
			origGoVersion := cmdGoVersion
			origGetenv := osGetenv
			cmdLookPath = tt.lookPath
			cmdGoVersion = tt.goVersion
			osGetenv = func(key string) string { return tt.env[key] }
			t.Cleanup(func() {
				cmdLookPath = origLookPath
				cmdGoVersion = origGoVersion
				osGetenv = origGetenv
			})

			err := validateGoForModuleInstall(tt.profile)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateGoForModuleInstall() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.errContains)
			}
		})
	}
}

func TestResolveEngramBrewBypassesGoValidation(t *testing.T) {
	// On macOS, brew manages Go — validation must be skipped entirely.
	origLookPath := cmdLookPath
	cmdLookPath = func(file string) (string, error) {
		return "", fmt.Errorf("go not found")
	}
	t.Cleanup(func() { cmdLookPath = origLookPath })

	profile := system.PlatformProfile{OS: "darwin", PackageManager: "brew"}
	cmds, err := resolveEngramInstall(profile)
	if err != nil {
		t.Fatalf("resolveEngramInstall() unexpected error = %v", err)
	}
	if len(cmds) == 0 {
		t.Fatal("resolveEngramInstall() returned empty CommandSequence")
	}
}

func TestResolveDependencyInstall(t *testing.T) {
	r := NewResolver()

	tests := []struct {
		name    string
		profile system.PlatformProfile
		dep     string
		want    CommandSequence
		wantErr bool
	}{
		{
			name:    "darwin resolves brew command",
			profile: system.PlatformProfile{OS: "darwin", PackageManager: "brew"},
			dep:     "somepkg",
			want:    CommandSequence{{"brew", "install", "somepkg"}},
		},
		{
			name:    "ubuntu resolves apt command",
			profile: system.PlatformProfile{OS: "linux", LinuxDistro: system.LinuxDistroUbuntu, PackageManager: "apt"},
			dep:     "somepkg",
			want:    CommandSequence{{"sudo", "apt-get", "install", "-y", "somepkg"}},
		},
		{
			name:    "arch resolves pacman command",
			profile: system.PlatformProfile{OS: "linux", LinuxDistro: system.LinuxDistroArch, PackageManager: "pacman"},
			dep:     "somepkg",
			want:    CommandSequence{{"sudo", "pacman", "-S", "--noconfirm", "somepkg"}},
		},
		{
			name:    "fedora resolves dnf command",
			profile: system.PlatformProfile{OS: "linux", LinuxDistro: system.LinuxDistroFedora, PackageManager: "dnf"},
			dep:     "somepkg",
			want:    CommandSequence{{"sudo", "dnf", "install", "-y", "somepkg"}},
		},
		{
			name:    "silverblue resolves rpm-ostree command",
			profile: system.PlatformProfile{OS: "linux", LinuxDistro: system.LinuxDistroFedora, PackageManager: "rpm-ostree"},
			dep:     "somepkg",
			want:    CommandSequence{{"rpm-ostree", "install", "-y", "--apply-live", "somepkg"}},
		},
		{
			name:    "windows resolves winget command",
			profile: system.PlatformProfile{OS: "windows", PackageManager: "winget"},
			dep:     "somepkg",
			want:    CommandSequence{{"winget", "install", "--id", "somepkg", "-e", "--accept-source-agreements", "--accept-package-agreements"}},
		},
		{
			name:    "unsupported package manager returns error",
			profile: system.PlatformProfile{OS: "linux", LinuxDistro: system.LinuxDistroUbuntu, PackageManager: "zypper"},
			dep:     "somepkg",
			wantErr: true,
		},
		{
			name:    "empty dependency returns error",
			profile: system.PlatformProfile{OS: "darwin", PackageManager: "brew"},
			dep:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command, err := r.ResolveDependencyInstall(tt.profile, tt.dep)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveDependencyInstall() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr {
				return
			}

			if !reflect.DeepEqual(command, tt.want) {
				t.Fatalf("ResolveDependencyInstall() = %v, want %v", command, tt.want)
			}
		})
	}
}

func TestResolveAgentInstall(t *testing.T) {
	r := NewResolver()

	tests := []struct {
		name    string
		profile system.PlatformProfile
		agent   model.AgentID
		want    CommandSequence
		wantErr bool
	}{
		{
			name:    "claude-code on darwin uses npm without sudo",
			profile: system.PlatformProfile{OS: "darwin", PackageManager: "brew"},
			agent:   model.AgentClaudeCode,
			want:    CommandSequence{{"npm", "install", "-g", "--ignore-scripts", "@anthropic-ai/claude-code@latest"}},
		},
		{
			name:    "claude-code on linux system npm uses sudo",
			profile: system.PlatformProfile{OS: "linux", LinuxDistro: system.LinuxDistroUbuntu, PackageManager: "apt"},
			agent:   model.AgentClaudeCode,
			want:    CommandSequence{{"sudo", "npm", "install", "-g", "--ignore-scripts", "@anthropic-ai/claude-code@latest"}},
		},
		{
			name:    "claude-code on linux nvm skips sudo",
			profile: system.PlatformProfile{OS: "linux", LinuxDistro: system.LinuxDistroUbuntu, PackageManager: "apt", NpmWritable: true},
			agent:   model.AgentClaudeCode,
			want:    CommandSequence{{"npm", "install", "-g", "--ignore-scripts", "@anthropic-ai/claude-code@latest"}},
		},
		{
			name:    "claude-code on arch system npm uses sudo",
			profile: system.PlatformProfile{OS: "linux", LinuxDistro: system.LinuxDistroArch, PackageManager: "pacman"},
			agent:   model.AgentClaudeCode,
			want:    CommandSequence{{"sudo", "npm", "install", "-g", "--ignore-scripts", "@anthropic-ai/claude-code@latest"}},
		},
		{
			name:    "claude-code on fedora nvm skips sudo",
			profile: system.PlatformProfile{OS: "linux", LinuxDistro: system.LinuxDistroFedora, PackageManager: "dnf", NpmWritable: true},
			agent:   model.AgentClaudeCode,
			want:    CommandSequence{{"npm", "install", "-g", "--ignore-scripts", "@anthropic-ai/claude-code@latest"}},
		},
		{
			name:    "claude-code on windows uses npm without sudo",
			profile: system.PlatformProfile{OS: "windows", PackageManager: "winget", NpmWritable: true},
			agent:   model.AgentClaudeCode,
			want:    CommandSequence{{"npm", "install", "-g", "--ignore-scripts", "@anthropic-ai/claude-code@latest"}},
		},

		{
			name:    "unsupported agent returns error",
			profile: system.PlatformProfile{OS: "darwin", PackageManager: "brew"},
			agent:   "unsupported",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command, err := r.ResolveAgentInstall(tt.profile, tt.agent)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveAgentInstall() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr {
				return
			}

			if !reflect.DeepEqual(command, tt.want) {
				t.Fatalf("ResolveAgentInstall() = %v, want %v", command, tt.want)
			}
		})
	}
}

func TestResolveComponentInstall(t *testing.T) {
	// Keep the pre-trust command sequences deterministic on every host.
	original := cmdBrewHelpTrust
	cmdBrewHelpTrust = func(string) error { return fmt.Errorf("unknown command: trust") }
	t.Cleanup(func() { cmdBrewHelpTrust = original })
	r := NewResolver()

	tests := []struct {
		name      string
		profile   system.PlatformProfile
		component model.ComponentID
		want      CommandSequence
		wantErr   bool
	}{
		{
			name:      "engram on darwin uses brew tap and install",
			profile:   system.PlatformProfile{OS: "darwin", PackageManager: "brew"},
			component: model.ComponentEngram,
			want:      CommandSequence{{"brew", "tap", "Gentleman-Programming/homebrew-tap"}, {"brew", "install", "engram"}},
		},
		// Linux and Windows engram now use DownloadLatestBinary() — resolver returns error.
		// These cases are handled by run.go's componentApplyStep directly.
		{
			name:      "engram on ubuntu returns error (uses DownloadLatestBinary instead)",
			profile:   system.PlatformProfile{OS: "linux", LinuxDistro: system.LinuxDistroUbuntu, PackageManager: "apt"},
			component: model.ComponentEngram,
			wantErr:   true,
		},
		{
			name:      "engram on arch returns error (uses DownloadLatestBinary instead)",
			profile:   system.PlatformProfile{OS: "linux", LinuxDistro: system.LinuxDistroArch, PackageManager: "pacman"},
			component: model.ComponentEngram,
			wantErr:   true,
		},
		{
			name:      "engram on fedora returns error (uses DownloadLatestBinary instead)",
			profile:   system.PlatformProfile{OS: "linux", LinuxDistro: system.LinuxDistroFedora, PackageManager: "dnf"},
			component: model.ComponentEngram,
			wantErr:   true,
		},
		{
			name:      "engram on windows returns error (uses DownloadLatestBinary instead)",
			profile:   system.PlatformProfile{OS: "windows", PackageManager: "winget"},
			component: model.ComponentEngram,
			wantErr:   true,
		},
		{
			name:      "unsupported component returns error",
			profile:   system.PlatformProfile{OS: "darwin", PackageManager: "brew"},
			component: "unsupported",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command, err := r.ResolveComponentInstall(tt.profile, tt.component)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveComponentInstall() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr {
				return
			}

			if !reflect.DeepEqual(command, tt.want) {
				t.Fatalf("ResolveComponentInstall() = %v, want %v", command, tt.want)
			}
		})
	}
}

func TestPowerShellSingleQuotedValue(t *testing.T) {
	if got, want := system.PowerShellSingleQuoted(`C:\Users\O'Brien\Temp`), `C:\Users\O''Brien\Temp`; got != want {
		t.Fatalf("PowerShellSingleQuoted() = %q, want %q", got, want)
	}
}
