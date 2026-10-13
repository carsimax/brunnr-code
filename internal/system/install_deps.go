package system

import "fmt"

// installHintGit returns the platform-specific install hint for git.
func installHintGit(profile PlatformProfile) string {
	switch {
	case profile.OS == "darwin":
		return "xcode-select --install"
	case profile.OS == "windows":
		return "winget install Git.Git"
	case profile.PackageManager == "apt":
		return "sudo apt-get install -y git"
	case profile.PackageManager == "pacman":
		return "sudo pacman -S --noconfirm git"
	case profile.PackageManager == "dnf":
		return "sudo dnf install -y git"
	case profile.PackageManager == "rpm-ostree":
		return "rpm-ostree install -y --apply-live git"
	default:
		return "install git from https://git-scm.com/"
	}
}

// installHintCurl returns the platform-specific install hint for curl.
func installHintCurl(profile PlatformProfile) string {
	switch {
	case profile.OS == "darwin":
		return "curl ships with macOS; reinstall it from https://curl.se/"
	case profile.OS == "windows":
		return "curl is pre-installed on Windows 10+"
	case profile.PackageManager == "apt":
		return "sudo apt-get install -y curl"
	case profile.PackageManager == "pacman":
		return "sudo pacman -S --noconfirm curl"
	case profile.PackageManager == "dnf":
		return "sudo dnf install -y curl"
	case profile.PackageManager == "rpm-ostree":
		return "rpm-ostree install -y --apply-live curl"
	default:
		return "install curl from https://curl.se/"
	}
}

// installHintNode returns the platform-specific install hint for Node.js.
func installHintNode(profile PlatformProfile) string {
	switch {
	case profile.OS == "darwin":
		return "install node from https://nodejs.org/"
	case profile.OS == "windows":
		return "winget install OpenJS.NodeJS.LTS"
	case profile.PackageManager == "apt":
		return "curl -fsSL https://deb.nodesource.com/setup_lts.x | sudo -E bash - && sudo apt-get install -y nodejs"
	case profile.PackageManager == "pacman":
		return "sudo pacman -S --noconfirm nodejs npm"
	case profile.PackageManager == "dnf":
		return "curl -fsSL https://rpm.nodesource.com/setup_lts.x | sudo bash - && sudo dnf install -y nodejs"
	case profile.PackageManager == "rpm-ostree":
		return "rpm-ostree install -y --apply-live nodejs npm"
	default:
		return "install node from https://nodejs.org/"
	}
}

// installHintNpm returns the platform-specific install hint for npm.
func installHintNpm(_ PlatformProfile) string {
	// npm comes with node on all platforms.
	return "npm is included with node — install node first"
}

// installHintGo returns the platform-specific install hint for Go.
func installHintGo(profile PlatformProfile) string {
	switch {
	case profile.OS == "darwin":
		return "install go from https://go.dev/dl/"
	case profile.OS == "windows":
		return "winget install GoLang.Go"
	case profile.PackageManager == "apt":
		return "sudo apt-get install -y golang"
	case profile.PackageManager == "pacman":
		return "sudo pacman -S --noconfirm go"
	case profile.PackageManager == "dnf":
		return "sudo dnf install -y golang"
	case profile.PackageManager == "rpm-ostree":
		return "rpm-ostree install -y --apply-live golang"
	default:
		return "install go from https://go.dev/dl/"
	}
}

// FormatMissingDepsMessage creates a human-readable message about missing dependencies.
func FormatMissingDepsMessage(report DependencyReport) string {
	if report.AllPresent {
		return "All required dependencies are present."
	}

	msg := fmt.Sprintf("Missing %d required dependency(ies): %s\n",
		len(report.MissingRequired),
		joinStrings(report.MissingRequired))

	msg += "\nInstall hints:\n"
	for _, dep := range report.Dependencies {
		if !dep.Installed && dep.Required {
			msg += fmt.Sprintf("  %s: %s\n", dep.Name, dep.InstallHint)
		}
	}

	return msg
}

func joinStrings(values []string) string {
	if len(values) == 0 {
		return "none"
	}

	result := values[0]
	for i := 1; i < len(values); i++ {
		result += ", " + values[i]
	}

	return result
}
