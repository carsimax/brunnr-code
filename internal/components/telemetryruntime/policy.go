// Package telemetryruntime owns native runtime event adapters for Claude Code
// and Codex hooks.
package telemetryruntime

import (
	"errors"

	"github.com/gentleman-programming/gentle-ai/v4/internal/telemetry"
)

var errEnvelope = errors.New("invalid runtime telemetry envelope")

// allowed reports whether runtime telemetry may be sent: the environment must
// not opt out, and the persisted policy must have shown the notice and remain
// enabled.
func allowed(home string, getenv func(string) string) bool {
	if !telemetry.Decide(getenv, telemetry.State{Enabled: true}).Enabled {
		return false
	}
	state, err := telemetry.LoadPolicyState(home)
	return err == nil && state.NoticeShown && telemetry.Decide(getenv, state).Enabled
}
