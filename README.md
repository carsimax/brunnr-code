# Brunnr-Code

An installer and configurator for AI coding agents. It sets up memory, skills,
a persona, MCP servers, and a review workflow for the agent you already use.

Brunnr-Code is based on [Gentle AI](https://github.com/Gentleman-Programming/gentle-ai). It is an
independent fork and is not affiliated with or endorsed by the Gentle AI project.

## Supported agents

| Agent | ID |
|-------|----|
| Claude Code | `claude-code` |
| Codex | `codex` |

## Install

macOS and Linux (amd64, arm64):

```bash
curl -fsSL https://raw.githubusercontent.com/carsimax/brunnr-code/main/scripts/install.sh | bash
```

The script downloads the latest release, verifies its checksum, and installs
the binary to `/usr/local/bin` or `~/.local/bin`.

| Variable | Effect |
|----------|--------|
| `BRUNNR_CODE_VERSION` | Pin a release, for example `v1.0.0`. |
| `BRUNNR_CODE_INSTALL_DIR` | Install to another directory. |

See [`scripts/install.sh`](scripts/install.sh) for details.

## Usage

Run `brunnr-code` with no arguments to open the interactive TUI, or use a command:

| Command | What it does |
|---------|--------------|
| `brunnr-code install` | Configure agents on this machine (`--agents`, `--components`, `--dry-run`). |
| `brunnr-code sync` | Sync agent configs and skills to the current version. |
| `brunnr-code uninstall` | Remove managed files from this machine. |
| `brunnr-code doctor` | Run health diagnostics. |
| `brunnr-code update` / `upgrade` | Check for updates / apply them. |
| `brunnr-code restore` | Restore a config backup. |
| `brunnr-code review ...` | Native review workflow (`review mode status` shows whether it is on). |
| `brunnr-code telemetry status` | Show telemetry state; `telemetry disable` opts out. |
| `brunnr-code version` | Print the version. |

Every command supports `--help`.

## Build from source

```bash
go build -o brunnr-code ./cmd/brunnr-code
go test ./...
```

## License

MIT. See [LICENSE](LICENSE).
