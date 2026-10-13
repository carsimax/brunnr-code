# Feature: audit-remediation

## Objective

Fix the 20 findings from the whole-repo audit of 2026-10-12, then triage the 12 minor findings left out of that report.

## Problem / Why

A whole-repo audit (6 read-only Sonnet auditors, parent spot checks) found user-config data loss in the `settings.json` merge, a review capture with no deadline, unquoted paths in commands the agent is told to run, a rollback that stops at the first error, and about 2,900 lines of dead code left from removed agents, presets and the upstream release pipeline. The six "must" findings were confirmed by the parent against the code; the rest come from the auditors' reading.

## Scope

- T1–T6: must-fix bugs and risks.
- T7–T17: should-fix bugs, risks and missing guards.
- T18–T20: lean deletions.
- T21: triage of the 12 minor findings (fix, accept, or drop each one).

## Out of scope

- Uninstall deletion logic, contracts v1/v2 drift and the unread parts of `reviewtransaction`, `review_facade.go` and `internal/tui` (not covered by the audit).
- New features.

## Constraints

- Each behavior fix gets RED first when a deterministic test is possible.
- Prefer deletion. No new layers or config.
- Artifacts in English. Guards are ratchets.
- `internal/assets/generic/` and `contracts/` stay.

## Open decisions (ask before the task starts)

- T6: lock the whole sync/install run, or accept the race (single user).
- T17: skills become ledger-managed, or are documented as installer-owned.
- T18: delete `providercontractbundle` with its drift test, or delete only the `cmd`.

## Tasks

### Must fix

- [x] T1: `settings.json` malformed or JSONC is rewritten as only `permissions`. Use `filemerge.MergeJSONCObjects` in `components/permissions/inject.go:98`; drop the malformed-as-`{}` swallow in `filemerge/json_merge.go:11-19` and its pinning test (`json_merge_test.go:120`); confirm `agents/claude/adapter.go` and `engram/inject.go:472` still hold.
- [x] T2: user `permissions.deny` is replaced by the managed list. In `mergeObjectScope` (`filemerge/json_merge.go:905`), when base and overlay are both arrays, keep base entries and append missing overlay entries. Test with a user deny entry.
- [x] T3: lens and correction-plan captures run with `context.Background()` (`cli/review_artifact.go:192`, `cli/review_correction_plan_capture.go:48`). Wrap in `WithTimeout(reviewProviderRoleCaptureTimeout)`; set `command.WaitDelay` in the Claude and Codex adapters.
- [ ] T4: stop-hook prints `--cwd <root>` unquoted (`cli/review_stop_hook.go:294`). Use `pathquote.ShellWord(root)`. Test with a path containing a space and `;`.
- [ ] T5: `backup.Restore` returns on the first failing entry (`backup/restore.go:310`, `:371`), also during rollback. Collect with `errors.Join` and continue. Test with an unrestorable entry followed by a restorable one.
- [ ] T6: two concurrent syncs: the loser's rollback reverts the winner's files (`statecoord/statecoord.go:27`, `cli/sync.go:1273-1291`, `cli/run.go:259-285`). Decision first (see Open decisions).

### Should fix

- [ ] T7: TOML header with a trailing comment is not matched, so a duplicate table is appended and Codex fails to parse (`filemerge/toml.go:40,99,193,239`). Strip a `#...` outside quotes before comparing.
- [ ] T8: corrupt `state.json` blocks every `review mode` verb with a raw JSON error (`cli/review_mode.go:440-455`). Wrap with path and repair hint; skip the global read in `--scope clone`.
- [ ] T9: `pathquote.Quote` lets `$`, backtick and `$(...)` expand in printed executable commands (`compact_reconcile.go:148`, `compact_reclaim.go:206,211,221`, `authority_disposition_execute.go:643-663`, `cli/review_mode.go:231-236,296`). Switch executable lines to `pathquote.ShellWord`; delete `quotePOSIXShellToken` (`review_mode.go:272`).
- [ ] T10: `restore` overwrites current files with no pre-restore snapshot (`cli/restore.go:143`). Snapshot the manifest's original paths to `pre-restore-<ts>` first.
- [ ] T11: install state is persisted before `recordCodexServiceTier`, so a later failure leaves `state.json` ahead of rolled-back files (`cli/run.go:272-285`, `cli/sync.go:1278`). Fold the tier into the single state write.
- [ ] T12: `persistAssignments` returns `nil` on a corrupt `state.json`, so the TUI reports success (`app/app.go:810`). Return the error.
- [ ] T13: `.github/workflows/ci.yml` has no `permissions:` block. Add `permissions: {contents: read}` and `persist-credentials: false` on checkouts.
- [ ] T14: store lock retry pasted five times; commit paths fail immediately on contention (`reviewtransaction/compact_reviewer_capture.go:517,661,939`, `compact_unachievable_lens.go:63,176`, `compact_store.go:1559`, `compact_abandon.go:262`). One `acquireLocalStoreLockWait(ctx, path)` helper for all seven; check `cli/review_operation_contract.go:817-860` still classifies the timeout as no-mutation.
- [ ] T15: `terminal-consumption` tombstones survive `review reset` (`reviewtransaction/store_reset.go:128-165`). Add the category to `storeResetRemovableTargets`.
- [ ] T16: Codex reviewer failure embeds all stderr in the error (`reviewerprovider/codex_adapter.go:93`). Use `reviewerTransportFailureDetail` with a byte cap.
- [ ] T17: shipped skills are overwritten with no ownership check (`components/skills/inject.go:154`). Decision first (see Open decisions).

### Lean

- [ ] T18: `internal/providercontractbundle` and `internal/providercontractbundlecmd` have no references outside themselves (~1,420 lines). Decision first (see Open decisions).
- [ ] T19: delete dead code:
  - `internal/filecoord` (no importers, ~600 lines with tests); inline `cli/install_state_lock.go`.
  - `installcmd.ResolveComponentInstall`, `ResolveDependencyInstall`, `resolveEngramInstall` (~55 lines).
  - `engram/download.go:599-706` unverified download helpers (~108 lines).
  - `persona/inject.go:91-125` unreachable `StrategyFileReplace` branch and `preserveManagedSections` (~55 lines).
- [ ] T20: delete leftovers from removed agents and presets:
  - `update/upgrade`: `strategyOutcome`, `runStrategyWithOutcome`, both `ExitRequested` fields and their TUI branches (~45 lines); `enumerateFilesInDir`, `backupExcludeSubdirs` and tests (~110 lines + tests).
  - `system/install_deps.go:91-200` `InstallCommandsForDep` and helpers (~110 lines + tests).
  - `doctor.Remedy` unused fields and types (~70 lines).
  - `agentbuilder`: `reH2Section`, `RemoveByName`, `MockEngine` out of production, unused `Install` param; `tui/model.go:3215` fake timeout; move `custom-agents.json` under `~/.brunnr-code` (~25 lines).
  - `reviewtransaction/store.go:178-337` v1 `append` and `WriteReceiptAtomic` into a test helper (~170 lines).
  - one `backup.List(root)` instead of `app/app.go:913`, `cli/restore.go:218` and `backup.listManifests` (~40 lines).
  - `compact_abandon.go:381-390` dead branch; `cli/sync.go:1308-1324` dead `shouldWrite` branch; `RuntimeContextBudget`; `store_lock.go:316-364` near-copy walk; triplicated anon-retry in `engram/download.go:208-244`; duplicated `osReadFile`/`readFileOrEmpty`/`mergeJSONFile`.

### Minor findings triage

- [ ] T21: for each item, fix, accept with a `shortcut:` comment, or drop with a reason recorded here.
  1. Backup archives and manifests are written 0644 in 0755 dirs, yet may hold tokens (`backup/compression.go:34`, `snapshot.go:33`, `manifest.go`).
  2. A failed snapshot leaves an orphan dir that is never pruned (`backup/snapshot.go:33-73`).
  3. Claude reviewer lacks `--strict-mcp-config`, so user MCP servers may start per lens (`reviewerprovider/claude_adapter.go:38-43`; unverified live).
  4. Codex lens reviewer's read-only boundary is only `--sandbox read-only`; absolute paths stay readable (`reviewerprovider/codex_adapter.go:108-124`).
  5. `probeScratchParent` compares paths as strings on case-insensitive APFS (`reviewerprovider/probe_scratch.go:30`); use `pathidentity.Contains`.
  6. `review mode <enable|disable> --expected-revision` is silently ignored in global scope (`cli/review_mode.go:76-95,403-405`).
  7. Any GitHub 403 is reported as rate limit; the token follows the `Link` header host unchecked (`update/github.go:128-155,196-206`).
  8. `detectNpmWritable` has no timeout and a loose home-prefix check (`system/detect.go:112-118`).
  9. `gofmtcheck` fails on a tracked file deleted from the worktree (`gofmtcheck/main.go:18-45`).
  10. Stop-hook record write error returns non-zero after the block JSON was printed; `recordReviewStopHookReminder` returns an unused bool (`cli/review_stop_hook.go:243,394-420`).
  11. `install --agent ,` parses to an empty list and takes the full-install overwrite path (`cli/run.go:266`).
  12. The 2s store-lock budget can be outlasted by git work under the lock on big repos (`reviewtransaction/compact_store.go:39,1620`).
  13. Found during T2: `uninstall` removes the whole `permissions` key from `~/.claude/settings.json` (`components/uninstall/service.go:647`), including the user's own `allow`, `ask` and `deny` rules.

## Acceptance criteria

- `go run ./internal/gofmtcheck` passes.
- `go test ./... -timeout 28m` passes.
- Each fixed finding has a test that fails without the fix, or a recorded exception.
- `git grep` for each deleted symbol outside `odd/` returns nothing.

## Delivery

Strategy: one PR per task into an integration branch, then one PR to `main` (user decision 2026-10-12, overrides the no-chained-PR convention for this feature). Forecast: ~3,500 authored lines, mostly deletions (T18–T20).

- Integration branch: `fix/audit-remediation`, cut from `main`. Its PR to `main` stays draft/no-merge until every task PR is merged into it.
- Task branches: `fix/audit-remediation-tNN`, each cut from the current tip of `fix/audit-remediation` and targeting it. Tasks are independent, so every task PR targets the integration branch directly, not the previous task.
- Each task PR holds that task's work-unit commits with tests and docs, states its task ID, and lists depends-on / out-of-scope.
- Before each new task, update the task branch from the integration branch so its diff shows only that task.
- This document is updated on each task branch (checkbox, commit IDs, PR link).

```text
main
 └─ fix/audit-remediation   (draft PR → main)
     ├─ fix/audit-remediation-t01  (PR → fix/audit-remediation)
     ├─ fix/audit-remediation-t02  (PR → fix/audit-remediation)
     └─ … t21
```

## Progress

- 2026-10-12: feature document created from the audit report. Integration branch `fix/audit-remediation`, draft PR #1 to `main`.
- T1 done on `fix/audit-remediation-t01`. Route: inline (two small files plus tests). `permissions` merges `settings.json` with `MergeJSONCObjects`, so a malformed file is refused untouched and comments survive; `MergeJSONObjects` no longer treats a malformed base as `{}` (its other callers pre-validate or ignore the error). The full suite exposed a pre-existing `MergeJSONCObjects` bug: a replaced nested value kept its in-document indent and got the key indent again, so each sync added 2 spaces and broke sync idempotence; fixed by compacting and re-indenting the member. RED observed first: `TestInjectClaudeCodeRefusesMalformedSettings`, `TestInjectClaudeCodePreservesSettingsComments`, `TestMergeJSONCObjectsNestedValueIsIdempotent` (and the three `internal/cli` sync idempotence tests). Verification: `go run ./internal/gofmtcheck` ok; `go test ./... -timeout 28m` ok. Commit `2820d76b`, PR #2, merged into the integration branch.
- T2 done on `fix/audit-remediation-t02`. Route: inline (one file plus test). Fix lives in `permissions` (`withUserDenyRules`), not in `mergeObjectScope`: a global array union would also merge MCP `args` and `hooks` arrays. The user's own `permissions.deny` entries are appended after the managed list, de-duplicated; a second inject is a no-op. RED observed first: `TestInjectClaudeCodeKeepsUserDenyRules` (user rule dropped), then GREEN. New finding recorded as T21 item 13 (uninstall removes the whole `permissions` key). Verification: `go run ./internal/gofmtcheck` ok; `go test ./... -timeout 28m` ok. Commit `000b2d88`, PR #3, merged.
- T3 done on `fix/audit-remediation-t03`. Route: inline (two one-line CLI changes, two one-line adapter changes, one test). Lens and correction-plan captures now run under `reviewProviderRoleCaptureTimeout` (600s), like refuter and validator; both adapters set `command.WaitDelay = time.Second`. RED observed first: `TestProviderLensCaptureRunsUnderADeadline` (no deadline), then GREEN. `WaitDelay` has no dedicated test (one line per adapter). Verification: `go run ./internal/gofmtcheck` ok; `go test ./... -timeout 28m` ok.

## Next step

Create `fix/audit-remediation` from `main` with this document, push it, open the draft PR to `main`; then `fix/audit-remediation-t01` for T1.
