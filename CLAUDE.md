# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project state

This is a single-maintainer fork of upstream Gentle AI, rebranded as **Brunnr-Code** and reduced to two agents: **Claude Code and Codex**. The Go module is `github.com/carsimax/brunnr-code`, the entrypoint is `cmd/brunnr-code`, and `.goreleaser.yaml` and `scripts/install.sh` ship the binary as `brunnr-code`. Env vars use `BRUNNR_CODE_*`, state lives in `~/.brunnr-code` and `<git-common-dir>/brunnr-code/`, and schema IDs use the `brunnr-code.` prefix. Feature history and decisions are recorded in `odd/tasks/*.md`.

What the removal of other agents left behind, by design:

- Removed agents leave no legacy code paths. Stale `state.json` entries for them are filtered out when selections are read.
- `internal/assets/generic/`, the frozen text in `contracts/` are kept on purpose. Do not "clean them up".
- The fork releases through `.goreleaser.yaml` and `.github/workflows/release.yml` (tag `v*`, unsigned). Install is curl-only via `scripts/install.sh` or `go install`. The upstream signing, provenance and promotion pipeline was removed.

## Commands

No Makefile or task runner exists, and CI runs no golangci-lint. CI (`.github/workflows/ci.yml`) runs these checks:

```bash
go run ./internal/gofmtcheck                 # format gate (custom gofmt checker); the other jobs require it
go test ./...                                # full unit suite; run it after every task
go build -o brunnr-code ./cmd/brunnr-code         # local build; version via -ldflags "-X main.version=..."
```

For local runs, `scripts/test-fast.sh` runs the same suite in about 2 minutes instead of about 5. It splits `internal/cli` and `internal/reviewtransaction` into shards that run as separate processes, and the `CLI_SHARDS` and `REVIEW_SHARDS` variables tune the shard counts. CI still runs `go test ./...`.

Single test: `go test -v ./internal/<pkg> -count=1 -run '<Regex>'`

Golden and baseline files:

- `go test ./internal/components -run Golden -update` rewrites `testdata/golden/`.
- `go test ./internal/components/agentguidance -update` rewrites the orchestrator module baseline.

Many tests in `internal/assets` pin the exact wording of the embedded markdown, so an edit to an asset usually needs a matching test or baseline update.

Other test suites:

- **Real-agent e2e** needs the Claude Code CLI installed: `go test -tags real_agent_e2e -v ./e2e/organicruntime -count=1 -timeout=25m`.

## Architecture

The CLI is an installer and configurator. It writes managed configuration, skills, personas, MCP entries and hooks into a coding agent's config directories. It also owns a native code-review workflow.

- **Entry:** `cmd/brunnr-code/main.go` calls `internal/app.RunArgs`. With no arguments it opens the bubbletea TUI (`internal/tui`), which needs a TTY. With arguments it dispatches subcommands such as `install`, `sync`, `doctor`, `restore`, `uninstall`, `update`, `codegraph`, `skill-registry` and `review …` to their implementations in `internal/cli`.
- **Agent adapters:** `internal/agents` defines the `Adapter` interface: detection, config paths, prompt and MCP strategies, and capability flags. The only implementations are `agents/claude` and `agents/codex`, wired in `factory.go`. Components call adapter methods instead of switching on the agent ID. Shared IDs and strategy types live in `internal/model`.
- **Install pipeline:** `catalog` (agents, components, skills) → `planner` resolves dependencies into a `ResolvedPlan` → `pipeline.Orchestrator` executes the stages, with rollback.
- **Components:** `internal/components/*` holds the installable units (engram, mcp, persona, skills, permissions, agenthooks, agentguidance, reviewassets, …). File writes go through `components/filemerge`.
- **Ownership and state:** `managedownership` keeps a hash ledger of the bytes the tool owns, so user edits are never overwritten blindly. `state` and `statecoord` manage `state.json` under a lock. `backup` and `restore` take snapshots.
- **Embedded assets:** `internal/assets` uses `go:embed` to ship the per-agent orchestrator prompts, personas, output styles and skills (`internal/assets/skills/`). These are the source of the files written to user machines. The root `skills/` directory holds repo-local development skills, which are not shipped.
- **Review subsystem (RDD, the largest area):** the CLI side is in `internal/cli/review*.go`. The transaction and lineage logic is in `internal/reviewtransaction`. `internal/reviewerprovider` is the boundary between review authority, which Go owns, and the host-specific reviewer calls. The JSON and text contracts are frozen under `contracts/`. Receipt-driven review is disabled in this clone.
- **Other:** `doctor`/`verify` (diagnostics), `update` (update checks and upgrades for managed tools such as Engram; the binary never updates itself).

## Conventions

- Single maintainer: work on feature branches and use no chained PRs. Each feature is tracked in `odd/tasks/<feature>.md`.
- Guards are ratchets that block only new violations, not clean-slate gates. Comments explain *why*.
