# Test speed: stop re-asking Git for repository directories

## Objective

Cut the `go test ./...` wall time (608 s on a 10-core M-series Mac; `internal/cli`
hits the default 10 m `go test` timeout) by removing redundant `git` subprocesses
in the review repository-identity boundary, without weakening its identity checks.

## Problem and evidence

- `internal/cli`: 989 tests, ~600 s summed, only 28 use `t.Parallel`.
  `internal/reviewtransaction`: 272 s.
- `TestCrossRepositoryLifecycleRootConformance` spawns 1284 `git` processes;
  ~65 % are `rev-parse` (`--show-toplevel` 458, `--show-toplevel --git-common-dir --git-dir` 266,
  `--show-object-format` 114).
- Hot path: `isolatedImmutableTreeGitWithAttributesFile` → `reviewRepositoryIdentity`
  → `OpenRepositoryIdentityLease` → `ResolveRepositoryRoot` (rev-parse) +
  `openRepositoryIdentityLeaseAtRoot` → `resolveReviewRepositoryDirectories` (rev-parse),
  then `gitObjectFormat` (rev-parse), on every call (114 calls in that test).

## Why a cache is safe here

`openRepositoryIdentityLeaseAtRoot` already proves the answer with the filesystem:
`os.SameFile` on root/top/common/git dirs, the `.git` control entry (inode + gitfile
payload), and the `commondir` control. A cache that stores only Git's path answers and
re-runs every one of those checks on a hit skips the spawn but not the proof. A stale
answer fails the checks and falls back to asking Git.

## Scope

- `internal/reviewtransaction` only: repository directory resolution, repository root
  resolution, and object format lookup.
- Out of scope: `t.Parallel` adoption (blocked by 131 `Setenv` / 49 `Chdir` in `internal/cli`),
  CI timeout changes, `internal/cli/codegraph.go`.

## Constraints

- Every existing identity/containment check keeps running on a cache hit.
- A hit that fails validation falls back to Git, never errors on stale data alone.
- Cache is process-wide and bounded; production runs one command per process.

## Tasks

- [x] T1 — Cache Git's directory and root answers behind the existing filesystem identity checks, plus object format per common directory. Route: delegated (writer; 2+ non-trivial files, preparation reading).
- [x] T2 — Re-measure the full suite and record before/after. Route: delegated (verification run).

## Acceptance criteria

- Repeated lease opens on an unchanged repository spawn no further `rev-parse`.
- Changing the `.git` gitfile payload, nesting a new repository, or replacing a
  directory invalidates the cached answer.
- `go run ./internal/gofmtcheck` and `go test ./...` pass; `internal/cli` finishes under
  the default timeout.

## Checks

- `go run ./internal/gofmtcheck`
- `go test ./internal/reviewtransaction -count=1`
- `go test ./... -count=1`

## Progress

- Baseline: 608 s wall; `internal/cli` FAIL (timeout 10 m); `internal/reviewtransaction` 272 s.
- Delivery strategy: single-pr (single maintainer, no chained PRs).
- T1 implemented (uncommitted, pending parent review): `repository_identity_cache.go` caches the root,
  directories and object-format answers, keyed by path plus a digest of the sanitized Git environment.
  A directories answer is stored only after a lease built from it passed every check. On a hit the full
  lease checks run again, and a failure falls back to Git. RED observed first (7 rev-parse on a warm repo,
  want 0). Observed: `gofmtcheck` ok; `internal/reviewtransaction` ok in 156 s (was 272 s); `internal/cli` ok
  in 326 s under `-timeout=20m` (was a 600 s timeout FAIL).
- Test updates: the exact child count in `TestPreparedCandidateInspectorReusesOneSetupForFourReads` dropped
  from 12 to 9. The `TestAssessTargetStatusPropagatesOperationalAuthorityFailures` subtests clear the caches,
  so their injected `--git-common-dir` failure is still reached.

- T2 observed: `go test -count=1 ./...` 334.6 s wall (was 608.5 s), 0 failing packages, default timeout.
  `internal/cli` 332 s (was 600 s FAIL), `internal/reviewtransaction` 166 s (was 272 s). user+sys CPU 413 s (was 831 s).
- Parent spot check: cache tests re-run ok. RDD is disabled in this clone, so no native review ran.

## Next step

Feature complete. Remaining lever, not started: `t.Parallel` in `internal/cli` and
`internal/reviewtransaction` (blocked by `Setenv`/`Chdir` use); `internal/cli` alone is still ~330 s serial.
