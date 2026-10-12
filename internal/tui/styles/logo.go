package styles

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// logoLines contains the ASCII "BRUNNR-CODE" wordmark.
var logoLines = []string{
	" ___  ___  _   _  _  _  _  _  ___        ___   ___   ___   ___",
	"| _ )| _ \\| | | || \\| || \\| || _ \\ ___  / __| / _ \\ |   \\ | __|",
	"| _ \\|   /| |_| || .` || .` ||   /|___|| (__ | (_) || |) || _|",
	"|___/|_|_\\ \\___/ |_|\\_||_|\\_||_|_\\      \\___| \\___/ |___/ |___|",
}

// gradientColors defines the top-to-bottom gradient for the logo.
// Distributed across rows: mauve → lavender → blue → teal → green.
var gradientColors = []lipgloss.Color{
	ColorMauve,    // band 1
	ColorLavender, // band 2
	ColorBlue,     // band 3
	ColorTeal,     // band 4
	ColorGreen,    // band 5
}

// RenderLogo returns the ASCII logo with a top-to-bottom gradient.
func RenderLogo() string {
	total := len(logoLines)
	if total == 0 {
		return ""
	}

	bands := len(gradientColors)
	var b strings.Builder

	for i, line := range logoLines {
		bandIdx := (i * bands) / total
		if bandIdx >= bands {
			bandIdx = bands - 1
		}
		style := lipgloss.NewStyle().Foreground(gradientColors[bandIdx])
		b.WriteString(style.Render(line))
		if i < total-1 {
			b.WriteByte('\n')
		}
	}

	return b.String()
}
