package telemetry

import (
	"testing"
)

func TestNoticeRuntimeDisclosure(t *testing.T) {
	const want = "Gentle AI sends anonymous usage metrics (version, OS, agents, counters) and may send anonymous runtime usage from supported Claude Code/Codex integrations (public model, effort, agent class, available token usage, timing, error categories); runtime usage is never stored locally; run gentle-ai telemetry disable to opt out."
	if NoticeLine != want {
		t.Fatalf("enrollment notice = %q, want %q", NoticeLine, want)
	}
}
