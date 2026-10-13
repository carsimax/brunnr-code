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
- [x] T4: stop-hook prints `--cwd <root>` unquoted (`cli/review_stop_hook.go:294`). Use `pathquote.ShellWord(root)`. Test with a path containing a space and `;`.
- [x] T5: `backup.Restore` returns on the first failing entry (`backup/restore.go:310`, `:371`), also during rollback. Collect with `errors.Join` and continue. Test with an unrestorable entry followed by a restorable one.
- [x] T6: two concurrent syncs: the loser's rollback reverts the winner's files (`statecoord/statecoord.go:27`, `cli/sync.go:1273-1291`, `cli/run.go:259-285`). Decision first (see Open decisions).

### Should fix

- [x] T7: TOML header with a trailing comment is not matched, so a duplicate table is appended and Codex fails to parse (`filemerge/toml.go:40,99,193,239`). Strip a `#...` outside quotes before comparing.
- [x] T8: corrupt `state.json` blocks every `review mode` verb with a raw JSON error (`cli/review_mode.go:440-455`). Wrap with path and repair hint; skip the global read in `--scope clone`.
- [x] T9: `pathquote.Quote` lets `$`, backtick and `$(...)` expand in printed executable commands (`compact_reconcile.go:148`, `compact_reclaim.go:206,211,221`, `authority_disposition_execute.go:643-663`, `cli/review_mode.go:231-236,296`). Switch executable lines to `pathquote.ShellWord`; delete `quotePOSIXShellToken` (`review_mode.go:272`).
- [x] T10: `restore` overwrites current files with no pre-restore snapshot (`cli/restore.go:143`). Snapshot the manifest's original paths to `pre-restore-<ts>` first.
- [x] T11: install state is persisted before `recordCodexServiceTier`, so a later failure leaves `state.json` ahead of rolled-back files (`cli/run.go:272-285`, `cli/sync.go:1278`). Fold the tier into the single state write.
- [x] T12: `persistAssignments` returns `nil` on a corrupt `state.json`, so the TUI reports success (`app/app.go:810`). Return the error.
- [x] T13: `.github/workflows/ci.yml` has no `permissions:` block. Add `permissions: {contents: read}` and `persist-credentials: false` on checkouts.
- [x] T14: store lock retry pasted five times; commit paths fail immediately on contention (`reviewtransaction/compact_reviewer_capture.go:517,661,939`, `compact_unachievable_lens.go:63,176`, `compact_store.go:1559`, `compact_abandon.go:262`). One `acquireLocalStoreLockWait(ctx, path)` helper for all seven; check `cli/review_operation_contract.go:817-860` still classifies the timeout as no-mutation.
- [x] T15: `terminal-consumption` tombstones survive `review reset` (`reviewtransaction/store_reset.go:128-165`). Add the category to `storeResetRemovableTargets`.
- [x] T16: Codex reviewer failure embeds all stderr in the error (`reviewerprovider/codex_adapter.go:93`). Use `reviewerTransportFailureDetail` with a byte cap.
- [x] T17: shipped skills are overwritten with no ownership check (`components/skills/inject.go:154`). Decision first (see Open decisions).

### Lean

- [x] T18: `internal/providercontractbundle` and `internal/providercontractbundlecmd` have no references outside themselves (~1,420 lines). Decision first (see Open decisions).
- [x] T19: delete dead code:
  - `internal/filecoord` (no importers, ~600 lines with tests); inline `cli/install_state_lock.go`.
  - `installcmd.ResolveComponentInstall`, `ResolveDependencyInstall`, `resolveEngramInstall` (~55 lines).
  - `engram/download.go:599-706` unverified download helpers (~108 lines).
  - `persona/inject.go:91-125` unreachable `StrategyFileReplace` branch and `preserveManagedSections` (~55 lines).
- [x] T20: delete leftovers from removed agents and presets:
  - `update/upgrade`: `strategyOutcome`, `runStrategyWithOutcome`, both `ExitRequested` fields and their TUI branches (~45 lines); `enumerateFilesInDir`, `backupExcludeSubdirs` and tests (~110 lines + tests).
  - `system/install_deps.go:91-200` `InstallCommandsForDep` and helpers (~110 lines + tests).
  - `doctor.Remedy` unused fields and types (~70 lines).
  - `agentbuilder`: `reH2Section`, `RemoveByName`, `MockEngine` out of production, unused `Install` param; `tui/model.go:3215` fake timeout; move `custom-agents.json` under `~/.brunnr-code` (~25 lines).
  - `reviewtransaction/store.go:178-337` v1 `append` and `WriteReceiptAtomic` into a test helper (~170 lines).
  - one `backup.List(root)` instead of `app/app.go:913`, `cli/restore.go:218` and `backup.listManifests` (~40 lines).
  - `compact_abandon.go:381-390` dead branch; `cli/sync.go:1308-1324` dead `shouldWrite` branch; `RuntimeContextBudget`; `store_lock.go:316-364` near-copy walk; triplicated anon-retry in `engram/download.go:208-244`; duplicated `osReadFile`/`readFileOrEmpty`/`mergeJSONFile`.

### Minor findings triage

- [ ] T21: for each item, fix, accept with a `shortcut:` comment, or drop with a reason recorded here.
  Decision (user, 2026-10-13): fix 1, 2, 3, 5, 6, 8, 9, 10, 11, 13; fix the message of 7 and drop its `Link` host check (GitHub controls that header); accept 4 and 12 with `shortcut:` comments. Delivered as T21a (items 1, 2, 7, 8, 9, 11, 13) and T21b (items 3, 4, 5, 6, 10, 12).
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
- T3 done on `fix/audit-remediation-t03`. Route: inline (two one-line CLI changes, two one-line adapter changes, one test). Lens and correction-plan captures now run under `reviewProviderRoleCaptureTimeout` (600s), like refuter and validator; both adapters set `command.WaitDelay = time.Second`. RED observed first: `TestProviderLensCaptureRunsUnderADeadline` (no deadline), then GREEN. `WaitDelay` has no dedicated test (one line per adapter). Verification: `go run ./internal/gofmtcheck` ok; `go test ./... -timeout 28m` ok. Commit `9a551ac6`, PR #4, merged.
- T4 done on `fix/audit-remediation-t04`. Route: inline (one line plus test). The stop-hook STATUS line renders the root with `pathquote.ShellWord`, like its START line. RED observed first: `TestReviewStopHookReasonQuotesRepositoryRoot` (root `my repo;touch pwned` unquoted), then GREEN. Verification: `go run ./internal/gofmtcheck` ok; `go test ./... -timeout 28m` ok. Commit `af83cea5`, PR #5, merged.
- T5 done on `fix/audit-remediation-t05`. Route: inline (one file plus test). `restoreCompressed` and `restorePlain` repeated the same loop (and an inline copy of `reconcileExistingSymlink`); both now call `restoreManifestEntries`, which restores every entry and returns `errors.Join` of the failures. Net -20 lines. RED observed first: `TestRestoreContinuesPastAFailingEntry` (later entry left modified), then GREEN. Verification: `go run ./internal/gofmtcheck` ok; `go test ./... -timeout 28m` ok.
- T6 decision (user, 2026-10-12): accept the concurrent-sync race (single user, no hook runs sync); document it with a `shortcut:` comment only. Commit for T5 is in PR #6, merged.
- T6 done on `fix/audit-remediation-t06`. Route: inline (comment only). `statecoord.WithLock` carries a `shortcut:` comment naming the limit and when to upgrade. No test (no behavior change). Verification: `go build ./...` ok; `go run ./internal/gofmtcheck` ok. PR #7, merged.
- T7 done on `fix/audit-remediation-t07`. Route: inline (scan moved out of `isTOMLTableHeader` into exported `TOMLHeaderWithoutComment`, used by the four header matches in `toml.go` and the one in `engram/inject.go`). RED observed first: `TestTOMLTableHeaderWithTrailingComment` (`[features] # my flags` got a duplicate `[features]`), then GREEN. Verification: `go run ./internal/gofmtcheck` ok; `go test ./... -timeout 28m` ok. PR #8, merged.
- T8 done on `fix/audit-remediation-t08`. Route: inline (one file plus test). `readGlobalRDDMode` names `state.json` and how to repair it, which every review verb and the kill switch inherit. A clone-scoped `disable` tolerates an unreadable global source (its result is off either way); `enable` still refuses because it could not report a true status. RED observed first: `TestReviewModeSurvivesCorruptGlobalState` (clone disable failed with a raw JSON error), then GREEN. Verification: `go run ./internal/gofmtcheck` ok; `go test ./... -timeout 28m` ok. PR #9, merged.
- T9 done on `fix/audit-remediation-t09`. Route: inline (mechanical replacement in three files plus tests). The nine executable `inspect-authority` / `abandon` lines in `reviewtransaction` use `pathquote.ShellWord`; the three prose path renderings in `cli/review_mode.go` keep `Quote`. `quotePOSIXShellToken` is deleted in favor of `ShellWord`. `path_quote_render_test.go` (issue #2498) now expects the single-quoted shell word, which still keeps the Windows path verbatim. RED observed first: `TestPrintedAbandonCommandQuotesRepositoryAsShellWord` (`$(touch pwned)` inside double quotes), then GREEN. Verification: `go run ./internal/gofmtcheck` ok; `go test ./... -timeout 28m` ok. PR #10, merged.
- T10 done on `fix/audit-remediation-t10`. Route: inline (one file plus a source constant and tests). `brunnr-code restore` snapshots every path of the target manifest into a new backup (`BackupSourceRestore`) before restoring, not pruned there because pruning could delete the target. After success it prints a fixed note pointing to `restore --list`. RED observed first: `TestRunRestore_SavesCurrentFilesBeforeRestoring` (no pre-restore backup), then GREEN. Exact-stdout expectations in `restore_test.go` and `codex_home_boundaries_test.go` include the note. Not covered: the TUI restore (`app.tuiRestore`) does not go through this command and still has no pre-restore snapshot. Verification: `go run ./internal/gofmtcheck` ok; `go test ./... -timeout 28m` ok. PR #11, merged.
- T11 done on `fix/audit-remediation-t11`. Route: inline (structural, three source files). `persistInstallState` and `persistSyncManagedAssetState` take the Codex service tier and apply it inside their single locked state write; `recordCodexServiceTier` (a second lock, read and write) is replaced by `applyCodexServiceTier`. Net -16 lines. Test-first exception: a structural change with no meaningful RED; existing service-tier tests cover the behavior. Verification: `go run ./internal/gofmtcheck` ok (first run caught a blank import line); `go test ./... -timeout 28m` ok. PR #12, merged.
- T12 done on `fix/audit-remediation-t12`. Route: inline (one line plus test). `persistAssignments` returns the state read error instead of `nil`, still without overwriting the file. In practice the TUI sync already fails earlier on a corrupt state, so this mainly closes a silent path. RED observed first: `TestPersistAssignmentsReportsUnreadableState`, then GREEN. Verification: `go run ./internal/gofmtcheck` ok; `go build ./...` ok; `go test ./internal/app/... ./cmd/...` ok (only `cmd/brunnr-code` imports `internal/app`, so the full suite was not rerun). PR #13, merged.
- T13 done on `fix/audit-remediation-t13`. Route: inline (config only). `ci.yml` declares top-level `permissions: contents: read`, and all seven checkouts set `persist-credentials: false` (no step uses git against the remote). No test (CI config); YAML parsed locally, and the PR's own CI run exercises the workflow. PR #14, merged after the Go Format job passed on CI without persisted credentials.
- T14 done on `fix/audit-remediation-t14`. Route: delegated (writer trigger: four non-trivial files in `reviewtransaction`; Sonnet writer, parent spot check of the helper diff and the new test). `waitForStoreLock(ctx, timeout, acquire)` replaces the five pasted capture retry loops and the body of `acquireCompactStartLock`; `replaceContextGuarded` and compact abandon now wait up to `maintenanceLockTimeout` instead of failing on the first contended attempt, and the loops honor ctx cancellation. The CLI classifier already maps `AuthorityLockTimeoutError` to a pre-native, not-started outcome, so no classifier change. Net -38 lines. RED observed first (writer): `TestCompactCommitWaitsOutBriefStoreLockContention` failed with "authoritative review store advisory lock is held", then GREEN. Left as is: retry loops in `rar_path_safety.go` and `store_lock.go`. Verification: `go run ./internal/gofmtcheck` ok; `go test ./... -timeout 28m` ok (parent). PR #15, merged.
- T15 done on `fix/audit-remediation-t15`. Route: inline (one list entry plus test). `review-transactions/terminal-consumption` joins `storeResetRemovableTargets`. RED observed first: `TestResetReviewStoreRemovesTerminalConsumptionTombstones` (tombstone survived), then GREEN. Verification: `go run ./internal/gofmtcheck` ok; `go test ./... -timeout 28m` ok. PR #16, merged.
- T16 done on `fix/audit-remediation-t16`. Route: inline (shared helper plus one call site). `reviewerTransportFailureDetail` returned stderr unchanged for Claude as well; it now keeps the last `reviewerTransportFailureStderrLimit` (4096) bytes, and the Codex adapter uses the helper. RED observed first: `TestReviewerTransportFailureDetailBoundsStderr` (220020 bytes), then GREEN. Verification: `go run ./internal/gofmtcheck` ok; `go test ./... -timeout 28m` ok.
- T17 decision (user, 2026-10-13): shipped skills stay installer-owned; document that edits are overwritten and that customizations belong under another skill ID.
- T17 done on `fix/audit-remediation-t17`. Route: inline (docs plus one code comment). README states that the eight shipped skills' files are rewritten by every install and sync and that customizations go under another skill ID; `components/skills/inject.go` says the same at the write. No test (documentation only). Verification: `go build ./...` ok; `go run ./internal/gofmtcheck` ok; `go test ./internal/components/skills` ok; `go test ./internal/app -run Documented` ok. PR #18, merged.
- T18 decision (user, 2026-10-13): delete both packages, accepting the loss of the bundle drift test (the bundle is published nowhere). Done on `fix/audit-remediation-t18`. Route: inline (deletion). `internal/providercontractbundle` and `internal/providercontractbundlecmd` removed, -1415 lines; `contracts/review-provider-contract/` kept per CLAUDE.md. No other reference besides `odd/` history. Verification: `go build ./...` ok; `go vet ./...` ok; `go run ./internal/gofmtcheck` ok; `go test ./... -timeout 28m` ok. PR #19, merged.
- T19 done on `fix/audit-remediation-t19`. Route: delegated (writer trigger: deletions across 12 files; Sonnet writer proved each item dead with `git grep`, parent spot check of the persona diff). Deleted `internal/filecoord`; `cli/install_state_lock.go` inlined as direct `statecoord.WithLock` calls; `installcmd` keeps only `ResolveAgentInstall`; `engram` unverified `downloadAndExtractTarGz`/`downloadAndExtractZip`/`byteReaderAt` removed (`extractZipBinary` uses `bytes.NewReader`); `persona` drops the unreachable non-Codex `StrategyFileReplace` fallthrough and `preserveManagedSections`. -939 lines. Test-first exception: pure deletion. Verification: `go build ./...` ok; `go vet ./...` ok; `go run ./internal/gofmtcheck` ok; `go test ./... -timeout 28m` ok (parent). PR #20, merged.
- T20 done on `fix/audit-remediation-t20`. Route: delegated (writer trigger: 30 files; Sonnet writer, parent spot check of `backup.List`). Removed the never-true upgrade `ExitRequested` chain (`runStrategy` returns `error`; its `default:` arm stays because `InstallMethod` is a string and is tested), `enumerateFilesInDir`/`backupExcludeSubdirs`, `InstallCommandsForDep`, the unread `doctor.Remedy` fields, `agentbuilder` leftovers (`MockEngine` moved to a test file, unused `Install` param), the fake TUI timeout, the unreachable abandon replay block, the dead `shouldWrite` branch in sync, and `RuntimeContextBudget`. One `backup.List` replaces `app.ListBackups`/`cli.listBackupsFromDir` internals; one `findAuthorityRoot` serves both lock-root walks. -1422 lines net. Skipped by plan: v1 `Store.Append` move, engram anon-retry, `osReadFile`/`readFileOrEmpty`/`mergeJSONFile` duplicates (package test seams), `custom-agents.json` location. Test-first exception: deletion/consolidation. Verification: `go build ./...` ok; `go vet ./...` ok; `go run ./internal/gofmtcheck` ok; `go test ./... -timeout 28m` ok (parent). PR #21, merged.
- T21a done on `fix/audit-remediation-t21a`. Route: delegated (writer trigger: 17 files; Sonnet writer, parent spot check of `removeManagedPermissions`). Item 1: snapshot dirs 0700, archive and manifest 0600 (`TestSnapshotCreateIsPrivate`, RED 755/644). Item 2: `Snapshotter.Create` removes a dir it created when it fails (`TestSnapshotCreateRemovesPartialDirOnFailure`, RED dir left behind). Item 7: a 403 with remaining quota reports "forbidden (check GITHUB_TOKEN/GH_TOKEN)" (`TestCheckGitHubResponseForbiddenDistinguishesRateLimit`). Item 8: npm prefix probe under a 5s context; `pathUnderHome` compares whole segments (`TestPathUnderHome`, new helper so RED was a compile failure). Item 9: `gofmtcheck` skips tracked files deleted from the worktree (`TestRunSkipsTrackedFilesDeletedFromWorktree`). Item 11: `--agent`/`--agents` yielding no ids is rejected (`TestParseInstallFlagsRejectsEmptyAgentList`). Item 13: uninstall removes only the managed deny entries and `defaultMode: bypassPermissions` via `permissions.ManagedClaudeDenyRules()` (`TestRemoveManagedPermissionsKeepsUserRules`; written after the implementation, so no RED observed). Known limits: a user deny rule identical to a managed one is removed too; JSONC comments are still not preserved by this uninstall path. Verification: `go build ./...` ok; `go vet ./...` ok; `go run ./internal/gofmtcheck` ok; `go test ./... -timeout 28m` ok (parent).

## Next step

Create `fix/audit-remediation` from `main` with this document, push it, open the draft PR to `main`; then `fix/audit-remediation-t01` for T1.
