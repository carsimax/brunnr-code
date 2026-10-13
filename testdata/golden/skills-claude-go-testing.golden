---
name: go-testing
description: "Trigger: Go tests, go test coverage, Bubbletea teatest, golden files. Apply focused Go testing patterns."
license: Apache-2.0
metadata:
  author: gentleman-programming
  version: "1.1"
---

## Activation Contract

Load this skill when writing or reviewing Go tests, adding coverage, testing Bubbletea/TUI flows, using `teatest`, or updating golden files.

## Hard Rules

- Prefer table-driven tests for multiple cases; use `t.Run(tt.name, ...)`.
- Test behavior and state transitions, not implementation trivia.
- Use `t.TempDir()` for filesystem tests; never rely on a real home directory.
- Keep integration tests skippable with `testing.Short()` when they run external commands or slow flows.
- For Bubbletea, test `Model.Update()` directly for state changes; use `teatest` only for interactive flows.
- Golden files must be deterministic; update only through the repo's `-update` path and rerun tests without `-update`.
- Use small mocks/interfaces around system or command execution boundaries.

## Decision Gates

| Target | Test pattern |
|---|---|
| Pure function or parser | Table-driven unit test. |
| Error behavior | Explicit success and failure cases. |
| File operations | `t.TempDir()` plus focused assertions. |
| TUI state transition | Direct `Model.Update()` call with `tea.Msg`. |
| Full TUI interaction | `teatest.NewTestModel()`. |
| Rendered output | Golden file test. |
| Real external command | Integration test; skip in `-short`. |

## Execution Steps

1. Identify behavior under test and the smallest public boundary that proves it.
2. Choose the test pattern from the decision gate.
3. Name cases by scenario, not input mechanics.
4. Assert outputs, errors, state, and side effects explicitly.
5. Run the narrow package test first, then the relevant broader suite.
6. For golden updates: run with `-update`, inspect diff, then rerun without `-update`.

## Common Rationalizations

| Rationalization | Reality |
|-----------------|---------|
| "I'll add the test after the fix" | When a runnable deterministic test exists, write it first and watch it fail; a test written after may never have failed. |
| "The golden changed, so I'll run `-update`" | First explain why the output changed. Regenerating hides regressions as easily as it records features. |
| "It's flaky, add a `t.Skip`" | A skip deletes coverage. Find the nondeterminism: time, map order, shared state, or goroutine sync. |
| "This assertion is too strict" | Loosen it only when the asserted behavior is genuinely not part of the contract. |

## Red Flags

- A new `t.Skip` or a deleted assertion or test case in the diff.
- `time.Sleep` used to wait for goroutines or I/O instead of channels, `sync.WaitGroup`, or polling with a deadline.
- A golden file regenerated with no stated reason for the output change.
- A test that fails under `-count=1` or `-race` but passes from the cache.

## Verification

- [ ] The focused `go test ./<pkg> -run '<Test>' -count=1` result is recorded, including RED before the fix when test-first applied.
- [ ] Golden updates name the reason for each changed output and pass on a rerun without `-update`.
- [ ] Full `go test ./...` is green.

## Output Contract

Report test files changed, scenarios covered, commands executed, golden files updated, and any skipped integration scope.

## References

- [references/examples.md](references/examples.md) — compact table-driven, Bubbletea, teatest, golden, and command examples.
