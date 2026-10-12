package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/agents/claude"
	"github.com/gentleman-programming/gentle-ai/v4/internal/telemetry"
	"github.com/gentleman-programming/gentle-ai/v4/internal/testenv"
)

// telemetryTestSpawnRecorder is the RecordingSpawner installed as
// telemetry.DefaultSpawn for this whole test binary (see TestMain). Tests
// that need to observe whether a trigger actually attempted a send —
// without ever starting a real process or reaching the network — read
// telemetryTestSpawnRecorder.Calls() rather than injecting their own Deps.Spawn,
// since production call sites like TelemetryTrigger never expose that seam.
var telemetryTestSpawnRecorder *telemetry.RecordingSpawner

// pinGoCachesBeforeHomeIsolation keeps explicit GOPATH/GOMODCACHE/GOCACHE
// values and otherwise records Go's HOME-derived defaults as explicit values,
// so swapping HOME for a temporary directory does not empty the caches.
func pinGoCachesBeforeHomeIsolation() {
	if home, err := os.UserHomeDir(); err == nil && os.Getenv("GOPATH") == "" {
		if err := os.Setenv("GOPATH", filepath.Join(home, "go")); err != nil {
			panic(err)
		}
	}
	if os.Getenv("GOCACHE") == "" {
		if cache, err := os.UserCacheDir(); err == nil {
			if err := os.Setenv("GOCACHE", filepath.Join(cache, "go-build")); err != nil {
				panic(err)
			}
		}
	}
}

// TestMain overrides verifyEngramVersion and probeEngramProtocolFlag with
// hermetic fakes for the whole internal/cli test binary, so pre-existing
// tests never depend on a real installed engram binary being present (or
// absent) on the machine running `go test`. Individual tests that need to
// exercise the new Decision 1 version-gate / Decision 4 probe behavior
// override these vars locally (save/restore), the same pattern already used
// for cmdLookPath elsewhere in this package.
//
// Defaults intentionally mirror "engram not verifiable": empty version
// (falls back to the full protocol section, matching pre-change behavior)
// and an unsupported --protocol flag (omit it, matching pre-change setup
// invocations exactly — see the exact-argv assertions in
// run_integration_test.go).
//
// It does the same for claude's own
// LookPathOverride: since gentle-ai now refuses instead of installing a
// missing agent runtime (agentInstallStep in run.go), the many tests in this
// package whose real target is protocol forwarding, engram provisioning,
// workspace resolution, or config injection — not install/detection
// behavior — used to pass only because those binaries happened to be on the
// developer's PATH.
// Defaulting them to "present" here makes that accidental dependency an
// intentional, hermetic one; tests that must exercise a genuine absence (the
// refusal itself) override the relevant package's LookPathOverride back to
// missing locally, the same save/restore pattern as everywhere else — see
// e.g. TestAgentInstallStepRefusesMissingCodexInsteadOfInstalling for that
// opposite, deliberately-kept case.
func TestMain(m *testing.M) {
	if os.Getenv("GENTLE_AI_TEST_BREW_FIXTURE") == "1" {
		os.Exit(runPortableBrewFixture())
	}
	// Neutralize ambient agent config-path overrides before anything else,
	// including before the stand-in re-exec branch below, so a developer
	// shell cannot redirect these tests outside the sandboxed HOME set up
	// below.
	testenv.Isolate()
	// Subprocess stand-in (#4434 regression): re-executed with the CLI
	// arguments of an emitted continuation command, this test binary must
	// run the real CLI dispatch (flag parsing, agent selection, sync
	// execution), not the test harness below -- which would swap HOME out
	// from under the captured environment the continuation was emitted
	// against. The same LookPath stubs as the harness keep agent discovery
	// hermetic, and DO_NOT_TRACK is inherited so telemetry stays offline.
	if os.Getenv("GENTLE_AI_TEST_CLI_STANDIN") == "1" {
		if err := os.Unsetenv("GENTLE_AI_CHANNEL"); err != nil {
			panic(err)
		}
		agentPresent := func(name string) (string, error) { return "/usr/local/bin/" + name, nil }
		claude.LookPathOverride = agentPresent
		// The same routing app.RunArgs performs for the sync subcommand
		// (internal/app/app.go: cli.RunSync(args[1:]) -- it cannot be imported
		// here without an import cycle): strip the verb, reject anything else,
		// and run the real flag parsing and sync execution.
		args := os.Args[1:]
		var err error
		switch {
		case len(args) > 0 && args[0] == "sync":
			_, err = RunSync(args[1:])
		default:
			fmt.Fprintf(os.Stderr, "stand-in: unsupported CLI arguments %q\n", args)
			os.Exit(1)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if err := os.Unsetenv("GENTLE_AI_CHANNEL"); err != nil {
		panic(err)
	}
	// Go derives its module and build caches from HOME. Pin them to the
	// developer's existing caches before HOME moves, so tests that build the
	// real CLI (TestCursorJudgmentDayCLISmoke) never need the network.
	pinGoCachesBeforeHomeIsolation()
	testHome, err := os.MkdirTemp("", "gentle-ai-cli-test-home-*")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("HOME", testHome); err != nil {
		panic(err)
	}
	if err := os.Setenv("USERPROFILE", testHome); err != nil {
		panic(err)
	}

	verifyEngramVersion = func() (string, error) {
		return "", errors.New("engram version not available in tests")
	}
	probeEngramProtocolFlag = func(context.Context) (string, error) {
		return "", errors.New("engram setup --help not available in tests")
	}

	agentPresent := func(name string) (string, error) { return "/usr/local/bin/" + name, nil }
	claude.LookPathOverride = agentPresent

	// Telemetry hermeticity for the whole binary: countless tests in this
	// package exercise install/sync/review-outcome code paths that now call
	// telemetry.Opportunistic or increment a counter, without any of them
	// intending to test telemetry itself. Two defaults close that gap
	// without touching the many tests that already sandbox HOME themselves
	// (t.Setenv save/restores against whatever this TestMain set, never
	// against the developer's real environment):
	//   - DO_NOT_TRACK=1 makes telemetry.Decide refuse before any state file
	//     is read or written, for every test that does not explicitly
	//     re-enable it for its own scope.
	//   - DefaultSpawn is a RecordingSpawner: even a test that does
	//     re-enable telemetry can never start a real process or reach the
	//     network merely by calling Opportunistic with no injected Spawn.
	if err := os.Setenv("DO_NOT_TRACK", "1"); err != nil {
		panic(err)
	}
	telemetryTestSpawnRecorder = telemetry.NewRecordingSpawner()
	telemetry.DefaultSpawn = telemetryTestSpawnRecorder.Spawn

	code := m.Run()
	_ = os.RemoveAll(testHome)
	os.Exit(code)
}
