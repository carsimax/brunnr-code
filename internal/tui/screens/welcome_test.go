package screens_test

import (
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/tui/screens"
)

// ─── WelcomeOptions ──────────────────────────────────────────────────────────

func TestWelcomeOptions_OffersConfigureModels(t *testing.T) {
	opts := screens.WelcomeOptions(nil, true, true)
	if !containsOption(opts, "Configure models") {
		t.Fatalf("unexpected menu: %v", opts)
	}
}

// TestWelcomeOptions_OptionCount verifies 11 options when hasEngines=true.
func TestWelcomeOptions_OptionCount(t *testing.T) {
	opts := screens.WelcomeOptions(nil, true, true)
	// Includes the Receipt-Driven Development entry.
	want := 11
	if len(opts) != want {
		t.Errorf("WelcomeOptions(hasEngines=true) = %d options, want %d; opts: %v", len(opts), want, opts)
	}
}

// TestWelcomeOptions_NoEngines_ShowsDisabledLabel verifies that when hasEngines=false,
// the agent option is labelled "(no agents)" to signal unavailability.
func TestWelcomeOptions_NoEngines_ShowsDisabledLabel(t *testing.T) {
	opts := screens.WelcomeOptions(nil, true, false)
	found := false
	for _, opt := range opts {
		if strings.Contains(opt, "no agents") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'no agents' label when hasEngines=false; got: %v", opts)
	}
}

// TestWelcomeOptions_AgentBuilderBeforeManageBackups verifies the retained shortcuts stay adjacent.
func TestWelcomeOptions_AgentBuilderBeforeManageBackups(t *testing.T) {
	opts := screens.WelcomeOptions(nil, true, true)

	if opts[5] != "Create your own Agent" || opts[6] != "Manage backups" {
		t.Fatalf("retained actions are not adjacent: %v", opts)
	}
}

func containsOption(opts []string, want string) bool {
	for _, opt := range opts {
		if opt == want {
			return true
		}
	}
	return false
}

func TestWelcomeOptions_IncludesManagedUninstall(t *testing.T) {
	opts := screens.WelcomeOptions(nil, true, true)

	found := false
	for _, opt := range opts {
		if opt == "Managed uninstall" {
			found = true
			break
		}
	}

	if !found {
		t.Fatalf("expected 'Managed uninstall' option; got: %v", opts)
	}
}
