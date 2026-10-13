# Windows Full Suite: fix the pre-existing Windows-only failures

## Objective

Turn `.github/workflows/windows-full-suite.yml` green on `main`. It has failed since at least
`d27af613` (and on the daily schedule), with the same set before and after the test-speed caches,
so a new Windows regression is currently invisible.

## Evidence

Run `38429727532` on `76b49686` (log copy outside the repo, in the session scratchpad,
`windows-failures-76b49686.log`). Failing tests:

| Package | Test | Assertion |
|---|---|---|
| `internal/cli` | `TestRunDoctor_AssetVersionRemediation` (both subtests) | remedy quotes `'C:/Users/RUNNER~1/.../brunnr-code'`; test wants it unquoted |
| `internal/cli` | `TestRunDoctorToolExecution/valid_PowerShell_launcher` | PowerShell version probe exits 2 for a shim under a path with spaces and `&` |
| `internal/cli` | `TestIsInPATH` | `isInPATH` returns false for an entry in PATH |
| `internal/cli` | `TestReviewStopHookBlocksWithReasonAndBothCommands` | reason missing the canonical STATUS command (quoted `--cwd` form differs) |
| `internal/cli` | `TestNextTransitionPassiveDeltaNeverHidesALiveReview` | granted START returns `operation_outcome_unknown` |
| `internal/components/uninstall` | `TestClaudeUninstallRetiresModulePilotOnlyOnFullRemoval` | asserts a Unix file mode; Windows reports `-rw-rw-rw-` |
| `internal/reviewerprovider` | `TestProbeScratchParentRefusesCaseVariantInsideWorkspace` | case-variant path inside the workspace is not refused |

## Scope

- Fix the root cause per failure: product code when the product is wrong on Windows,
  the test when its expectation is Unix-only. Never skip a test only to go green.
- Out of scope: CI workflow changes, unrelated Windows hardening.

## Constraints

- No Windows machine locally: verification is `go test` on macOS (no regression) plus
  `GOOS=windows go vet` / `go test -c` for compilation, then the Windows workflow via
  `workflow_dispatch` on the pushed branch.

## Tasks

- [x] T1 — Diagnose and fix the 7 failures. Route: delegated (writer; 7 independent failures across 3 packages, preparation reading).
- [x] T2 — Push branch, dispatch Windows Full Suite, confirm green. Route: inline (git/gh state).

## Acceptance criteria

- Windows Full Suite passes on the branch; Linux CI and macOS `go test ./...` still pass.

## Checks

- `go run ./internal/gofmtcheck`
- `GOOS=windows go vet ./internal/cli ./internal/components/uninstall ./internal/reviewerprovider`
- `go test ./... -count=1` (macOS)
- Windows Full Suite workflow run on the branch

## Progress

- Delivery strategy: single-pr (fast-forward to main after green, as previous features).
- T1 diagnosis (all 7 are test-side; product behaviour is correct on Windows, no product edit):
  - `TestRunDoctor_AssetVersionRemediation`: the temp path holds `RUNNER~1`; `managedAssetsExecutableToken` quotes `~` (allowlist) for the POSIX target, which is a valid shell word. Test now builds the expectation with that helper.
  - `TestRunDoctorToolExecution/valid_PowerShell_launcher`: the `.ps1` shim hardcoded `--version` while doctor probes engram with `version` (exit 2 = arg mismatch). Shim now checks `tt.arg`.
  - `TestIsInPATH`: test joined PATH with `:`; Windows splits on `;`. Test uses `os.PathListSeparator`. Sole caller (`run.go`) is gated to non-Windows.
  - `TestReviewStopHookBlocksWithReasonAndBothCommands`: product quotes `--cwd` with `pathquote.ShellWord` (Claude Code runs Bash on Windows, a bare backslash path would break). Test expectation uses the same helper.
  - `TestNextTransitionPassiveDeltaNeverHidesALiveReview`: cause in log `CreateFile 'C:...` — the test split the shell-quoted granted invocation with `strings.Fields`, leaving literal quotes in `--cwd`. Test now uses `invocationArgs` (`SplitPrintedCommandWords`).
  - `TestClaudeUninstallRetiresModulePilotOnlyOnFullRemoval`: Windows exposes only a read-only bit; test now asserts the core mode is unchanged from before uninstall.
  - `TestProbeScratchParentRefusesCaseVariantInsideWorkspace`: `os.TempDir` reads `TMP`/`TEMP` on Windows, not `TMPDIR`; test sets all three. `pathidentity.Contains` is identity-based, so the product refuses correctly.

- T2 observed: Windows Full Suite run 38433752622 on the branch, all 10 shards success.

## Next step

Fast-forward to main (user decision).
