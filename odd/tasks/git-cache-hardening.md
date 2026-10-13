# Harden the review Git answer caches

## Objective

Close the four gaps both Judgment Day judges agreed on for the caches added in
`odd/tasks/test-speed.md` (`7f4e1507`) and `odd/tasks/tree-diff-cache.md` (`76b49686`), plus the
test-coverage concern one judge raised. The verdict was APPROVED (no CRITICAL); these are
WARNING-level hardening items.

Ledger: target `c7f20dd74788fc69555cb40d93d6df82eb1d0ee4afe00e6173cf3619e40cdc32`
(`git show 7f4e1507 76b49686 -- internal/`), Engram topic `judgment-day/git-answer-caches`.

## Findings to close

- I1 — An object-answer hit is validated only by `os.SameFile` on the objects directory, so pruned
  objects, removed alternates, or a re-created repository reusing the inode return a cached answer
  where Git would fail. `ValidateEvidence` no longer re-proves that the tree is readable (its
  ls-tree count went from 1 to 0).
- I2 — The directories cache stamps `config`, `config.worktree` and `commondir` after `rev-parse`
  (`repository_locator.go` ~790), the opposite of `repositoryObjectFormat`'s stamp-before rule.
- I3 — The root cache does not bind `.git/config` / common `config` or global/system Git config
  (`safe.directory`, `core.worktree`). Root-only callers such as `DiscoverUnignoredUntracked` have
  no lease check behind them.
- I4 — Caches are bounded by entry count (256), not bytes. Object outputs can be 8–64 MiB, and an
  overflow clears the whole map.
- T (one judge) — `TestPreparedCandidateInspectorReusesOneSetupForFourReads` now measures a warm
  cache (12 → 6), so a regression that re-runs setup per read could pass.

## Scope

`internal/reviewtransaction` caches and their tests only. No new caching and no behaviour
change outside the cache paths.

## Tasks

- [x] T1 — Close I1–I4 and T. Route: delegated (writer; 2+ non-trivial files, preparation reading).

## Acceptance criteria

- Evidence validation re-proves the trees with Git (no cached answer).
- A config edit landing during the Git query can only make an answer look stale, never fresh.
- A change to repository, global or system Git config invalidates root, directories and
  object-format answers.
- Cached bytes are bounded; one oversized answer is not cached.
- The prepared-inspector test fails if setup runs once per read, even with the caches present.
- `go run ./internal/gofmtcheck` and `go test ./...` pass; suite time stays near 304 s.

## Checks

- `go run ./internal/gofmtcheck`
- `go vet ./internal/reviewtransaction`
- `go test ./internal/reviewtransaction -count=1`
- `go test ./... -count=1`

## Progress

- Delivery strategy: single-pr (fast-forward to main, as previous features).
- T1 implemented (delegated writer), not committed:
  - I1: `ValidateEvidence` marks its context with `withReviewGitObjectRecheck`; object-answer
    loads skip the cache under it (stores still refresh). ValidateEvidence ls-tree pin back to 1.
  - I2/I3: `captureReviewGitConfigStamps` stamps global/system config (paths from
    `git var GIT_CONFIG_GLOBAL|GIT_CONFIG_SYSTEM`, once per Git environment), `commondir`,
    `config.worktree` and the common `config` before Git is asked. Root answers predict the
    repository by Git's discovery walk and cache only when Git answered that root; directory
    answers cache only when Git resolved the predicted git/common dirs. Object format adds the
    global/system stamps. Stamps follow symlinks; devices compare identity only.
    Limit (shortcut comment): include.path/includeIf files are not stamped; Git < 2.42 caches nothing.
  - I4: each cache holds at most 64 MiB; one answer above 16 MiB is not cached; overflow clears.
  - T: the prepared-inspector test clears the caches before counting; pin 10 (RED with setup
    forced per read: 14).
  - RED observed before implementation for I1, I2, I3 (all three edit kinds), the ls-tree pin, T.
  - Checks: gofmtcheck ok; vet ok; `go test ./internal/reviewtransaction` ok (143 s);
    `go test ./internal/cli` ok (302 s, baseline ~264 s; validation now always spawns Git).

- Parent check: targeted cache tests re-run ok. Full suite `go test -count=1 ./...` 312.4 s wall
  (was 304.2 s), 0 failing packages; `internal/cli` 311 s. The +8 s is I1's re-proof in
  `ValidateEvidence`, kept on purpose: that function exists to re-prove evidence with Git.
- RDD is disabled in this clone, so no native review ran.

## Next step

Fast-forward to main (user decision). Then `t.Parallel` in `internal/cli`.
