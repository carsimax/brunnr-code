package testenv_test

import (
	"os"
	"testing"
)

// isolateSentinelSubprocessEnv triggers the sentinel-check stand-in below.
// It plays the same role as internal/cli/protocol_probe_test.go's own
// GENTLE_AI_TEST_CLI_STANDIN re-exec guard.
const isolateSentinelSubprocessEnv = "GENTLE_AI_TESTENV_ISOLATE_SUBPROCESS"

// TestMain re-execs into the sentinel subprocess when the parent test below
// spawns this same test binary with an override already set to a sentinel
// directory, i.e. before any package TestMain runs.
func TestMain(m *testing.M) {
	if os.Getenv(isolateSentinelSubprocessEnv) == "xdg" {
		runOpenCodeSentinelSubprocess()
	}
	os.Exit(m.Run())
}
