# Feature: brunnr-code-rename

Locator: `odd/tasks/brunnr-code-rename.md` · Engram mirror: `odd/brunnr-code-rename/tasks` · Branch: `feat/brunnr-code-rename`

## Objective

Rebrand the fork from Gentle AI / carsi-ai to **Brunnr-Code**: binary and CLI `brunnr-code`, Go module `github.com/carsimax/brunnr-code`.

## Why

`TRADEMARKS.md` requires forks to use a distinct primary name; `gentle-ai` is a covered mark including package and CLI identifiers. The GitHub repository is being renamed to `carsimax/brunnr-code`.

## Decisions

- **Clean break (user, 2026-10-11):** rename state dirs, schema IDs, section markers, hooks and env vars with no fallback reads, aliases or migration of existing `gentle-ai`/`carsi-ai` installs. The user reinstalls.
- Keep: MIT `LICENSE` upstream copyright line; Engram name (external dependency).
- **Delete `legacyassets` (user, 2026-10-12):** the package only recognizes and retires old upstream `gentle-ai` installs, which the clean break no longer supports; same for `StripLegacyATLBlock`. Supersedes the earlier "keep legacyassets digests" note.
- README may state nominatively that Brunnr-Code is based on Gentle AI.
- **Removals (user, 2026-10-11):** telemetry entirely (code, `cmd/gentle-telemetry`, `deploy/telemetry`, frozen telemetry contracts, Claude telemetry hook); beta/prerelease channels (`GENTLE_AI_CHANNEL`, `-rc`/`-main` builds); `brunnr-code` self-update (update check/upgrade, minisign verification, advisory, Homebrew tap) while Engram updates stay; GGA entirely; the Gentleman persona and persona selection (Neutral only). Removals run before the rename tasks so removed code is never renamed.

## Footprint (explorer, 2026-10-11)

`gentle-ai` appears ~6582 times in 1043 files. ~600 Go files import the module path. ~190 `gentle-ai.*/vN` schema IDs, ~125 `GENTLE_AI_*` env vars, `~/.gentle-ai` and `<git-common-dir>/gentle-ai/` state dirs, `<!-- gentle-ai:ID -->` section markers, Claude hook commands.

## Tasks

Each task: `go build ./... && go vet ./...` plus touched-package tests; Conventional Commit on the branch. Known base failures: none (the former `internal/update` release-security failures were deleted with the upstream release scripts in T1).

- [x] **T1 — carsi → brunnr-code release surface.** README, CLAUDE.md, carsi goreleaser/workflow/install script, `CARSI_AI_*` vars; remove upstream release config, workflows and installers. Route: delegated (writer). Done in `71f620c7` (24 files, +168/−3920); deleted upstream release scripts and their tests (`install_script_test.go`, `windows_distribution_policy_test.go`, `release_security_test.go`).
- [x] **T2 — Module path.** `github.com/carsimax/brunnr-code` (root) and `.../bench`; imports, ldflags, scripts. Route: delegated (writer). Done in `ef87cb8b` (599 files, mechanical); `/v4` suffix dropped.
- [x] **T3 — Binary.** `cmd/gentle-ai` → `cmd/brunnr-code`; goreleaser, workflows, scripts. Route: delegated (writer). Done in `8241dfd2`. Verified: build/vet root+bench, deadcode ratchet, `test-review-capabilities.sh`, full suite only failing `TestIssueCreationAuthorityBoundary` (pre-existing since `5563eba0` replaced AGENTS.md; fixed inline by dropping its AGENTS.md routing check).
- [x] **T4a — Remove telemetry.** Route: delegated (writer). Done in `a8c49af7`; deleted `internal/telemetry`, `internal/telemetrycollector`, `internal/components/telemetryruntime`, `cmd/gentle-telemetry`, `deploy/`, `contracts/telemetry`, the `telemetry` CLI, Claude/Codex telemetry hooks and the j4395 bench journey; dead `model.CodexTierGroups` removed (invariant test moved internal).
- [x] **T4b — Remove prerelease channels and `brunnr-code` self-update** (keep Engram updates); remove dead upstream release packages (`releasepolicy*`, `releaseprovenance*`) and minisign paths if unused. Route: delegated (writer). Done in `dadf5b19`; removed `--channel`/`GENTLE_AI_CHANNEL` (incl. Engram `@main` installs), the gentle-ai registry entry, beta/main-head checks, advisory (and its TUI banner), self-update/PendingSync deferred sync, go-install route, minisign download, `releasepolicy*`/`releaseprovenance*`; `update`/`upgrade` now cover Engram and GGA only. `go mod tidy` also dropped `go-minisign` and the telemetry-only `modernc.org/sqlite`.
- [x] **T4c — Remove GGA.** Route: delegated (writer). Done in `035c239a`; deleted `internal/components/gga`, `internal/assets/gga` (PowerShell/cmd shims, `pr_mode.sh`), `model.ComponentGGA`, `versions.GGAVersion`, installer/sync/uninstall/doctor/TUI wiring, the GGA update registry entry and the script-based upgrade strategy, and GGA e2e cases; uninstall no longer recognizes GGA config.
- [x] **T4d — Remove the Gentleman persona and persona selection.** Route: delegated (writer). Done in `8c8850e6`; deleted Gentleman persona/output-style assets and goldens, `model.PersonaID`, `Selection.Persona`, state `persona`, `--persona`, the TUI persona screen and the alias migration; every managed preset installs the Neutral persona (`TestComponentsForPresetAlwaysInstallsNeutralPersona`); `combined-claude-claudemd.golden` regenerated with `-update`. Full suite after T4d: all packages ok; bench ok.
- [x] **T5 — User-visible prose.** Help, TUI logo, CLI strings and tests. Route: delegated (writer). Done in `d71d304b`; `brunnr-code version`/help/errors/TUI/doctor/uninstall prose renamed, ASCII `BRUNNR-CODE` wordmark, preset `full-gentleman`→`full`, Claude themes `gentleman`/`Gentleman Cute`→`brunnr`/`Brunnr Cute`, legacy Gentleman persona strippers (`StripLegacyPersonaBlock`, exact-asset migration, uninstall fingerprint preamble) deleted with their tests, doctor checks `brunnr-code`, CI image tags renamed, `bench/results.json` regenerated without j4395; refusal ratchet accepts both names until T6.
- [x] **T6 — Commands in assets and hooks**, goldens. Route: delegated (writer). Done in `48318222`; agent assets, goldens, Claude/Codex hook commands, `next_transition`/refusal/defect-report commands (`reviewTransitionCommandTool`, managed-assets fallback), harness binaries (organicruntime, bench shim/product name, crosslane, Dockerfile) now say `brunnr-code`; contract schemas/fixtures that live output is validated against updated for the command word and consent headline only (schema IDs and titles left for T9), pinned contract digests and reviewer-agent hashes refreshed; refusal ratchet requires `brunnr-code`. Mirrored skills `systemic-issue-triage` and `gentle-ai-bench` left for T10 (`skills/` mirror). Capabilities `package.name` stays `gentle-ai` (frozen fixtures, T9). Full suite: all packages ok; bench ok.
- [x] **T6b — Delete `legacyassets` and `StripLegacyATLBlock`** with their tests, testdata, generator script and callers. Route: delegated (writer). Done in `c7482666`; removed install/sync retirement steps and snapshot paths, uninstall `opRetireSDD`, upgrade backup retired paths, retired-path verification checks, legacy slash-command paths from the SDD component inventory, persona ATL healing, `scripts/gen-sdd-agent-digests`, and their tests. RED/GREEN not applicable (removal).
- [x] **T7 — State dirs and section markers.** Route: delegated (writer). Done in `b17aa57e`; `~/.gentle-ai`→`~/.brunnr-code`, `<git-common-dir>/gentle-ai`→`<git-common-dir>/brunnr-code` (new `reviewtransaction.GitCommonDirName` used by every store/mode/reclaim/reset/defect-report/consent path), `.claude/brunnr-code/orchestrator`, `~/.config/brunnr-code`, temp prefixes `.brunnr-code-*`/`brunnr-code-*` (bench temp names left for T10), section markers `<!-- brunnr-code:ID -->`, `__managed_by` `brunnr-code/sdd`; goldens and e2e updated, native-agent install hashes refreshed. No contract fixture pinned these paths. RED/GREEN not applicable (rename).
- [x] **T8 — Env vars** `BRUNNR_CODE_*`. Route: delegated (writer). Done in `27af60e4`; every `GENTLE_AI_*` variable, template placeholder and review prompt delimiter renamed with no aliases (incl. the `status-v8` binding-prompt pattern), native-agent install hashes refreshed, bench sandbox `.gitignore` now ignores `.brunnr-code/` (missed in T7). Full suite: all packages ok; bench ok; `test-review-capabilities.sh` PASS. Leftover: Codex reviewer provider id `gentle_ai_reviewer_loopback`. RED/GREEN not applicable (rename).
- [ ] **T9 — Schema IDs and `gentle-ai.dev` URLs**, `contracts/`, drift tests, bundle digests.
- [ ] **T10 — TRADEMARKS.md, `gentle-ai-bench` skill, bench naming, `.gitignore` entries.**

## Acceptance criteria

- `rg -i 'gentle-ai|gentle ai|gentle_ai|carsi'` returns only intentional hits (LICENSE, nominative README mention, legacyassets digests/fixtures).
- `go build ./... && go vet ./... && go test ./...` green except known base failures; `bench` module builds.
- `brunnr-code` binary installs and runs `install`, `sync`, `review` for Claude Code and Codex.

## Delivery

Sole maintainer: work-unit commits on the branch, merge to `main` by the user. RDD disabled clone-local.

## Next step

T5–T10 (rename).
