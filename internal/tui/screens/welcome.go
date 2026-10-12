package screens

import (
	"strings"

	"github.com/carsimax/brunnr-code/internal/tui/styles"
	"github.com/carsimax/brunnr-code/internal/update"
	"github.com/charmbracelet/lipgloss"
	"github.com/rivo/uniseg"
)

const welcomeHelpText = "j/k: navigate • enter: select • q: quit"

// WelcomeOptions returns the welcome menu options.
// When hasEngines is false, "Create your own Agent" is shown as disabled
// (labelled "(no agents)") to signal that no supported AI engine is installed.
func WelcomeOptions(updateResults []update.UpdateResult, updateCheckDone bool, hasEngines bool) []string {
	upgradeLabel := "Upgrade tools"
	if updateCheckDone && update.HasUpdates(updateResults) {
		upgradeLabel = "Upgrade tools ★"
	} else if updateCheckDone && !update.HasUpdates(updateResults) {
		upgradeLabel = "Upgrade tools (up to date)"
	}

	agentLabel := "Create your own Agent"
	if !hasEngines {
		agentLabel = "Create your own Agent (no agents)"
	}

	opts := []string{
		"Start installation",
		upgradeLabel,
		"Sync configs",
		"Upgrade + Sync",
		"Configure models",
		agentLabel,
	}

	opts = append(opts, "Manage backups")
	opts = append(opts, "Reset review store")
	opts = append(opts, "Receipt-Driven Development")
	opts = append(opts, "Managed uninstall")
	opts = append(opts, "Community Tools")
	opts = append(opts, "Quit")

	return opts
}

func RenderWelcome(cursor int, version string, updateBanner string, updateResults []update.UpdateResult, updateCheckDone bool, hasEngines bool) string {
	return RenderWelcomeWithWidth(cursor, version, updateBanner, updateResults, updateCheckDone, hasEngines, 0)
}

func RenderWelcomeWithWidth(cursor int, version string, updateBanner string, updateResults []update.UpdateResult, updateCheckDone bool, hasEngines bool, width int) string {
	return RenderWelcomeWithSize(cursor, version, updateBanner, updateResults, updateCheckDone, hasEngines, width, 0)
}

func RenderWelcomeWithSize(cursor int, version string, updateBanner string, updateResults []update.UpdateResult, updateCheckDone bool, hasEngines bool, width int, height int) string {
	render := func(includeOptional, compact bool) string {
		var b strings.Builder

		if !compact {
			b.WriteString(styles.TitleStyle.Render("Brunnr-Code " + version))
			b.WriteString("\n")

			if includeOptional && updateBanner != "" {
				b.WriteString(styles.WarningStyle.Render(wrapWelcomeBanner(updateBanner, welcomeContentWidth(width))))
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}

		if compact {
			b.WriteString(renderWelcomeText(styles.HeadingStyle, "Menu", width))
		} else {
			b.WriteString(styles.HeadingStyle.Render("Menu"))
		}
		b.WriteString("\n")
		options := WelcomeOptions(updateResults, updateCheckDone, hasEngines)
		if compact {
			b.WriteString(renderWelcomeOptions(options, cursor, width))
			b.WriteString(renderWelcomeText(styles.HelpStyle, welcomeHelpText, width))
			return b.String()
		}
		b.WriteString(renderOptions(options, cursor))
		b.WriteString(styles.HelpStyle.Render(welcomeHelpText))
		return welcomeFrameStyle(width).Render(b.String())
	}
	if width > 0 && width <= styles.FrameStyle.GetHorizontalBorderSize() {
		return renderWelcomeMinimum(width, height, cursor)
	}
	fitsViewport := func(view string) bool {
		return (width <= 0 || lipgloss.Width(view) <= width) &&
			(height <= 0 || lipgloss.Height(view) <= height)
	}

	view := render(true, false)
	if !fitsViewport(view) {
		// Drop the optional banner and the frame when the measured viewport cannot
		// hold the full screen. The complete menu, primary action, and help line remain visible.
		view = render(false, true)
	}
	if !fitsViewport(view) {
		// A viewport shorter than the compact menu still needs a safe, actionable
		// state instead of allowing the terminal to clip the rendered content.
		view = renderWelcomeMinimum(width, height, cursor)
	}
	return view
}

func renderWelcomeText(style lipgloss.Style, text string, width int) string {
	if width <= 0 {
		return style.Render(text)
	}
	return style.Render(strings.Join(wrapPlainLine(text, width), "\n"))
}

func renderWelcomeOptions(options []string, cursor int, width int) string {
	if width <= 0 {
		return renderOptions(options, cursor)
	}

	var b strings.Builder
	for idx, option := range options {
		prefix := "  "
		style := styles.UnselectedStyle
		if idx == cursor {
			prefix = styles.Cursor
			style = styles.SelectedStyle
		}
		for _, line := range wrapPlainLine(prefix+option, width) {
			b.WriteString(style.Render(line))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func renderWelcomeMinimum(width int, height int, cursor int) string {
	const primaryAction = "Start installation"
	const compactPrimaryAction = "Go"
	const narrowPrimaryAction = ">"
	const compactHelp = "j/k, enter, q"
	const narrowHelp = "q"

	primaryLabel := primaryAction
	if width > 0 && lipgloss.Width(primaryLabel) > width {
		primaryLabel = compactPrimaryAction
		if lipgloss.Width(primaryLabel) > width {
			primaryLabel = narrowPrimaryAction
		}
	}
	primaryLines := wrapWelcomeLine(primaryLabel, width)
	helpLines := wrapWelcomeLine(welcomeHelpText, width)
	if height > 0 && len(primaryLines)+len(helpLines) > height {
		helpLabel := compactHelp
		if width > 0 && lipgloss.Width(helpLabel) > width {
			helpLabel = narrowHelp
		}
		helpLines = wrapWelcomeLine(helpLabel, width)
	}

	lines := append(primaryLines, helpLines...)
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	if len(lines) == 0 {
		return ""
	}

	primaryLineCount := len(primaryLines)
	if primaryLineCount > len(lines) {
		primaryLineCount = len(lines)
	}
	primaryStyle := styles.UnselectedStyle
	if cursor == 0 {
		primaryStyle = styles.SelectedStyle
	}
	rendered := primaryStyle.Render(strings.Join(lines[:primaryLineCount], "\n"))
	if primaryLineCount == len(lines) {
		return rendered
	}
	return rendered + "\n" + styles.HelpStyle.Render(strings.Join(lines[primaryLineCount:], "\n"))
}

func wrapWelcomeLine(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	return wrapPlainLine(text, width)
}

func welcomeContentWidth(width int) int {
	if width <= 0 {
		return 0
	}
	return max(0, width-styles.FrameStyle.GetHorizontalFrameSize())
}

func welcomeFrameStyle(width int) lipgloss.Style {
	if width <= 0 {
		return styles.FrameStyle
	}
	borderWidth := styles.FrameStyle.GetHorizontalBorderSize()
	if width <= borderWidth {
		return styles.FrameStyle.MaxWidth(width)
	}
	return styles.FrameStyle.Width(width - borderWidth)
}

func wrapWelcomeBanner(text string, width int) string {
	if width <= 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		wrapped = append(wrapped, wrapPlainLine(line, width)...)
	}
	return strings.Join(wrapped, "\n")
}

func wrapPlainLine(line string, width int) []string {
	if lipgloss.Width(line) <= width {
		return []string{line}
	}

	words := strings.Fields(line)
	if len(words) == 0 {
		return []string{""}
	}

	var lines []string
	var current strings.Builder
	for _, word := range words {
		wordWidth := lipgloss.Width(word)
		currentWidth := lipgloss.Width(current.String())
		if wordWidth > width {
			if currentWidth > 0 {
				lines = append(lines, current.String())
				current.Reset()
			}
			lines = append(lines, hardWrapWord(word, width)...)
			continue
		}
		if currentWidth == 0 {
			current.WriteString(word)
			continue
		}
		if currentWidth+1+wordWidth > width {
			lines = append(lines, current.String())
			current.Reset()
			current.WriteString(word)
			continue
		}
		current.WriteString(" ")
		current.WriteString(word)
	}
	if current.Len() > 0 {
		lines = append(lines, current.String())
	}
	return lines
}

func hardWrapWord(word string, width int) []string {
	lines := make([]string, 0)
	var current strings.Builder
	currentWidth := 0

	graphemes := uniseg.NewGraphemes(word)
	for graphemes.Next() {
		cluster := graphemes.Str()
		clusterWidth := lipgloss.Width(cluster)
		if currentWidth > 0 && currentWidth+clusterWidth > width {
			lines = append(lines, current.String())
			current.Reset()
			currentWidth = 0
		}
		current.WriteString(cluster)
		currentWidth += clusterWidth
	}
	if current.Len() > 0 {
		lines = append(lines, current.String())
	}
	return lines
}
