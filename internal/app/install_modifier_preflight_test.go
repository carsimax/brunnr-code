package app

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/carsimax/brunnr-code/internal/state"
	"github.com/carsimax/brunnr-code/internal/system"
)

// TestRunArgsRemovedSetupFlagsRejectedWithoutEffects pins that the retired
// setup flags fail at flag parsing, before any state or file is written.
func TestRunArgsRemovedSetupFlagsRejectedWithoutEffects(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"preset", []string{"--preset", "full"}},
		{"component", []string{"--component", "engram"}},
		{"skill", []string{"--skill", "go-testing"}},
	} {
		for _, dryRun := range []bool{false, true} {
			name := tc.name + "/real"
			if dryRun {
				name = tc.name + "/dry-run"
			}
			t.Run(name, func(t *testing.T) {
				home := t.TempDir()
				setupMockHome(t, home)
				timestamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
				before := state.InstallState{InstalledAgents: []string{"claude-code"}, LastUpdateCheck: &timestamp}
				if err := state.Write(home, before); err != nil {
					t.Fatal(err)
				}
				before, err := state.Read(home)
				if err != nil {
					t.Fatal(err)
				}
				oldDetect, oldEnsure := detectSystem, ensureCurrentOSSupported
				t.Cleanup(func() { detectSystem, ensureCurrentOSSupported = oldDetect, oldEnsure })
				ensureCurrentOSSupported = func() error { return nil }
				detectSystem = func(context.Context) (system.DetectionResult, error) {
					return system.DetectionResult{System: system.SystemInfo{Supported: true, Profile: system.PlatformProfile{OS: "windows", Supported: true}}}, nil
				}
				args := append([]string{"install", "--agent", "claude-code"}, tc.args...)
				if dryRun {
					args = append(args, "--dry-run")
				}
				var out bytes.Buffer
				err = RunArgs(args, &out)
				if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
					t.Fatalf("error = %v, want unknown flag error", err)
				}
				if out.String() != "" {
					t.Fatalf("stdout = %q, want empty", out.String())
				}
				after, err := state.Read(home)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(after, before) {
					t.Fatalf("rejection changed state: before=%+v after=%+v", before, after)
				}
			})
		}
	}
}
