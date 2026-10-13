# Test speed: run independent internal/cli tests in parallel

## Objective

Cut `internal/cli` test time (311 s in the full suite, the wall-time floor of `go test ./...`
at 312 s) by marking tests that share no mutable process state with `t.Parallel()`.

## Problem and evidence

- `internal/cli`: ~1250 top-level tests, 28 use `t.Parallel()`. Tests run one at a time on a
  10-core machine; per-test cost is mostly `git` subprocesses (I/O and process spawn, not CPU).
- 131 `Setenv` and 49 `Chdir` calls; several tests also swap package-level variables
  (seams such as `gitCommandContext`, `lookPath`-style hooks, output streaming).

## Why it is safe when done right

Go pauses parallel top-level tests and resumes them only after every sequential top-level
test has finished. Sequential tests that call `t.Setenv`, `t.Chdir`, or swap package
variables therefore never overlap a parallel test. The rule is only: a test marked parallel
(and every helper it calls) must not call `Setenv`/`Chdir`, swap package-level state, or
capture `os.Stdout`/`os.Stderr`, and must not depend on such state being set.
Go panics if a parallel test calls `t.Setenv`/`t.Chdir`, so those surface immediately;
shared-variable mutation surfaces under `-race`.

## Scope

- `internal/cli` test files only. No production code changes, unless a test needs a seam
  that already exists elsewhere (report instead of adding one).
- Out of scope: `internal/reviewtransaction` (follow-up if this pays off), CI changes.

## Constraints

- Never weaken a test to make it parallel-safe. When in doubt, leave it sequential.
- `go test -race ./internal/cli` must pass, and repeated runs must stay green.

## Tasks

- [x] T1 — Mark parallel-safe `internal/cli` tests with `t.Parallel()`. Route: delegated (writer; many files, preparation reading).
- [x] T2 — Re-measure `go test ./...` and record before/after. Route: delegated (verification run).

## Acceptance criteria

- `go test -count=1 ./internal/cli` passes; `go test -race -count=1 ./internal/cli` passes.
- Three consecutive `go test -count=1 ./internal/cli` runs pass (flakiness check).
- `internal/cli` wall time drops measurably from 311 s.

## Checks

- `go run ./internal/gofmtcheck`
- `go vet ./internal/cli`
- `go test -race -count=1 ./internal/cli`
- `go test -count=1 ./...`

## Progress

- Baseline: `go test -count=1 ./...` 312.4 s wall; `internal/cli` 311 s (main `1687597a`).
- Delivery strategy: single-pr (fast-forward to main, as previous features).
- T1 (uncommitted): 477 top-level tests marked `t.Parallel()` across 127 files (28 were already
  parallel). Selection by a static taint pass over the `cli` test package (go/packages call graph):
  a test is excluded if it or any package function it reaches calls `t.Setenv`/`t.Chdir`/
  `os.Setenv`/`os.Chdir`, assigns a package-level variable (here or in another package), or calls
  an exported `Set*ForTest`-style seam. 732 excluded: 600 Setenv/Chdir, 127 package-var seams,
  3 other-package seams or `os.Stderr`/`os.Args` swaps, 2 reading the shared TestMain HOME.
  A probe of the shared TestMain HOME after running only the marked tests found only Go telemetry
  files, so the marked tests do not write HOME-derived paths.
- T1 result: the marked tests cost only ~14 s of the 293 s sequential total, so wall time did not
  drop measurably: same-machine baseline 294 s; after T1 284.0 s, 285.8 s, 300.9 s (all pass);
  `go test -race -count=1 ./internal/cli` passes (415.7 s).
- The real blocker is `reviewModeHome` (`review_mode_test.go`), which sets HOME via `t.Setenv` for
  the global RDD opinion: it blocks 519 tests / ~226 s; 344 tests (~123 s) have no other blocker.
  Next: `reviewProviderAdapterFor` (101 tests, ~75 s), `osUserHomeDir` (68, ~35 s),
  `cmdLookPath` (73, ~30 s), `runCommand` (71, ~30 s). Unblocking them needs a per-call home or
  seam injection (a production change outside T1 scope).

- Decision: the 477 `t.Parallel()` marks were discarded (no measurable gain, and they constrain
  future edits). Unblocking `reviewModeHome` would need production code to take the home
  directory per call (8+ `os.UserHomeDir` reads), judged too invasive for a test-speed gain.
- Replacement: `scripts/test-fast.sh` compiles each slow package's test binary once and runs its
  top-level tests round-robin across separate processes, so `Setenv`/`Chdir`/package seams need no
  change. Observed on this 10-core machine, all PASS: 6/3 shards 137 s, 4/2 149 s, 8/4 119 s,
  10/5 116 s; default set to 8/4 (vs 312 s for `go test ./...`). CI keeps `go test ./...`.
- Found by T1's static pass and fixed: `reviewNarrationWordRegexpCache` (`internal/cli/review_narration.go`)
  was a plain map written without a lock; now a `sync.Map`. `go test -race -run Narration` ok.

## Next step

Feature complete. Fast-forward to main (user decision).
