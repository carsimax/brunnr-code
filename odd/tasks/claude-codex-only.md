# Feature: claude-codex-only

Locator: `odd/tasks/claude-codex-only.md` · Engram mirror: `odd/claude-codex-only/tasks` · Branch: `feat/claude-codex-only`

## Objective

Reduce the system to two supported agents, Claude Code and Codex, removing every other agent with no legacy trace, and reset the fork's documentation to a fresh start.

## Decisions

- No legacy trace: uninstall does not recognize removed agents' paths; their `legacyassets` digest tables are deleted.
- Stale `state.json` entries for removed agents are silently filtered at selection read.
- Kept on purpose: `internal/assets/generic/`, `reviewtransaction/risk.go` dot-dir heuristics, frozen `contracts/` text and telemetry wire values, `telemetryruntime` (used by Claude Code and Codex).
- Gentle Shell removed (it is a Pi distribution).
- Fresh start: upstream `docs/`, ODD history, PRDs, contribution docs and machinery are removed; README only states the project is based on Gentle AI; `LICENSE` keeps the upstream copyright notice.
- Sole maintainer: no external contributions, no chained PRs.
- Receipt-driven review disabled clone-local by the user.

## Tasks

- [x] **T2 — 13 leaf adapters and their assets.** `28e91f17`.
- [x] **T3 — Pi and the gentle-pi review relay.** `9fccb15d`, `e6771e75`.
- [x] **T8 — Gentle Shell.** `7f987609`.
- [x] **T4 — OpenCode and its review relay.** `07bc7b22`.
- [x] **T5 — Shared switch sites, constants, stale-state filtering.** `6e65ed19`.
- [x] **T6 — Tests, guards, ratchets, deadcode baseline.** `56563dfe`, `c3d9ede3`.
- [x] **T7 — Non-Go surfaces, fresh start.** `a4b86135`, `4d336cd2`.

## Verification

Full `go test ./...` after each task: only pre-existing base failures in `internal/update` (`TestReleaseSecurityScripts…`, `TestCanonicalReleasePublicKeys…`).

## Leftovers for the Brunnr-Code rename

- `.goreleaser.yaml` still packages the deleted `docs/review-integration.md`.
- `deploy/telemetry` comments and shipped skills mention upstream docs.

## Next step

Feature complete. Merge to `main` is the user's decision. Next feature: Brunnr-Code rename (binary `brunnr-code`).
