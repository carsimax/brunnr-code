package update

// UpdateStatus represents the outcome of a single tool version check.
type UpdateStatus string

const (
	UpToDate        UpdateStatus = "up-to-date"
	UpdateAvailable UpdateStatus = "update-available"
	NotInstalled    UpdateStatus = "not-installed"
	VersionUnknown  UpdateStatus = "version-unknown"
	CheckFailed     UpdateStatus = "check-failed"
	// DevBuild is used when the installed version is the sentinel "dev" string,
	// indicating a source-built binary. Such builds are not auto-targeted for upgrade.
	DevBuild UpdateStatus = "dev-build"
)

// InstallMethod describes how a managed tool is installed on the current platform.
// Used by the upgrade executor to choose the correct upgrade strategy.
type InstallMethod string

const (
	InstallBinary InstallMethod = "binary"
)

// ToolInfo describes a managed tool that can be checked for updates.
type ToolInfo struct {
	Name              string        // human-readable name (e.g., "engram")
	Owner             string        // GitHub repository owner
	Repo              string        // GitHub repository name
	DetectCmd         []string      // command to detect installed version
	VersionPrefix     string        // prefix to strip from version output (e.g., "v")
	ReleaseTagPattern string        // optional regexp for selecting the correct GitHub release channel
	InstallMethod     InstallMethod // how this tool is installed (used by upgrade executor)

	// FallbackPaths returns a list of absolute paths to check when exec.LookPath
	// fails. This covers a running process PATH that is stale after install
	// (the install dir is added for new shells only). When a path
	// is found on disk, detectInstalledVersion runs the detect command using that
	// full path rather than the bare binary name.
	//
	// The function receives the user home directory. May be nil when no
	// fallback is needed.
	FallbackPaths func(homeDir string) []string
}

// UpdateResult holds the result of checking a single tool for updates.
type UpdateResult struct {
	Tool             ToolInfo
	InstalledVersion string
	LatestVersion    string
	Status           UpdateStatus
	ReleaseURL       string
	UpdateHint       string
	Err              error
}
