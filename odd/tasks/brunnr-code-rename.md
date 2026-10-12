# Feature: brunnr-code-rename

Locator: `odd/tasks/brunnr-code-rename.md` · Engram mirror: `odd/brunnr-code-rename/tasks` · Branch: `feat/brunnr-code-rename`

## Objective

Rebrand the fork from Gentle AI / carsi-ai to **Brunnr-Code**: binary and CLI `brunnr-code`, Go module `github.com/carsimax/brunnr-code`.

## Why

`TRADEMARKS.md` requires forks to use a distinct primary name; `gentle-ai` is a covered mark including package and CLI identifiers. The GitHub repository is being renamed to `carsimax/brunnr-code`.

## Decisions

- **Clean break (user, 2026-10-11):** rename state dirs, schema IDs, section markers, hooks and env vars with no fallback reads, aliases or migration of existing `gentle-ai`/`carsi-ai` installs. The user reinstalls.
- Keep: MIT `LICENSE` upstream copyright line; Engram name (external dependency); `legacyassets` digest tables and historical fixtures (fingerprints of released upstream assets, never regenerated).
- README may state nominatively that Brunnr-Code is based on Gentle AI.
- **Removals (user, 2026-10-11):** telemetry entirely (code, `cmd/gentle-telemetry`, `deploy/telemetry`, frozen telemetry contracts, Claude telemetry hook); beta/prerelease channels (`GENTLE_AI_CHANNEL`, `-rc`/`-main` builds); `brunnr-code` self-update (update check/upgrade, minisign verification, advisory, Homebrew tap) while Engram updates stay; GGA entirely; the Gentleman persona and persona selection (Neutral only). Removals run before the rename tasks so removed code is never renamed.

## Footprint (explorer, 2026-10-11)

`gentle-ai` appears ~6582 times in 1043 files. ~600 Go files import the module path. ~190 `gentle-ai.*/vN` schema IDs, ~125 `GENTLE_AI_*` env vars, `~/.gentle-ai` and `<git-common-dir>/gentle-ai/` state dirs, `<!-- gentle-ai:ID -->` section markers, Claude hook commands.

## Tasks

Each task: `go build ./... && go vet ./...` plus touched-package tests; Conventional Commit on the branch. Known base failures: none (the former `internal/update` release-security failures were deleted with the upstream release scripts in T1).

- [x] **T1 — carsi → brunnr-code release surface.** README, CLAUDE.md, carsi goreleaser/workflow/install script, `CARSI_AI_*` vars; remove upstream release config, workflows and installers. Route: delegated (writer). Done in `71f620c7` (24 files, +168/−3920); deleted upstream release scripts and their tests (`install_script_test.go`, `windows_distribution_policy_test.go`, `release_security_test.go`).
- [x] **T2 — Module path.** `github.com/carsimax/brunnr-code` (root) and `.../bench`; imports, ldflags, scripts. Route: delegated (writer). Done in `ef87cb8b` (599 files, mechanical); `/v4` suffix dropped.
- [x] **T3 — Binary.** `cmd/gentle-ai` → `cmd/brunnr-code`; goreleaser, workflows, scripts. Route: delegated (writer). Done in `8241dfd2`. Verified: build/vet root+bench, deadcode ratchet, `test-review-capabilities.sh`, full suite only failing `TestIssueCreationAuthorityBoundary` (pre-existing since `5563eba0` replaced AGENTS.md; fixed inline by dropping its AGENTS.md routing check).
- [x] **T4a — Remove telemetry.** Route: delegated (writer). Done in the `refactor(telemetry): remove telemetry` commit; deleted `internal/telemetry`, `internal/telemetrycollector`, `internal/components/telemetryruntime`, `cmd/gentle-telemetry`, `deploy/`, `contracts/telemetry`, the `telemetry` CLI, Claude/Codex telemetry hooks and the j4395 bench journey; dead `model.CodexTierGroups` removed (invariant test moved internal).
- [ ] **T4b — Remove prerelease channels and `brunnr-code` self-update** (keep Engram updates); remove dead upstream release packages (`releasepolicy*`, `releaseprovenance*`) and minisign paths if unused.
- [ ] **T4c — Remove GGA.**
- [ ] **T4d — Remove the Gentleman persona and persona selection.**
- [ ] **T5 — User-visible prose.** Help, TUI logo, CLI strings and tests.
- [ ] **T6 — Commands in assets and hooks**, goldens.
- [ ] **T7 — State dirs and section markers.**
- [ ] **T8 — Env vars** `BRUNNR_CODE_*`.
- [ ] **T9 — Schema IDs and `gentle-ai.dev` URLs**, `contracts/`, drift tests, bundle digests.
- [ ] **T10 — TRADEMARKS.md, `gentle-ai-bench` skill, bench naming, `.gitignore` entries.**

## Acceptance criteria

- `rg -i 'gentle-ai|gentle ai|gentle_ai|carsi'` returns only intentional hits (LICENSE, nominative README mention, legacyassets digests/fixtures).
- `go build ./... && go vet ./... && go test ./...` green except known base failures; `bench` module builds.
- `brunnr-code` binary installs and runs `install`, `sync`, `review` for Claude Code and Codex.

## Delivery

Sole maintainer: work-unit commits on the branch, merge to `main` by the user. RDD disabled clone-local.

## Next step

T4a–T4d (removals), then T5–T10.
