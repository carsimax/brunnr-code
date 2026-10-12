# Feature: claude-codex-only

Locator: `odd/tasks/claude-codex-only.md` · Engram mirror: `odd/claude-codex-only/tasks` · Branch: `feat/claude-codex-only`

## Objective

Reduce the whole system to two supported agents: Claude Code and Codex. Every other agent is removed with no trace.

## Problem / Why

The fork only targets Claude Code and Codex. Fifteen other adapters (antigravity, conductor, cursor, gemini-cli, hermes, kilocode, kimi, kiro-ide, openclaw, opencode, pi, qwen-code, trae-ide, vscode-copilot, windsurf) add ~45k LOC, ~162 agent-aware test files, and review-transport complexity (OpenCode relay, gentle-pi relay) that the fork does not need.

## Scope

- Delete adapters, assets, components, CLI commands, review transports, and constants of the 15 removed agents.
- Prune agent switch branches in shared code; keep Claude Code and Codex behavior unchanged.
- Update tests, guard ratchets, goldens, docs, scripts, bench, e2e, CI.

## Constraints and decisions

- **No legacy trace (user decision, 2026-10-11):** uninstall no longer recognizes paths of removed agents; `legacyassets` digest tables for removed agents are deleted.
- `internal/assets/generic/` stays (shared persona, orchestrator fallback, remote-authorization contract).
- `engram/inject.go:1289` multi-agent case is narrowed, not deleted.
- `reviewtransaction/risk.go` dot-dir heuristics (`.cursor`, `.gemini`, ...) are risk signals, not agent support: keep.
- Frozen `contracts/review-integration/v1`: proposal is to leave the historical v1 text untouched and restrict the live runtime allowlist to `claude-code` and `codex`. Revisit in T7.
- `telemetryruntime`: confirm OpenCode-only before deleting (T4).
- Claude Code and Codex install/sync/review behavior must not change.

## Tasks

Run `go build ./... && go vet ./...` plus touched-package tests after each task. Each task closes with a Conventional Commit on `feat/claude-codex-only`.

- [x] **T1 — Assets (merged, 2026-10-11).** Reopened into T2/T4: assets are embedded (`internal/assets/assets.go:5`) and read by path at runtime by adapters/components, so deleting them alone leaves surviving consumers' tests red. Leaf-agent assets + hermes skill move to T2; `internal/assets/opencode*` moves to T4.
- [x] **T2 — 13 leaf adapters + their assets.** Done in `28e91f17` (218 files, +1187/−21933). Surface extended with user approval to `.refusal-ratchet-baseline.txt`, `scripts/gen-sdd-agent-digests/**`, planner/upgrade/e2e tests, `docs/agents.md` + `README.md` agent table only. Verified: gofmt clean, build+vet ok, full `go test -timeout 60m ./...` only base failures in `internal/update` (TestReleaseSecurityScripts…, TestCanonicalReleasePublicKeys…). Digest file edited by hand (no upstream tags locally to regenerate). RDD: assessed high (hot_path update), consent granted, START refused `lens_context_budget_exceeded` (no authority created); user disabled RDD clone-local (2026-10-11). Delivery: `disabled/unmanaged`. Delete `internal/assets/{antigravity,cursor,gemini,hermes,kimi,kiro,qwen,windsurf}` and skill `hermes-ephemeral-delegation` (update the embed directive); delete `internal/agents/{antigravity,conductor,cursor,gemini,hermes,kilocode,kimi,kiro,openclaw,qwen,trae,vscode,windsurf}`; remove entries in `agents/factory.go`, `catalog/agents.go`, `agents/capabilitymanifest`, `installcmd/resolver.go`, `telemetry/event.go`, `agentbuilder/engine.go`, `legacyassets`. Route: delegated (writer).
- [x] **T3 — Pi.** Route: delegated (writer). Removed `agents/pi`, `cli/pi_*`, `model.AgentPi`, Pi background intent (model/state/TUI screen/install+sync flags), Pi persona writer, Pi CodeGraph, Pi engram provisioner, Pi uninstall retention, gentle-pi review relay (`pi_host_relay` transport, relay handshake, `--materialize` on capture-result/refuter/validation, role `--input`, host-relay submission descriptors), bundle Pi orchestration (identity list now empty), store-reset `reviews/` adapter category and `--include-adapter-reviews`, `review-ledger-contract-pi.md`. Tests that used the Pi relay to submit role results now stub the compiled claude-code adapter (`stubCompiledProviderRawForTest`) and run `--execute`; e2e refuter uses a stdin-reading claude fake; bench j4435 relays the refuter via `review opencode-transport` (T4 must rework it), j75 uses claude-code. Kept: `internal/shellinstaller` (Gentle Shell product, installs gentle-pi npm package) pending a separate decision; frozen `contracts/` text. Verified: see commit.
- [ ] **T4 — OpenCode.** In order: `internal/assets/opencode` + `internal/assets/opencode_*_test.go`, `components/opencode{agents,default,plugin,runtimeplugins}` (+ `telemetryruntime` if confirmed), `cli/opencode_*`, `cli/review_opencode_transport.go` + `review opencode-transport` facade case, `cli/uninstall_opencode_plugin.go`, rctx3 seal in `reviewtransaction`, `agents/opencode`, `internal/opencode`; replace `NewMVPRegistry`. Route: delegated (writer).
- [ ] **T5 — Shared switch sites and constants.** Prune remaining branches in `cli`, `components/{engram,persona,permissions,mcp,theme,gga,uninstall,agentguidance,communitytool,reviewassets}`, `tui`, `doctor`, `verify`, `system`, `state`, `skillregistry`, `app`, `update/upgrade`; delete removed constants from `model/types.go`. Route: delegated (writer).
- [ ] **T6 — Tests and guards.** Update all-agent table tests, forbidden-construction/minimality guards, retired-workflow and orchestrator-drift ratchets; regenerate goldens. Route: delegated (writer + verifier).
- [ ] **T7 — Non-Go surfaces.** README, CONTRIBUTING, AGENTS.md, `docs/`, `scripts/`, `bench/`, `e2e/`, `.github/`, `.goreleaser.carsi.yaml`, `cmd/gentle-telemetry`, `contracts/`. Route: delegated (writer).

## Acceptance criteria

- `rg -i 'opencode|gentle-pi|kilocode|gemini-cli|cursor|windsurf|antigravity|kimi|qwen|kiro|openclaw|trae|hermes|conductor|vscode-copilot'` returns only intentional hits (risk heuristics, historical frozen contract).
- `go build ./... && go vet ./... && go test ./...` green.
- Agent catalog, TUI, and `review` runtimes expose only `claude-code` and `codex`.

## Delivery

- Forecast: ~45k deleted LOC + ~40 edited files → far above the ~400-line budget.
- Strategy: `ask-on-risk`; chain strategy: **`stacked-to-main`** (user choice, 2026-10-11).
- Proposed slices: PR1 = T2, PR2 = T3, PR3 = T4, PR4 = T5+T6, PR5 = T7.

## Progress

- 2026-10-11: footprint mapped (read-only explorer); plan and no-trace decision recorded. No source changes yet.

## Open items for T5

- `DiscoverAgents` returns removed agents from an existing `state.json` unfiltered → sync fails "unsupported agent"; decide filter vs migrate.
- Lost coverage: ownership-ledger reuse (Kiro) and shared-slug engram dedup (Gemini/Antigravity).
- Leftovers: model constants, StrategyJinjaModules, engram Antigravity code, TUI Kiro picker, system/state, skillregistry, mcp/permissions branches, Kimi SDD retirement plumbing, unused `communitytool.excludedCompatibility`, unreachable generic orchestrator variant code.
- Note: `internal/cli` tests need `go test -timeout 60m` (~13 min).

## Open items for T4/T7 (from T3)

- bench j4435 refuter capture goes through `review opencode-transport`; rework when OpenCode is removed.
- `docs/trigger-rules.md` documents removed `--include-adapter-reviews` (documented-invocation test); `docs/pi.md`, Pi prose in `docs/agents.md`, `scripts/cross-lane-battery.sh`, `.github` remain for T7.
- Gentle Shell (`internal/shellinstaller`, `gentle-ai shell install`, provisioning scripts, e2e shell suites) is a Pi distribution; decide keep/remove.
- Generic leftovers now dead: `communitytool.AgentStatusPending`, store-reset opt-in mechanism, `ReviewTransitionSubmission` capture-result/role shapes in frozen schemas, `writeReviewCLIRawInput`.

## Next step

T3 (Pi). RDD is off clone-local for this feature; verification = gofmt, build, vet, full `go test -timeout 60m ./...`.
