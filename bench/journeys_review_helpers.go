package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func proveJSON(sandbox *Sandbox, target any, args ...string) error {
	observation := sandbox.readBack(args...)
	if err := json.Unmarshal([]byte(strings.TrimSpace(observation.Stdout)), target); err != nil {
		return fmt.Errorf("%s: %w (stderr: %s)", strings.Join(args, " "), err, firstLine(observation.Stderr))
	}
	return nil
}

// claudeProviderFakeScript is a deterministic compiled Claude Code provider.
// It answers a refuter request by corroborating every finding the compiled
// prompt names, and any other provider request with the response a journey
// wrote last (claude.response), only when that response's validation hash
// appears in the compiled prompt. It never calls a model.
const claudeProviderFakeScript = `#!/bin/sh
set -eu
prompt="$0.prompt"
cat > "$prompt"
if grep -q 'gentle-ai.review-provider-refuter-request/v1' "$prompt"; then
  hash=$(grep -o '"request_hash":"[^"]*"' "$prompt" | head -n 1 | sed 's/.*:"\(.*\)"/\1/')
  test -n "$hash"
  results=
  for id in $(grep -o '"finding_id":"[^"]*"' "$prompt" | sed 's/.*:"\(.*\)"/\1/' | sort -u); do
    results="$results${results:+,}{\"finding_id\":\"$id\",\"outcome\":\"corroborated\",\"proof_refs\":[\"independent reproduction of $id\"]}"
  done
  printf '{"refuter_request_hash":"%s","results":[%s]}\n' "$hash" "$results"
  exit 0
fi
if test -f "$0.response"; then
  hash=$(grep -o '"targeted_validation_request_hash":"[^"]*"' "$0.response" | cut -d '"' -f 4)
  if test -n "$hash"; then grep -q "$hash" "$prompt"; fi
  cat "$0.response"
  exit 0
fi
exit 1
`

// installClaudeProviderFake prepends the deterministic Claude Code provider
// to PATH once per sandbox, so compiled provider captures run without a
// model.
func installClaudeProviderFake(sandbox *Sandbox) error {
	bin := filepath.Join(sandbox.Root, "claude-provider-bin")
	if sandbox.PathOverride == bin {
		return nil
	}
	if sandbox.PathOverride != "" {
		return fmt.Errorf("sandbox PATH override %q already owns the provider runtime", sandbox.PathOverride)
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(claudeProviderFakeScript), 0o755); err != nil {
		return err
	}
	sandbox.PathOverride = bin
	return nil
}

// runOfferedProviderCapture installs the fake provider, records the response
// it must return, and executes the exact capture operation STATUS offered as
// operation plus ordered provider-issued tokens.
func runOfferedProviderCapture(r *journeyRun, operation string, tokens []string, response []byte) (Observation, error) {
	if err := installClaudeProviderFake(r.sandbox); err != nil {
		return Observation{}, err
	}
	responsePath := filepath.Join(r.sandbox.PathOverride, "claude.response")
	if response == nil {
		if err := os.Remove(responsePath); err != nil && !os.IsNotExist(err) {
			return Observation{}, err
		}
	} else if err := os.WriteFile(responsePath, response, 0o644); err != nil {
		return Observation{}, err
	}
	verb, found := strings.CutPrefix(operation, "review.")
	if !found || verb == "" {
		return Observation{}, fmt.Errorf("offered capture operation %q is not a review operation", operation)
	}
	args := append([]string{"review", verb, "--cwd=" + r.sandbox.Repo}, tokens...)
	return r.runAt(r.sandbox.Root, args, true), nil
}
