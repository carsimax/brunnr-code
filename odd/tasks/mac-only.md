# Feature: mac-only

Objective: Brunnr-Code is a personal tool. Remove support for every platform except macOS on Apple Silicon (`darwin/arm64`): releases, installer, CI, platform detection, and per-OS code.
Branch: `feat/mac-only`. Delivery: `single-pr` (single-maintainer repo, no chained PRs; mostly deletions, forecast well above 400 changed lines). Test runner: `go test` (`scripts/test-fast.sh` for the full suite). Risk: high (item 5 release/CI/installer, item 2 review security guards in T6).

## Specs

- **S1 — Scope.** User request (verbatim): "necesito un plan para iminar el soporte a otros sistemas operativos, solo para Mac."
- **S2 — Architecture.** User decision (verbatim): "Solo Apple Silicon". Only `darwin/arm64` is built, installed, and supported; Intel Macs (`darwin/amd64`) are dropped.
- **S3 — Distribution.** `.goreleaser.yaml` builds only `darwin/arm64`. `scripts/install.sh` fails with a clear error on any other OS or architecture; an Apple Silicon shell under Rosetta (`uname -m` = `x86_64`, `sysctl -n hw.optional.arm64` = `1`) is treated as `arm64`. README and release target lists say macOS Apple Silicon only.
- **S4 — CI.** Windows jobs/workflows, the Windows organic-runtime matrix entry, and the Linux Docker distro e2e are removed. Unit tests run on macOS (Apple Silicon runners).
- **S5 — Platform guard.** The runtime support check accepts only `darwin` + `arm64`; any other platform gets a clear unsupported message.
- **S6 — Detection model.** `internal/system` drops Linux distro / package-manager detection and Windows PowerShell/PATH code; consumers are simplified.
- **S7 — Stray branches.** Windows/Linux/WSL branches outside `internal/system` (engram download, update detection, filemerge, communitytool, tui, cli, storage, reviewerprovider) are removed.
- **S8 — Review subsystem.** `internal/reviewtransaction` Windows, Linux and other-unix files are removed; darwin keeps its current behavior; unix files left without a twin are folded into their base files.
- **Assumption:** frozen text (`contracts/`, `internal/assets/generic/`, and the embedded skill wording in `internal/assets/skills/issue-creation/SKILL.md:73`, `_shared/review-ledger-contract.md:53`) is left untouched.

## Tasks

| ID | Specs | Route | Status | Evidence |
|----|-------|-------|--------|----------|
| T1 | S2, S3 | inline | done | see commit; installer guard probed with fake `uname`/`sysctl` (5 cases OK); goreleaser snapshot emits only `darwin_arm64` |
| T2 | S4 | inline | done | see commit; ci.yml jobs = go-format, unit-tests (macos-latest), darwin-runtime, organic-runtime-e2e (macos-latest); `go test ./internal/assets/... ./internal/components/...` ok |
| T3 | S5 | inline | done | `8ec3010d` (RED: guard tests failed to build; GREEN) |
| T4 | S6 | inline + delegated writer (context backstop) for test fixups | done | `8ec3010d` |
| T5 | S7 | delegated writer (context backstop) | done | `8ec3010d` (RED: `TestDownloadLatestBinaryFetchesOnlyDarwinArm64Asset`), leftovers `see commit` |
| T6 | S8 | delegated writer in worktree (parallel with T4/T5) + independent verifier | done (verifier pending) | `64198ace` |
| V  | S1-S8 | delegated independent verifier | done | S1-S8 PASS, no blockers; advisory (rollback row) fixed in `ad391d4c` |

Each task: `go run ./internal/gofmtcheck`, `GOOS=darwin GOARCH=arm64 go build ./... && go vet ./...`, tests of touched packages; full `go test ./...` at the end.

## Log

- **L1 (2026-10-13, user, verbatim):** "necesito que planifiquemos un cambio, como premisa esta herramienta actualmente es de uso personal. asi que necesito un plan para iminar el soporte a otros sistemas operativos, solo para Mac. primero explora y despues dime el plan"
- **L2 (2026-10-13):** Explorer map: releases ship darwin+linux only; Windows lives in 23 tagged + 4 untagged files, `windows-runtime` job, `windows-full-suite.yml`, organic-runtime matrix; Linux in 6 reviewtransaction files, Docker distro e2e, `internal/system` distro detection; `PlatformProfile` has ~39 consumers. Darwin builds the `unix`/`!windows` files and `x/sys/unix`, which stay.
- **L3 (2026-10-13, user, verbatim):** "Solo Apple Silicon"
- **L4 (2026-10-13, user, verbatim):** "ok" (authorizes implementation of the six-phase plan).
- **L5 (2026-10-13):** T1 done. Advisory (pre-existing, out of scope): `scripts/test-review-contract-package.sh` fails its contract inventory assertion (`review-acknowledged.schema.json` not in the expected list) before reaching the platform check; CI does not run it.
- **L6 (2026-10-13):** T2 done. Removed `windows-full-suite.yml`, the `windows-runtime`, `claude-network-none` (Linux Docker netns proof) and `e2e-tests` jobs, `e2e/Dockerfile.*`, `docker-test.sh`, `e2e_test.sh`, `lib.sh`, the Windows CI pin tests in `internal/assets/formatter_ordering_test.go`, and the Docker e2e lines in `CLAUDE.md` and the shipped `branch-pr` skill. The organic PowerShell step became bash; `BRUNNR_CODE_LARGE_WORKSPACE_E2E` now runs on macOS. Follow-ups: the Linux-only Codex transport journey (`organic_runtime_test.go:321`, strace) no longer runs in CI and is removed in T5; branch protection must drop the removed required checks (manual repo setting).
- **L7 (2026-10-13):** T6 (S8) by a delegated writer. Deleted 33 files in `internal/reviewtransaction`, `internal/reviewerprovider`, `internal/storage` (every `*_windows*`, `*_linux*`, other-unix and aix/freebsd/solaris file, the untagged `rar_windows_*` and `windows_batch_cmdline*` files, the no-op `claude_adapter_posix.go`, and the now-unused `storeLockCoordinationBytes`). Narrowed 16 `!windows`/`unix` constraints to `//go:build darwin`; folded `rar_path_safety_fuse_other.go` into `rar_path_safety_unix.go`. Removed the `reviewRuntimeGOOS` seam, the `runtime.GOOS == "windows"` locator mode bypass, the Windows/WSL wording in the RAR refusal messages, and Windows-only test skips/branches. Darwin logic unchanged. Checks: gofmtcheck, build, vet, `darwin-release-blockers.sh verify` (39 entries), reviewerprovider/storage/reviewtransaction tests.
- **L8 (2026-10-13):** T3-T5 committed as one commit `8ec3010d` (their edits interleave across packages). Guard: only darwin/arm64 (`ErrUnsupportedPlatform`). Dry-run line is now `os=<os> status=<status>`. Engram downloads only `engram_<v>_darwin_arm64.tar.gz`. `system.AddToUserPath` is process-PATH only. T6 cherry-picked as `64198ace`.
- **L9 (2026-10-13):** Found that main CI has been red since `5629b5f1` (#36): the "Authorized work continuity" section (1069 bytes) was added to the orchestrators without regenerating `internal/components/agentguidance/testdata/orchestrator-module-baseline.json`. The module baseline/budget tests were Linux-only; they now run on darwin, and the baseline was regenerated with `-update` (only the claude-code and codex lines changed; the drift is that intended section). Removed the dead Windows skips in `internal/backup`, `internal/statecoord`, `internal/skillregistry` tests. Full checks: gofmtcheck, `go build ./... && go vet ./...`, `go vet -tags real_agent_e2e ./e2e/organicruntime`, `scripts/test-fast.sh` all PASS.
- **L10 (2026-10-13):** Independent verifier: S1-S8 PASS, no blockers. Its advisory (deleting the no-op `compatibilitySkillsRefreshStep.Rollback` dropped that step's row from a failed run's rollback progress, an unrequested output change) fixed in `ad391d4c` (RED: `TestCompatibilitySkillsRefreshStepKeepsRollbackRow`).
- **L11 (2026-10-13):** RDD: assessed high risk; user chose "Revisar este cambio". START for the whole branch (`5629b5f1..b427d71f`, 245 files, 15561 lines) stopped with `lens_context_budget_exceeded`: no authority created. Next step needs the user: review smaller candidates (per commit) or skip.
- **L12 (2026-10-13, user choices verbatim):** "Revisar por commit (Recommended)" -> START for `64198ace` alone (69 files, 4413 lines, medium) was granted ("Revisar este cambio") and also stopped with `lens_context_budget_exceeded`; no authority created. Then "Cerrar sin revisión (Recommended)": the feature closes without native review; the darwin-affecting changes were checked line by line by the independent verifier (S8 PASS). Pending: PR (user decision) and removing the deleted jobs from branch protection's required checks (manual).
