package installcmd

import (
	"reflect"
	"testing"

	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/system"
)

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
			name:    "darwin without a package manager returns error",
			profile: system.PlatformProfile{OS: "darwin"},
			dep:     "somepkg",
			wantErr: true,
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
			profile: system.PlatformProfile{OS: "darwin"},
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
			profile: system.PlatformProfile{OS: "darwin"},
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
			profile: system.PlatformProfile{OS: "darwin"},
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
	r := NewResolver()

	tests := []struct {
		name      string
		profile   system.PlatformProfile
		component model.ComponentID
		want      CommandSequence
		wantErr   bool
	}{
		// Engram uses DownloadLatestBinary() on every platform — resolver returns error.
		{
			name:      "engram on darwin returns error (uses DownloadLatestBinary instead)",
			profile:   system.PlatformProfile{OS: "darwin"},
			component: model.ComponentEngram,
			wantErr:   true,
		},
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
			profile:   system.PlatformProfile{OS: "darwin"},
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
