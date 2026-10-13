package system

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type SystemInfo struct {
	OS        string
	Arch      string
	Shell     string
	Supported bool
	Profile   PlatformProfile
}

type PlatformProfile struct {
	OS          string
	NpmWritable bool // true when npm global prefix is user-writable (nvm/fnm/volta)
	GoAvailable bool // true when `go` is found on PATH (used for auto-detect: go-install → binary)
	Supported   bool
}

// detectedToolNames is the fixed probe list passed to DetectTools.
func detectedToolNames() []string {
	return []string{"git", "curl", "node", "go"}
}

type DetectionResult struct {
	System       SystemInfo
	Tools        map[string]ToolStatus
	Configs      []ConfigState
	Dependencies DependencyReport
}

func Detect(ctx context.Context) (DetectionResult, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return DetectionResult{}, err
	}

	tools := DetectTools(ctx, detectedToolNames())
	configs := ScanConfigs(homeDir)

	result := detectFromInputs(runtime.GOOS, runtime.GOARCH, os.Getenv("SHELL"), tools, configs)
	// A caller that supplies Engram skips the npm prefix probe: NpmWritable
	// only shapes install advice for npm-based agents that is never executed.
	if !callerSuppliesEngram() {
		result.System.Profile.NpmWritable = detectNpmWritable(homeDir)
	}
	result.Dependencies = DetectDependencies(ctx, result.System.Profile)

	return result, nil
}

// detectNpmWritable checks if npm's global prefix is under the user's home
// directory (nvm, fnm, volta, etc.), meaning sudo is not needed for global installs.
func detectNpmWritable(homeDir string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := runProbe(exec.CommandContext(ctx, "npm", "config", "get", "prefix"))
	if err != nil {
		return false
	}
	return pathUnderHome(strings.TrimSpace(string(out)), homeDir)
}

// pathUnderHome matches whole path segments so /home/al does not claim /home/alice.
func pathUnderHome(path, homeDir string) bool {
	return path == homeDir || strings.HasPrefix(path, strings.TrimRight(homeDir, string(os.PathSeparator))+string(os.PathSeparator))
}

func detectFromInputs(goos, arch, shell string, tools map[string]ToolStatus, configs []ConfigState) DetectionResult {
	if shell == "" {
		shell = "unknown"
	}

	profile := resolvePlatformProfile(goos, arch, tools)

	return DetectionResult{
		System: SystemInfo{
			OS:        goos,
			Arch:      arch,
			Shell:     shell,
			Supported: profile.Supported,
			Profile:   profile,
		},
		Tools:   tools,
		Configs: configs,
	}
}

func resolvePlatformProfile(goos, arch string, tools map[string]ToolStatus) PlatformProfile {
	profile := PlatformProfile{OS: goos, Supported: IsSupportedPlatform(goos, arch)}

	// Detect Go availability for the go-install → binary auto-detect order.
	if go_, ok := tools["go"]; ok && go_.Installed {
		profile.GoAvailable = true
	}

	return profile
}
