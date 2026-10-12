# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project state

This is a single-maintainer fork of upstream `gentle-ai`. It has been reduced to two agents: **Claude Code and Codex**. It is being rebranded to `brunnr-code` (see `odd/tasks/brunnr-code-rename.md`). The Go module is `github.com/carsimax/brunnr-code`, the entrypoint is `cmd/brunnr-code`, and `.goreleaser.yaml` and `scripts/install.sh` ship the binary as `brunnr-code`. Env vars, schema IDs, state dirs and CLI prose still carry the upstream name until the later rename tasks land. Feature history and decisions are recorded in `odd/tasks/*.md`.

What the removal of other agents left behind, by design:

- Removed agents leave no legacy code paths. Stale `state.json` entries for them are filtered out when selections are read.
- `internal/assets/generic/`, the frozen text in `contracts/` are kept on purpose. Do not "clean them up".
- The fork releases through `.goreleaser.yaml` and `.github/workflows/release.yml` (tag `v*`, unsigned, no Homebrew tap). The upstream signing, provenance and promotion pipeline was removed.

## Commands

No Makefile or task runner exists, and CI runs no golangci-lint. CI (`.github/workflows/ci.yml`) runs these checks:

```bash
go run ./internal/gofmtcheck                 # format gate (custom gofmt checker); the other jobs require it
go test ./...                                # full unit suite; run it after every task
./scripts/deadcode-ratchet.sh                # fails only on NEW unreachable funcs vs .deadcode-baseline.txt
./scripts/deadcode-ratchet.sh --update       # after deliberately removing dead code
go build -o brunnr-code ./cmd/brunnr-code         # local build; version via -ldflags "-X main.version=..."
```

Single test: `go test -v ./internal/<pkg> -count=1 -run '<Regex>'`

Golden and baseline files:

- `go test ./internal/components -run Golden -update` rewrites `testdata/golden/`.
- `go test ./internal/components/agentguidance -update` rewrites the orchestrator module baseline.

Ratchets: `.refusal-ratchet-baseline.txt` is enforced by `internal/cli/refusal_resolution_ratchet_test.go`. Many tests in `internal/assets` pin the exact wording of the embedded markdown, so an edit to an asset usually needs a matching test or baseline update.

Other test suites:

- **`bench/`** is a separate Go module. In that directory run `go vet ./... && go test ./...`. CI then runs the bench binary against the built CLI with `run --binary <cli> --out x.json`.
- **Real-agent e2e** needs the Claude Code CLI installed: `go test -tags real_agent_e2e -v ./e2e/organicruntime -count=1 -timeout=25m`.
- **Docker e2e:** `./e2e/docker-test.sh`. Set `RUN_FULL_E2E=1` and/or `RUN_BACKUP_TESTS=1` to add the extra tiers. The images are `e2e/Dockerfile.*`.

## Architecture

The CLI is an installer and configurator. It writes managed configuration, skills, personas, MCP entries and hooks into a coding agent's config directories. It also owns a native code-review workflow.

- **Entry:** `cmd/brunnr-code/main.go` calls `internal/app.RunArgs`. With no arguments it opens the bubbletea TUI (`internal/tui`), which needs a TTY. With arguments it dispatches subcommands such as `install`, `sync`, `doctor`, `restore`, `uninstall`, `update`, `codegraph`, `skill-registry` and `review …` to their implementations in `internal/cli`.
- **Agent adapters:** `internal/agents` defines the `Adapter` interface: detection, config paths, prompt and MCP strategies, and capability flags. The only implementations are `agents/claude` and `agents/codex`, wired in `factory.go`. Components call adapter methods instead of switching on the agent ID. Shared IDs and strategy types live in `internal/model`.
- **Install pipeline:** `catalog` (agents, components, skills) → `planner` resolves dependencies into a `ResolvedPlan` → `pipeline.Orchestrator` executes the stages, with rollback.
- **Components:** `internal/components/*` holds the installable units (engram, mcp, persona, skills, theme, permissions, agenthooks, agentguidance, reviewassets, …). File writes go through `components/filemerge`.
- **Ownership and state:** `managedownership` keeps a hash ledger of the bytes the tool owns, so user edits are never overwritten blindly. `state` and `statecoord` manage `state.json` under a lock. `backup` and `restore` take snapshots.
- **Embedded assets:** `internal/assets` uses `go:embed` to ship the per-agent orchestrator prompts, personas, output styles and skills (`internal/assets/skills/`). These are the source of the files written to user machines. The root `skills/` directory holds repo-local development skills, which are not shipped.
- **Review subsystem (RDD, the largest area):** the CLI side is in `internal/cli/review*.go`. The transaction and lineage logic is in `internal/reviewtransaction`. `internal/reviewerprovider` is the boundary between review authority, which Go owns, and the host-specific reviewer calls. The JSON and text contracts are frozen under `contracts/`. Receipt-driven review is disabled in this clone.
- **Other:** `doctor`/`verify` (diagnostics), `update` and `releasepolicy`/`releaseprovenance` (self-update with signature verification).

## Conventions

- Single maintainer: work on feature branches and use no chained PRs. Each feature is tracked in `odd/tasks/<feature>.md`.
- Guards are ratchets that block only new violations, not clean-slate gates. Comments explain *why*.
