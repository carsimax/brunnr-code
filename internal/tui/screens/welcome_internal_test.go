package screens

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestWrapWelcomeBanner_UsesDisplayWidthForWideBanner(t *testing.T) {
	const width = 40
	text := "Updates: 🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀 deployment notice must stay inside the welcome frame"

	wrapped := wrapWelcomeBanner(text, width)

	for i, line := range strings.Split(wrapped, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("wrapped line %d display width = %d, want <= %d\nline: %q\nwrapped:\n%s", i, got, width, line, wrapped)
		}
	}
}

func TestRenderWelcome_StaysWithinViewport(t *testing.T) {
	cases := []struct {
		name         string
		width        int
		height       int
		updateBanner string
		minimum      bool
		wantPrimary  string
		wantControl  string
	}{
		{name: "Windows Terminal startup", width: 120, height: 30},
		{name: "narrow resize", width: 80, height: 24},
		{name: "short viewport", width: 120, height: 19},
		{name: "below compact height", width: 120, height: 2, minimum: true},
		{name: "below compact width", width: 18, height: 19, minimum: true},
		{name: "below frame border width", width: 2, height: 20, minimum: true, wantPrimary: "Go"},
		{name: "tiny viewport uses atomic labels", width: 2, height: 2, minimum: true, wantPrimary: "Go", wantControl: "q"},
		{name: "single column tiny viewport uses atomic labels", width: 1, height: 2, minimum: true, wantPrimary: ">", wantControl: "q"},
		{
			name:         "short viewport with optional content",
			width:        120,
			height:       19,
			updateBanner: strings.Repeat("Updates available with more detail ", 20),
		},
		{
			name:         "compact viewport with optional content",
			width:        120,
			height:       17,
			updateBanner: "Updates available",
		},
		{name: "wide resize", width: 160, height: 50},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := RenderWelcomeWithSize(0, "dev", tc.updateBanner, nil, true, true, tc.width, tc.height)

			if got := lipgloss.Width(view); got > tc.width {
				t.Fatalf("welcome width = %d, want <= %d\nview:\n%s", got, tc.width, view)
			}
			if got := lipgloss.Height(view); got > tc.height {
				t.Fatalf("welcome height = %d, want <= %d\nview:\n%s", got, tc.height, view)
			}
			content := view
			if tc.minimum {
				content = strings.ReplaceAll(view, "\n", "")
			}
			primary := tc.wantPrimary
			if primary == "" {
				primary = "Start installation"
			}
			if !strings.Contains(content, primary) {
				t.Fatalf("welcome lost primary action %q after fitting viewport\nview:\n%s", primary, view)
			}
			if tc.minimum {
				control := tc.wantControl
				if control == "" {
					control = "j/k"
				}
				for _, want := range []string{primary, control} {
					if !strings.Contains(content, want) {
						t.Fatalf("minimum welcome state lost %q\nview:\n%s", want, view)
					}
				}
			} else {
				for _, want := range []string{"Quit", "j/k: navigate • enter: select • q: quit"} {
					if !strings.Contains(view, want) {
						t.Fatalf("welcome lost %q after fitting viewport\nview:\n%s", want, view)
					}
				}
			}
		})
	}
}
