package persona

import "path/filepath"

// managedOutputStyle is the Claude Code output style installed with the
// persona component.
var managedOutputStyle = struct {
	Name      string
	File      string
	AssetPath string
}{Name: "Principal Engineer", File: "principal-engineer.md", AssetPath: "claude/output-style-principal-engineer.md"}

// legacyOutputStyleFiles are managed output-style files written by earlier
// releases. Install and sync remove them so Claude Code stops offering a
// retired style, and uninstall removes them with the current one.
var legacyOutputStyleFiles = []string{"neutral.md"}

// OutputStylePath returns the managed output-style file inside an
// output-style directory, or "" when dir is empty. Install, sync, backup and
// verification all derive the path from here.
func OutputStylePath(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, managedOutputStyle.File)
}

// LegacyOutputStylePaths returns the retired managed output-style files inside
// an output-style directory, or nil when dir is empty. Every caller that
// removes them also declares them as backup targets so a rollback restores
// them.
func LegacyOutputStylePaths(dir string) []string {
	if dir == "" {
		return nil
	}
	paths := make([]string, 0, len(legacyOutputStyleFiles))
	for _, file := range legacyOutputStyleFiles {
		paths = append(paths, filepath.Join(dir, file))
	}
	return paths
}
