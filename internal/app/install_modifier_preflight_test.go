package app

import (
	"bytes"
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/carsimax/brunnr-code/internal/state"
	"github.com/carsimax/brunnr-code/internal/system"
)

func TestRunArgsOrphanInstallModifiersRejectedWithoutEffects(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"persona", []string{"--persona", "neutral"}, "--persona requires component persona in the resolved install plan; rerun gentle-ai install with --component persona added to your existing component list, or remove --persona"},
		{"skill", []string{"--skill", "go-testing"}, "--skill/--skills requires component skills in the resolved install plan; rerun gentle-ai install with --component skills added to your existing component list, or remove --skill/--skills"},
		{"skills", []string{"--skills", "go-testing"}, "--skill/--skills requires component skills in the resolved install plan; rerun gentle-ai install with --component skills added to your existing component list, or remove --skill/--skills"},
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
				before := state.InstallState{InstalledAgents: []string{"claude-code"}, Persona: "custom", LastUpdateCheck: &timestamp}
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
				args := append([]string{"install", "--agent", "claude-code", "--component", "theme"}, tc.args...)
				if dryRun {
					args = append(args, "--dry-run")
				}
				var out bytes.Buffer
				err = RunArgs(args, &out)
				if err == nil || err.Error() != tc.want {
					t.Fatalf("error = %v, want %q", err, tc.want)
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
