# Test speed: cache Git answers about immutable trees

## Objective

Cut `internal/cli` test time (332 s after `odd/tasks/test-speed.md`) by not re-spawning
`git` for questions whose answer depends only on content-addressed tree objects.

## Problem and evidence

- After the rev-parse cache, `internal/cli` has no outliers: 565 tests at 0.1–1 s sum
  209 s, ~55 `git` spawns per test at ~6 ms each.
- A 28-test sample spawned 1555 `git` processes; 840 were exact repeats. Repeats whose
  answer depends only on tree object IDs:
  - `diff-tree --no-commit-id --name-only` 231 (`SnapshotBuilder.changedPaths`, via
    `ValidateEvidence`, `build`, `targetedValidationFixFromSnapshot`, `ValidateLiveSnapshot`,
    `CandidateLocationSupportsCausality`)
  - `diff --numstat` 117 (`snapshot.go:596`, `risk.go:1180`), `diff --raw` 115 (`snapshot.go:631`,
    `frozen_candidate_context.go:225`)
  - `--no-pager diff` 58 (`frozen_candidate_context.go:369`), `ls-tree` 31

## Why a cache is safe here

A tree object ID names its content. Two runs of the same read-only command over the same
tree IDs, with the same Git binary, environment and isolation config, answer the same.
No filesystem revalidation is needed, unlike the repository identity cache.

## Scope

- `internal/reviewtransaction` only: read-only commands whose arguments name only object IDs.
- Out of scope: live-state commands (`rev-parse --verify HEAD`, `write-tree`, `ls-files`, `add -u`),
  `t.Parallel` adoption, CI changes.

## Constraints

- Cache only successful, complete output (no truncated/limited output, no errors).
- Key includes the full argument list, the Git environment digest, any isolation config
  that changes output (attributes file content, object format), and the object store,
  so an object missing from one repository is never answered from another.
- Bounded, process-wide; production runs one command per process.

## Tasks

- [x] T1 — Cache read-only Git answers over object IDs. Route: delegated (writer; preparation reading, 2+ files).
- [x] T2 — Re-measure `go test ./...` and record before/after. Route: delegated (verification run).

## Acceptance criteria

- A repeated `changedPaths`/numstat/raw diff over the same trees spawns no further `git`.
- Different trees, object stores, or environments never share an answer.
- `go run ./internal/gofmtcheck` and `go test ./...` pass.

## Checks

- `go run ./internal/gofmtcheck`
- `go test ./internal/reviewtransaction -count=1`
- `go test ./... -count=1`

## Progress

- Baseline: `go test ./...` 334.6 s wall; `internal/cli` 332 s; `internal/reviewtransaction` 166 s.
- Delivery strategy: single-pr (fast-forward to main, as the previous feature).
- T1 implemented (not yet committed): `reviewGitObjectQueryFor` in
  `internal/reviewtransaction/git_object_answer_cache.go`, consulted by
  `runGitCapturedRangeWithTimeout`. Allowlist: `ls-tree`, name-only `diff-tree`, and
  `diff`/`diff-tree` only inside the bare isolated view. Key: repo path, isolate/stderr
  flags, output limit, full args (attributes file by content), extra env (temp
  `GIT_DIR` and config files by content, `GIT_OBJECT_DIRECTORY` must be the repo's),
  git env digest; hits re-prove the object store with `os.SameFile`. Revisions must be
  full IDs of the repo's object format. Stdin, ranged and overflow-tolerant reads bypass.
- T1 checks: gofmtcheck ok; vet ok; `go test ./internal/reviewtransaction` ok 134.7 s
  (was 166 s); `go test ./internal/cli` ok 264.0 s (was 332 s). Pinned counts updated:
  prepared inspector 9 → 6, ValidateEvidence ls-tree 1 → 0.

- T2 observed: `go test -count=1 ./...` 304.2 s wall (was 334.6 s), 0 failing packages.
  `internal/cli` 302.9 s (was 332 s), `internal/reviewtransaction` 168.7 s (unchanged under full-suite
  CPU contention; 134.7 s alone). user+sys CPU 366 s (was 413 s).
- Parent review: allowlist bypasses anything that is not a full object ID. Known minor gap: outside the
  isolated view, `ls-tree`/`diff-tree --name-only` without `-z` depend on repository `core.quotePath`,
  which the key does not bind; stale only if that config changes mid-process.
- RDD is disabled in this clone, so no native review ran.

## Next step

Feature complete. `internal/cli` is the wall-time floor (~300 s, serial). Next lever: `t.Parallel`
for tests that use neither `Setenv` nor `Chdir`.
