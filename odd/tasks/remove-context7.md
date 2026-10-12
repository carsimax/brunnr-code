# Feature: remove-context7

Locator: `odd/tasks/remove-context7.md` · Engram mirror: `odd/remove-context7/tasks` · Branch: `refactor/remove-context7`

## Objective

Remove the Context7 MCP integration so the installer manages only two external tools, CodeGraph and Engram, and drop the leftover `gga` fixture names from tests.

## Why

Context7 is still a useful MCP, but it is optional. Claude Code's built-in web search and fetch cover occasional doc lookups, the free tier was cut sharply in 2026, and its tool definitions use context in every session. Anyone who wants it can add it with `claude mcp add`. The installer should only own tools the orchestrator depends on.

## Decisions

- **Full removal with no migration (user, 2026-10-12):** there are no production installs. Uninstall will not clean up Context7 entries written before this change.
- **Keep:** CodeGraph (`internal/components/communitytool`), Engram, `filemerge.replaceCodexMCPServerBlock` (used by `UpsertCodexEngramBlock`), the generic adapter MCP methods, and system deps (node/npm/go/brew).
- GGA production code was already removed in `035c239a`. Only fake-tool names in tests remain.

## Footprint (explorer, 2026-10-12)

Production: delete `internal/components/mcp/`, `internal/versions/`, and `testdata/golden/context7-*.json`. Partial edits in `model/types.go`, `model/presets.go`, `catalog/components.go`, `planner/graph.go`, `cli/run.go`, `cli/sync.go`, `uninstall/service.go`, and `update/upgrade/executor.go`. Code left dead: `filemerge.UpsertCodexRemoteMCPServerBlock`, `filemerge.UpsertCodexMCPServerBlock`, and `claude.MergeUserMCPServer`. Tests: about 25 files plus `e2e/e2e_test.sh`. Forecast: about 600 production lines and about 1,000 test lines. The `single-pr` delivery strategy is used because this is a single-maintainer repo with no chained PRs.

## Tasks

Each task runs `go run ./internal/gofmtcheck`, `go build ./... && go vet ./...`, and the tests of touched packages. A full `go test ./...` runs at the end. Each task ends with a Conventional Commit. RED/GREEN applies only to T2. T1 and T3 are removals, so the build and updated tests are the proof. RDD is disabled in this clone.

- [x] **T1: GGA test fixtures.** Rename the fake `gga` tool in `internal/update`, `internal/app`, and `internal/cli` tests, and remove the orphan comment headers in `upgrade/strategy_test.go`.
- [x] **T2: Filter unknown persisted components.** `state.RestoreSelection` drops component IDs that are not in the catalog, so a stale `"context7"` in `state.json` does not break sync. Write the test first.
- [x] **T3: Remove Context7.** Remove the wiring in model, presets, catalog, planner, cli run/sync, uninstall, and upgrade backup paths. Delete `components/mcp`, `versions`, and the goldens. Prune the dead filemerge and claude helpers. Rewrite the shared TOML tests around Engram or a generic server.
- [x] **T4: e2e and final sweep.** Update `e2e/e2e_test.sh` presets and the component cases, and decide whether to keep `renovate.json`. Run a full `go test ./...` and a final `git grep -i context7` sweep.

## Progress

Branch created from `a1887265`.

- **T1 (delegated, writer):** commit `ecd9eee6`. Renamed the fake `gga` tool to `faketool` in the `internal/update`, `internal/app`, and `internal/cli` tests and reworded three comments. Removed the orphan headers in `upgrade/strategy_test.go`: the GGA and script-upgrade headers, plus `GoInstallMissingImportPath` and `ScriptUpgradeExecFailure`, which had no test under them. `git grep -i -w gga` outside `odd/` is empty. Checks: gofmtcheck ok, build+vet ok, `go test ./internal/update/... ./internal/app ./internal/components/filemerge` ok, `go test ./internal/cli -run Doctor` ok.
- **T2 (delegated, writer):** commit `40b7d4fd`. RED: `TestRestoreSelectionDropsUnknownComponents` failed with `[engram retired-component skills]`. GREEN: `model.KnownComponentIDs` mirrors `model.SupportedAgentIDs`, and `RestoreSelection` filters through it while keeping nil for empty input. `model.ShippedComponentIDs` plus a catalog parity test keep the model list equal to `catalog.MVPComponents`. T3 must remove `ComponentContext7` from both. Checks: gofmtcheck ok, build+vet ok, `go test ./internal/state ./internal/model ./internal/catalog` ok, `go test ./internal/cli -run 'Sync|State|Restore'` ok.
- **T3 (delegated, writer):** commit `70ef2a06` (38 files, +73/-2139). Removed `ComponentContext7` from model (`knownComponentIDs`), presets, catalog, planner graph, `cli/run.go` (install case, backup/verification paths, `claudeMCPSettingsCleanupPaths`), `cli/sync.go` (default scope, cleanup paths, sync case), uninstall (component lists, `context7Targets`/`context7Operations`, managed-file checks), and the upgrade backup path. Deleted `internal/components/mcp`, `internal/versions`, and `testdata/golden/context7-*.json`. Pruned `filemerge.UpsertCodexMCPServerBlock`, `UpsertCodexRemoteMCPServerBlock`, and `claude.MergeUserMCPServer`; `MergeUserConfig` absorbed `mergeUserConfig` because its preserve-server branch had no other caller. Context7-only tests were deleted; tests that used context7 as sample data now use engram, `example`, or `claude-theme`. `git grep -i context7` outside `odd/`, `e2e/`, `renovate.json` is empty. Checks: gofmtcheck ok, build+vet ok, `go test ./... -count=1` ok (50 packages).
- **T4 (delegated, writer):** commit `0a00a099` (2 files, +15/-99). In `e2e/e2e_test.sh`, removed `test_dry_run_component_context7`, `test_cc_context7_injection`, and `test_codex_context7_in_toml` with their registrations, and dropped the context7 preset assertions. Expectations now match `model.ComponentsForPreset`: ecosystem-only is engram, skills, persona; full adds permissions and claude-theme. Mixed tests that used context7 as the MCP sample now assert the Engram MCP entry (`"engram"` in `~/.claude.json`, `[mcp_servers.engram]` in Codex `config.toml`), which `components/engram/inject.go` writes. `renovate.json` keeps only `$schema` and `config:recommended`, because its custom manager targeted the deleted `internal/versions/versions.go`. Docker e2e was not run. Checks: `bash -n` ok, every called test is defined and every defined test is called, gofmtcheck ok, build+vet ok, `git grep -i -e context7 -e upstash -e internal/versions -- ':!odd/'` is empty.

All tasks (T1-T4) are done. Next step: the user decides on push and the PR.
