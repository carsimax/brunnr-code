# carsi-ai

An installer and configurator for AI coding agents. It sets up memory, skills,
a persona, MCP servers, and a review workflow for the agent you already use.

Based on [Gentle AI](https://github.com/Gentleman-Programming/gentle-ai).

## Supported agents

| Agent | ID |
|-------|----|
| Claude Code | `claude-code` |
| Codex | `codex` |

## Install

macOS and Linux (amd64, arm64):

```bash
curl -fsSL https://raw.githubusercontent.com/carsimax/carsi-ai/main/scripts/install-carsi.sh | bash
```

The script downloads the latest release, verifies its checksum, and installs
the binary to `/usr/local/bin` or `~/.local/bin`.

| Variable | Effect |
|----------|--------|
| `CARSI_AI_VERSION` | Pin a release, for example `v1.0.0`. |
| `CARSI_AI_INSTALL_DIR` | Install to another directory. |

See [`scripts/install-carsi.sh`](scripts/install-carsi.sh) for details.

## Usage

Run `carsi-ai` with no arguments to open the interactive TUI, or use a command:

| Command | What it does |
|---------|--------------|
| `carsi-ai install` | Configure agents on this machine (`--agents`, `--components`, `--dry-run`). |
| `carsi-ai sync` | Sync agent configs and skills to the current version. |
| `carsi-ai uninstall` | Remove managed files from this machine. |
| `carsi-ai doctor` | Run health diagnostics. |
| `carsi-ai update` / `upgrade` | Check for updates / apply them. |
| `carsi-ai restore` | Restore a config backup. |
| `carsi-ai review ...` | Native review workflow (`review mode status` shows whether it is on). |
| `carsi-ai telemetry status` | Show telemetry state; `telemetry disable` opts out. |
| `carsi-ai version` | Print the version. |

Every command supports `--help`.

## Build from source

```bash
go build -o carsi-ai ./cmd/gentle-ai
go test ./...
```

## License

MIT. See [LICENSE](LICENSE).
