package main

import (
	"strings"
	"testing"
)

func TestOpenCodeHookHarnessRequiresExactProviderOwnedTask(t *testing.T) {
	for _, forbidden := range []string{"binding_pairs", "Object.fromEntries", "config.body"} {
		if strings.Contains(hookHarness, forbidden) {
			t.Errorf("OpenCode hook harness still assembles a binding through %q", forbidden)
		}
	}
	for _, want := range []string{"subagent: string", "prompt: string", "const prompt = config.prompt"} {
		if !strings.Contains(hookHarness, want) {
			t.Errorf("OpenCode hook harness missing exact provider-task relay shape %q", want)
		}
	}
}

func TestOpenCodeCrossLaneCopiesProviderTaskExactly(t *testing.T) {
	wantAgent := "review-reliability"
	wantPrompt := "GENTLE_AI_REVIEW_BINDING {\"order\":0}\nopaque body with `fences` and trailing spaces  "
	input := map[string]any{"provider_task": map[string]any{"agent": wantAgent, "prompt": wantPrompt}}
	agent, prompt, ok := providerTask(input)
	if !ok || agent != wantAgent || prompt != wantPrompt {
		t.Fatalf("provider task relay = %q/%q/%t, want byte-exact %q/%q/true", agent, prompt, ok, wantAgent, wantPrompt)
	}
	for _, incomplete := range []map[string]any{
		{},
		{"provider_task": map[string]any{"agent": wantAgent}},
		{"provider_task": map[string]any{"prompt": wantPrompt}},
	} {
		if _, _, ok := providerTask(incomplete); ok {
			t.Fatalf("incomplete provider task accepted: %#v", incomplete)
		}
	}
}

func TestCommittedMediumCandidateFailsWhenBaseWriteFails(t *testing.T) {
	battery := &battery{
		workRoot: t.TempDir(),
		lineages: map[string]lineageScope{},
	}

	repo, baseTree, ok := battery.committedMediumCandidate(
		"test", "invalid-base-write", ".", "base", "candidate",
	)
	if ok || repo != "" || baseTree != "" {
		t.Fatalf("committed candidate after base write failure = repo %q, base %q, ok %t", repo, baseTree, ok)
	}
	if len(battery.checks) != 1 {
		t.Fatalf("failure checks = %#v, want exactly one committed-process failure", battery.checks)
	}
	failure := battery.checks[0]
	if failure.Lane != "test" || failure.Name != "committed process base" || failure.Status != statusFail {
		t.Fatalf("base write failure = %#v, want committed process base FAIL", failure)
	}
}
