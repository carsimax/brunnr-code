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
| T3 | S5 | inline | pending | |
| T4 | S6 | inline | pending | |
| T5 | S7 | inline | pending | |
| T6 | S8 | inline + independent verifier (high risk) | pending | |
| V  | S1-S8 | delegated independent verifier | pending | |

Each task: `go run ./internal/gofmtcheck`, `GOOS=darwin GOARCH=arm64 go build ./... && go vet ./...`, tests of touched packages; full `go test ./...` at the end.

## Log

- **L1 (2026-10-13, user, verbatim):** "necesito que planifiquemos un cambio, como premisa esta herramienta actualmente es de uso personal. asi que necesito un plan para iminar el soporte a otros sistemas operativos, solo para Mac. primero explora y despues dime el plan"
- **L2 (2026-10-13):** Explorer map: releases ship darwin+linux only; Windows lives in 23 tagged + 4 untagged files, `windows-runtime` job, `windows-full-suite.yml`, organic-runtime matrix; Linux in 6 reviewtransaction files, Docker distro e2e, `internal/system` distro detection; `PlatformProfile` has ~39 consumers. Darwin builds the `unix`/`!windows` files and `x/sys/unix`, which stay.
- **L3 (2026-10-13, user, verbatim):** "Solo Apple Silicon"
- **L4 (2026-10-13, user, verbatim):** "ok" (authorizes implementation of the six-phase plan).
- **L5 (2026-10-13):** T1 done. Advisory (pre-existing, out of scope): `scripts/test-review-contract-package.sh` fails its contract inventory assertion (`review-acknowledged.schema.json` not in the expected list) before reaching the platform check; CI does not run it.
- **L6 (2026-10-13):** T2 done. Removed `windows-full-suite.yml`, the `windows-runtime`, `claude-network-none` (Linux Docker netns proof) and `e2e-tests` jobs, `e2e/Dockerfile.*`, `docker-test.sh`, `e2e_test.sh`, `lib.sh`, the Windows CI pin tests in `internal/assets/formatter_ordering_test.go`, and the Docker e2e lines in `CLAUDE.md` and the shipped `branch-pr` skill. The organic PowerShell step became bash; `BRUNNR_CODE_LARGE_WORKSPACE_E2E` now runs on macOS. Follow-ups: the Linux-only Codex transport journey (`organic_runtime_test.go:321`, strace) no longer runs in CI and is removed in T5; branch protection must drop the removed required checks (manual repo setting).
