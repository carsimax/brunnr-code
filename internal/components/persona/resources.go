package persona

import "path/filepath"

// neutralOutputStyle is the managed Claude Code output style installed with
// the Neutral persona.
var neutralOutputStyle = struct {
	Name      string
	File      string
	AssetPath string
}{Name: "Neutral", File: "neutral.md", AssetPath: "claude/output-style-neutral.md"}

// OutputStylePath returns the managed output-style file inside an
// output-style directory, or "" when dir is empty. Install, sync, backup and
// verification all derive the path from here.
func OutputStylePath(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, neutralOutputStyle.File)
}
