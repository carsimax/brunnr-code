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
- [x] T2: Version alignment. Raise the floor to 1.6.2 and upgrade via `codegraph upgrade` when the installed CLI is older. Tests. Route: delegated.
- [x] T3: Strict MCP wiring detection for Claude (args `serve --mcp`) and Codex (TOML `[mcp_servers.codegraph]`). Tests. Route: delegated.
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
  - Commit: 3c354609
- T2 done (route: delegated, one writer; trigger: 2+ non-trivial files across communitytool and cli plus tests).
  - Floor raised to 1.6.2 (`codeGraphUpstreamVersion`). When the installed CLI is below it, `InstallWithHome` runs `codegraph upgrade`, re-detects and re-probes the CLI, then runs the targeted `codegraph install --target ... --location global --yes`. An upgrade also skips the "already configured" shortcut, so a wired but outdated CLI is still upgraded and re-wired. The old path that dropped the target ids, ran an untargeted `codegraph install --yes`, and returned a manual action is gone.
  - Failure handling: a failed upgrade, a CLI that disappears, or a version still below the floor fails the step with an error that names both versions and the manual fixes (`codegraph upgrade`, `npm install -g @colbymchenry/codegraph@latest`, `pnpm add -g @colbymchenry/codegraph@latest`). The CodeGraph config snapshots are rolled back. There is no untargeted fallback.
  - Unparseable version decision: an undeterminable `--version` is treated as below the floor. The targeted install passes agent ids that an older CLI rejects as a whole, so only a proven version is trusted. Upstream `upgrade` on a current CLI prints "Already up to date" and exits 0, so the cost is one extra command. If the version is still undeterminable after the upgrade, the step fails.
  - Upstream evidence (v1.6.2): `src/bin/codegraph.ts` defines `upgrade [version]` with only `--check` and `--force`, with no `--yes` and no confirmation. `src/upgrade/index.ts` `runUpgrade` never prompts. Its only interactive step is `maybeOfferBetaSignup` (`src/installer/beta-signup.ts`), which returns early unless both stdin and stdout are TTYs. Brunnr's runner gives the child no stdin, so the upgrade is non-interactive. Note: for `npx` and source-checkout installs, upstream exits 0 without upgrading. The re-probe catches that case and fails the step.
  - Home bug fix: the install step ran `codegraph install` (and npm/pnpm) through `runCommand` with the process HOME. Install and sync now share `codeGraphHomeRunner` and `codeGraphCommandEnvironment` in `internal/cli/sync.go`. For a non-real home they set `HOME`, `XDG_CONFIG_HOME` (`<home>/.config`) and `CODEX_HOME` (`<home>/.codex`, which upstream honors for Codex). The real home inherits the process environment. `CLAUDE_CONFIG_DIR` is not set, because sync never set it. `runCodeGraphCommand` is the test seam, and it streams output like `executeCommand` when streaming is on.
  - The missing npm/pnpm error now names Node.js/npm, pnpm, and both install commands.
  - Checks: gofmtcheck ok; go vet ok; components+app tests ok; full `internal/cli` suite ok (573s). `~/.codex` absent before and after. `~/.claude.json` mtime moved during the run, but it also moves every 1–2 minutes with no tests running (the live Claude Code session writes it), and it holds no temp-path or test traces.
  - Tests: floor comparison table (`TestCodeGraphVersionLess`), `TestInstallEnforcesTheUpstreamFloorBeforeTargetedInstall` (at floor, newer, older then upgrade, undeterminable then upgrade, upgrade failure, still old, still undeterminable), `TestInstallUpgradesAnOlderCLIEvenWhenAgentsAreAlreadyWired`, `TestCodeGraphCommandEnvironment`, `TestCodeGraphInstallAndSyncRunAgainstTheRuntimeHome`, and a stricter npm/pnpm error test. RED was observed first: communitytool assertion failures, and a cli compile failure on the missing helper. The communitytool `TestMain` stubs the version probe, so no test runs a real `codegraph --version`.
  - Commit: d013c36e
- T3 done (route: delegated, one writer; trigger: contract code plus fixtures in communitytool and cli tests).
  - Upstream evidence (v1.6.2): `src/installer/targets/shared.ts` `getMcpServerConfig()` returns `{type:"stdio", command:"codegraph", args:["serve","--mcp"]}`; `claude.ts` adds `alwaysLoad: true`; `codex.ts` writes the `[mcp_servers.codegraph]` table to `$CODEX_HOME/config.toml` with a `~/.codex` fallback. Upstream never writes an absolute command path, so only the bare `codegraph` command counts.
  - Claude: `.claude.json` `mcpServers.codegraph` must have `command == "codegraph"` and `args` holding `serve` immediately followed by `--mcp`. `type` and `alwaysLoad` are not checked, so the user can change them.
  - Codex: `config.toml` is parsed with `github.com/BurntSushi/toml` (already a dependency; go.mod unchanged). Only the `mcp_servers.codegraph` entry is decoded, with the same command/args rule, so another server's shape cannot hide it. The path is `system.CodexConfigDir(home)/config.toml`, which honors `CODEX_HOME` only for the real home, like the environment T2 hands the upstream CLI. A comment, a string value or another table that mentions codegraph no longer counts. The dead empty `SettingsPath` entry left the Codex owned paths.
  - Status flow: a mis-wired entry makes the agent not configured, so neither the reconcile shortcut nor the guidance-only repair applies and install runs the targeted `codegraph install --target ... --location global --yes` (`TestInstallRewiresAnOutdatedClaudeEntry`).
  - Tests: `TestClaudeGlobalConfigRequiresUpstreamServeMCPEntry`, `TestCodexConfigRequiresUpstreamServeMCPTable`, `TestCodexWiringIgnoresAmbientCodexHomeForIsolatedHomes`, `TestCodexWiringIgnoresOtherServerShapes`, `TestInstallRewiresAnOutdatedClaudeEntry`. Fixtures that used the pre-1.6 shape were moved to the upstream shape. RED observed first: 19 subtests failed on the old substring and command-only checks, and the rewire test failed on the old code.
  - Checks: gofmtcheck ok; go vet ok; `go test ./internal/components/... ./internal/assets/... ./internal/app` ok; targeted `internal/cli` CodeGraph tests ok (full cli suite runs after T4).
