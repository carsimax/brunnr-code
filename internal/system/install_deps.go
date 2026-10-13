package system

import "fmt"

// Install hints are manual: macOS has no managed package manager here.

func installHintGit(_ PlatformProfile) string {
	return "xcode-select --install"
}

func installHintCurl(_ PlatformProfile) string {
	return "curl ships with macOS; reinstall it from https://curl.se/"
}

func installHintNode(_ PlatformProfile) string {
	return "install node from https://nodejs.org/"
}

func installHintNpm(_ PlatformProfile) string {
	// npm comes with node.
	return "npm is included with node — install node first"
}

func installHintGo(_ PlatformProfile) string {
	return "install go from https://go.dev/dl/"
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
