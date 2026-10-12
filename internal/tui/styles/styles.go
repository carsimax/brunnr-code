package styles

import "github.com/charmbracelet/lipgloss"

// The TUI uses only the terminal's base ANSI colors so it inherits each
// user's terminal theme. Plain text keeps the terminal default foreground.
var (
	ColorAccent  = lipgloss.Color("4")
	ColorSuccess = lipgloss.Color("2")
	ColorWarning = lipgloss.Color("3")
	ColorError   = lipgloss.Color("1")
)

// Cursor is the prefix used for the currently focused item.
const Cursor = "▸ "

// Pre-built reusable styles.
var (
	TitleStyle = lipgloss.NewStyle().Bold(true)

	HeadingStyle = lipgloss.NewStyle().Bold(true)

	HelpStyle = lipgloss.NewStyle().Faint(true)

	SubtextStyle = lipgloss.NewStyle().Faint(true)

	SelectedStyle = lipgloss.NewStyle().
			Foreground(ColorAccent).
			Bold(true)

	UnselectedStyle = lipgloss.NewStyle()

	SuccessStyle = lipgloss.NewStyle().Foreground(ColorSuccess)

	ErrorStyle = lipgloss.NewStyle().Foreground(ColorError)

	WarningStyle = lipgloss.NewStyle().Foreground(ColorWarning)

	// FrameStyle keeps an uncolored border: it groups the screen content and
	// the welcome layout measures its frame size.
	FrameStyle = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			Padding(1, 2)

	ProgressFilled = lipgloss.NewStyle().Foreground(ColorSuccess)

	ProgressEmpty = lipgloss.NewStyle().Faint(true)

	PercentStyle = lipgloss.NewStyle().Bold(true)
)
