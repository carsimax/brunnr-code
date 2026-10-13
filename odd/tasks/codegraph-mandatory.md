# Feature: codegraph-mandatory

## Objective

Make CodeGraph a mandatory part of Brunnr-Code, remove the "Community Tools" selector, and align the integration with upstream CodeGraph v1.6.2 (https://github.com/colbymchenry/codegraph).

## Problem / Why

- CodeGraph is only installed if the user picks it in the TUI "Community Tools" screen. The maintainer wants it always installed.
- The integration targets upstream 1.4.1 (`codegraph_contract.go`). Upstream is at 1.6.2 (2026-10-03).
- Brunnr never upgrades an installed CLI, and its guidance forbids `codegraph upgrade`. Installed CLIs drift.
- The managed guidance makes `codegraph sync -q` mandatory before CLI reads. Upstream v1.6 keeps the index fresh with a daemon file watcher, so manual sync is only needed when the watcher is off.
- MCP wiring detection is weak. Claude only checks `command == "codegraph"`. Codex only checks that the substring "codegraph" appears in the config.

## Decision (user, 2026-10-12)

Keep Brunnr's own managed guidance block, and keep replacing upstream's `CODEGRAPH_START/END` block. Update the block to v1.6 behavior: daemon auto-sync, so manual sync is not required. Brunnr, not the agent, runs `codegraph upgrade`. Lazy auto-init without asking stays.

## Scope

- Remove the Community Tools TUI screens, welcome entry, routes and model state.
- CodeGraph install and guidance steps run unconditionally in install and sync.
- Legacy `community_tools` / `community_tools_configured` keys in `state.json` stay readable and are ignored.
- Contract floor raised to 1.6.2. An installed CLI below the floor is upgraded via `codegraph upgrade` before `codegraph install --target ... --location global --yes`.
- MCP detection:
  - Claude requires `command: "codegraph"` and `args` containing `serve --mcp`.
  - Codex parses `[mcp_servers.codegraph]` in `config.toml`.
- Guidance step 6 is rewritten for daemon auto-sync.

## Out of scope

- Switching the install method to upstream's standalone `install.sh` bundle. npm/pnpm stay, because a curl|sh install would add an unsigned remote script to the trusted install path.

## Constraints

- No legacy code paths for the removed selector, beyond tolerating old state.json keys.
- Tests and asset wording updated with the behavior. Artifacts in English.

## Tasks

- [x] T1: Remove the Community Tools selector and make CodeGraph mandatory. Covers TUI, model, state, app, cli run/sync, communitytool gating and tests. Route: delegated (multi-file writer).
- [ ] T2: Version alignment. Raise the floor to 1.6.2 and upgrade via `codegraph upgrade` when the installed CLI is older. Tests. Route: delegated.
- [ ] T3: Strict MCP wiring detection for Claude (args `serve --mcp`) and Codex (TOML `[mcp_servers.codegraph]`). Tests. Route: delegated.
- [ ] T4: Guidance update for v1.6 (daemon auto-sync; Brunnr owns upgrade). Tests pinning wording. Route: inline or delegated, based on size.

## Acceptance criteria

- `go run ./internal/gofmtcheck` passes.
- `go test ./...` passes.
- `git grep -i 'CommunityTools\|community_tools\|Community Tools'` outside `odd/` returns only the legacy state-key tolerance.

## Delivery

Strategy: `single-pr`. Forecast: ~600–900 authored lines, mostly deletions in the TUI and its tests.

## Progress

- Exploration done (repo mapping agent and upstream research agent).
- Branch `feat/codegraph-mandatory`.
- T1 done (route: delegated, one writer; trigger: 2+ non-trivial files across model, state, app, cli, communitytool and TUI).
  - Removed `Selection.CommunityTools`, `HasCommunityTool`, the state fields `community_tools` / `community_tools_configured`, `restorePersistedCommunityTools`, and the whole Community Tools TUI (welcome entry, standalone flow, install-flow screen, routes, model fields, messages, renderers and their tests; dead helpers `openBrowserCmd`, `commandEnvironmentWithOverrides` and `executeExternalCommand` went with it).
  - Install always schedules `codegraph:install` after persona and guidance. Global sync always schedules `sync:codegraph-guidance`; inside the step the existing conservative rules still apply (refresh only when the CLI is available). The native reviewer `codegraph_explore` grant is always applied. `InjectCodeGraphGuidanceIfSelected` was replaced by the existing unconditional `InjectCodeGraphGuidance`.
  - `CleanLegacyCodeGraphGuidance` now skips files without legacy guidance. Before, it collapsed blank lines in every prompt, and sync would have rewritten files on every run now that the step always runs.
  - Old state files with the retired keys still decode, and the keys are not written back (`TestLegacyCodeGraphSelectionKeysAreIgnored`).
  - Test seams: the cli `TestMain` and a new app `TestMain` stub the CodeGraph installer through `cli.SetCodeGraphInstallerForTest`. Without the stub, full-install tests run the real `codegraph install` against the developer's real HOME. Before the seam existed, one app test run did exactly that and created `~/.codex`; the directory was moved aside.
  - Checks: gofmtcheck ok; go vet ok; model/state/app/tui/components tests ok; full `internal/cli` suite ok (569s); acceptance grep only shows the legacy-key test.
