# Feature: principal-engineer-persona

## Objective

Replace the managed Neutral persona and output style with the Principal Engineer style, and remove Neutral entirely.

## Problem / Why

The Neutral style carries theatrical rules inherited from the old Gentleman persona ("Use CAPS for emphasis", "correct errors ruthlessly"). It has no verification-first engineering guidance. The maintainer wants the Principal Engineer style, trimmed so that it does not duplicate the ODD orchestrator (spec/plan, commits, test-first, delegation).

## Scope

- Claude output style: `output-style-principal-engineer.md` (frontmatter `name: Principal Engineer`), written to `~/.claude/output-styles/principal-engineer.md`. Settings get `"outputStyle": "Principal Engineer"`.
- The Claude residual `CLAUDE.md` block and the generic persona (Codex `AGENTS.md`) are renamed without the `neutral` suffix. The generic persona voice is rewritten to Principal Engineer.
- Legacy cleanup: remove a leftover `~/.claude/output-styles/neutral.md` on install, sync and uninstall.
- Tests, goldens, e2e script and stale comments are updated.

## Constraints

- Language rule: reply in the language of the user's latest request, with a neutral register (tú, no regional slang). Artifacts default to English.
- No real names and no sources section in the style.
- `language_contract_test.go` contracts (anti-drift, neutral/professional wording) are kept or deliberately updated.
- `odd/tasks/*.md` history is left untouched.

## Tasks

- [x] T1: Claude output style. Add the asset, delete `output-style-neutral.md`, update `resources.go`/`inject.go` naming, clean up the legacy `neutral.md`, update tests and goldens. Route: delegated (multi-file writer).
- [x] T2: Persona blocks. Rename `persona-neutral-residual.md` → `persona-residual.md` and `generic/persona-neutral.md` → `generic/persona.md`. Rewrite the generic voice to Principal Engineer and update the residual "Persona Voice" pointer. Update the assets/language-contract tests and goldens. Route: delegated.
- [x] T4: Make the persona component mandatory: remove it from the selectable components in the Custom preset/TUI so it is always installed (user decision 2026-10-12). Route: delegated.
- [x] T3: Update the e2e script (`e2e/e2e_test.sh`) and the stale comments (`sync.go`, `presets.go`). Route: inline or delegated, based on size.

## Acceptance criteria

- `go run ./internal/gofmtcheck` passes.
- `go test ./...` passes.
- `git grep -i 'neutral.md\|Neutral Output Style\|persona-neutral'` outside `odd/` and the legacy-cleanup code returns nothing.

## Delivery

Strategy: `single-pr` (single maintainer, no chained PRs). Forecast: ~400–600 authored lines, mostly the asset rewrite and test updates.

## Progress

- Exploration done (mapping agent).
- T1 done (delegated writer). Asset gained verbatim language-contract rules required by `language_contract_test.go` (notification/subagent language preservation, English-only replies); accepted as behavioral, not tone. Deleted `TestReconciledStylesCarryAllMovedPersonaRules` (pinned removed Neutral wording). Legacy `neutral.md` cleanup: `persona.LegacyOutputStylePaths`, removed on inject/uninstall, included in sync/install/upgrade backups.
  - Checks: gofmtcheck ok; go test components/assets/update ok; go test ./internal/cli full ok (624s); go vet ok. Docker e2e not run.
- T2 done (delegated writer). Renamed residual/generic persona assets, `managedPersonaContent`, generic persona rewritten to Principal Engineer voice (87 lines). No tests deleted; parity test now requires "Concepts before code" and "Weakening the verifier is the one unforgivable move."
  - Checks: gofmtcheck ok; go vet ok; go test components/assets/model/tui ok; go test ./internal/cli full ok (595s).
- T4 done (delegated writer). Single rule `model.WithMandatoryComponents` applied in presets, `cli.normalizeComponents`, `cli.RestorePersistedSelection`; TUI picker uses `catalog.SelectableComponents()` (excludes persona). `uninstall --component persona` still removes it; next install/sync restores it.
  - Exposed pre-existing bug: persona merged Claude `settings.json` with a comment-dropping merge. Now uses `filemerge.MergeJSONCObjects`; uninstall removes `outputStyle` comment-preserving via `RemoveJSONCMembers`.
  - Known limitation: if a comment is attached right before `outputStyle`, uninstall leaves the key (same policy as retained hooks) rather than deleting the user's comment.
  - Checks: gofmtcheck ok; go vet ok; go test components/app/catalog/model/planner/tui ok; go test ./internal/cli full ok (557s).
- T3 done (inline). e2e script wording updated; custom-preset tests now assert the mandatory persona (matches real `install --dry-run` output: `persona` / `persona,engram,skills`). Stale `sync.go`/`presets.go` comments were already fixed in T1/T2.
  - Checks: gofmtcheck ok; `go test ./... -count=1` ok (no FAIL). `bash -n e2e/e2e_test.sh` ok. Docker e2e not run.
- Feature complete. Commits: T1 0423fc22, T2 112ad3bf, T4 4f3b0846, T3 (this commit). Next: maintainer decides merge/push.
