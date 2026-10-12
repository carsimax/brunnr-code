# Feature: remove-sdd-remnants

Locator: `odd/tasks/remove-sdd-remnants.md` · Engram mirror: `odd/remove-sdd-remnants/tasks` · Branch: `refactor/remove-sdd-remnants`

## Objective

Remove every remnant of the retired SDD (Spec-Driven Development) workflow, which ODD replaced, except frozen contract text.

## Why

SDD was retired upstream, but its skill IDs, mode plumbing, model-phase tables, lane keys and retirement machinery remain. Some of it is still reachable: `--skills sdd-init` passes validation and the agent builder lists `sdd-*` skills, which are then silently dropped at install.

## Decisions

- **Full removal including retirement machinery (user, 2026-10-12):** nothing uses SDD. Verified on the maintainer's machine: no `sdd-*` skills under `~/.claude`, `~/.codex` or `~/.agents`, no Codex `sdd-*` profiles, no `sdd-orchestrator` marker, `~/.brunnr-code` has no `state.json`, `~/.gentle-ai/state.json` has no `sdd` keys, no `sdd-runtime` dirs. Consistent with the clean break in `brunnr-code-rename.md`: no fallback reads or migrations for upstream installs.
- **Codex lane keys** `sdd-strong/mid/cheap` are renamed to ODD names with no state migration (no saved state uses them).
- **Keep:** frozen text under `contracts/` (`sdd_receipt_binding` enum values in review-integration schemas/fixtures) and the scripts/guards that pin it.

## Footprint (explorer, 2026-10-12)

92 Go files match `SDD|Sdd|sdd[A-Z]`; 84 files match `\bsdd\b|sdd[-_]|spec[- ]driven`.

## Tasks

Known base failure: `TestRunSyncPreservesUserEditedLegacyClaudeCommandWithoutReinstallingSDD` (self-contradictory; removed in T3).

Each task: `go run ./internal/gofmtcheck`, `go build ./... && go vet ./...`, touched-package tests; full `go test ./...` at the end; Conventional Commit on the branch. RED/GREEN not applicable (removal); proof is the build plus updated tests. RDD is disabled in this clone.

- [x] **T1 — SDD skills.** Remove the 11 `SkillSDD*` constants, the catalog entries (category `sdd`), `IsSDDSkill` filters, agent-builder listing and tests. Route: delegated (writer). Done in the `refactor: remove retired SDD skill IDs` commit (12 files, +9/−90); `--skills sdd-*` is now rejected as unsupported; `state_test.go` sample IDs switched to `go-testing`. Verified: gofmtcheck, build, vet, touched packages ok; `internal/cli` ok except `TestRunSyncPreservesUserEditedLegacyClaudeCommandWithoutReinstallingSDD`, which fails on the base too (it writes `commands/sdd-init.md`, then asserts that path is absent) and is deleted in T3.
- [x] **T2 — SDD component and mode plumbing.** `ComponentSDD`, `SDDModeID`, `Selection.SDDMode`, state `sdd_mode`, `normalizeSDDMode`, install/sync `SDDMode` flags, `legacySDD`, `HasSDD`, `sddSubAgentPaths`, planner node/edge, no-op branches in run/sync/app, `activeComponents` `sdd` filter. Route: delegated (writer). Done in `ecc46ef1` (27 files, +112/−943). Removing `ComponentSDD` forced the uninstall `sdd` refusal and `install_claude_command_namespace_test.go` (including the self-contradictory base failure) into this commit; planner order tests now use `ComponentPermission` as a synthetic dependency.
- [x] **T3 — Retirement machinery.** Uninstall `sdd` refusal and `sdd-*` skip, skillregistry `sdd-` exclusion, agentguidance `sdd-orchestrator` marker conversion, Codex `SddProfilePaths` and Engram no-touch rule, `sdd-runtime` kept on store reset, assertion-only tests (`sdd_retirement_collateral_test.go`, compatibility/command-namespace/e2e/assets guards). Route: delegated (writer). `sdd_retirement_collateral_test.go` also covered shared-reference and uninstall behavior, so it was renamed to `shared_references_test.go` with only the SDD checks dropped; `verificationComponentPaths` went with `SddProfilePaths`; legacy marker tests were converted to plain orchestrator-insertion tests. Done in `28a7f717`; full `go test ./...` green.
- [ ] **T4 — Model tables and lane keys.** `sdd-*` phases in Claude presets and the TUI picker cleanup, Codex tier phase labels, lane keys `sdd-strong/mid/cheap` renamed to ODD names, TUI carry-forward of saved `sdd-*` choices. Route: delegated (writer).
- [ ] **T5 — Capability manifest, markers, comments, fixtures.** `SDDProposalFacts`/JSON `"sdd"` (only if no frozen contract pins it), `__managed_by` `brunnr-code/sdd`, stale comments, `sdd-*` sample keys in test fixtures, final grep sweep. Route: delegated (writer).

## Progress

Branch created from `9cb3a35b`. T1-T3 done. Next: T4.
