# Claude statusline

## Objective
Ship the maintainer's Claude Code status line as part of the installer and show the running brunnr-code version in it.

## Problem / why
The status line lives only in `~/.claude/statusline-command.sh` on one machine. Installing it from the binary makes it reproducible and lets it show which brunnr-code build configured the agent.

## Scope
- Claude Code only (Codex untouched).
- Embedded script asset, written next to Claude settings and tracked as managed bytes.
- `statusLine` set in Claude `settings.json` only when the user has none, or when it already points at the managed script.
- Version baked in at install/sync time from `cli.AppVersion`; stale until the next install/sync after an upgrade (accepted).
- Portable `date` for epoch formatting (BSD `date -r`, GNU `date -d @`).
- Uninstall removes what install added.

## Constraints
- Requires `zsh` and `jq` at render time; script degrades silently if missing values.
- Never overwrite a user-owned `statusLine`.

## Tasks
- [x] T1 Embedded statusline asset + installer step + uninstall + tests (route: delegated writer; trigger: 2+ non-trivial files)

- [x] T2 Skip a fresh status line setup when `zsh` or `jq` is missing from PATH, warn on stderr (route: inline; small, understood change)

## Acceptance criteria
- Fresh install writes the script and `statusLine`; script shows `brunnr-code <version>`.
- Existing foreign `statusLine` is left untouched.
- Re-running install is idempotent; uninstall removes managed script and its `statusLine`.

## Checks
- `go run ./internal/gofmtcheck`
- `go test ./...` (via `scripts/test-fast.sh` locally)

## Delivery
Strategy: single-pr (forecast < 400 authored lines).

## Progress
- Branch `feat/claude-statusline` created from `172482b4`.
- T1 done. Ownership via a self-hashing trailer (`# brunnr-code-managed sha256:<hash>`) instead of the managedownership ledger, so uninstall has no ledger to clean. Version shown dim at the end of the identity row as `brunnr-code <AppVersion>` (no `v`, same as `brunnr-code --version`).
- Evidence: RED `undefined: InstallClaudeStatusLine`; GREEN agenthooks + uninstall tests; `gofmtcheck` exit 0; `scripts/test-fast.sh` PASS; `zsh -n` ok; render smoke shows `brunnr-code v0.0.0-test`. Parent spot check: gofmtcheck + agenthooks/uninstall tests ok.
- Pending: Docker/real-agent e2e not run; GNU `date -d @` fallback not exercised on Linux. RDD disabled in this clone (no review).
- Note: an existing unowned `~/.claude/statusline-command.sh` makes install a no-op; delete it and re-run sync to adopt the managed one.

- T1 shipped in v0.2.0 (PR #29).
- T2: installer does not install system packages (needs sudo, per-distro package managers). It detects `zsh`/`jq` with `exec.LookPath`; a missing tool skips a fresh setup and returns `Result.Missing`, which install/sync print as a warning. An already managed statusLine keeps being refreshed. Evidence: RED `undefined: SetStatusLineLookPathForTest`; GREEN agenthooks + uninstall tests; `gofmtcheck` ok; `scripts/test-fast.sh` PASS.

## Next step
Push and open PR for T2 (user decision).
