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

To update `brunnr-code`, re-run the install script. `brunnr-code` does not
update itself.

## Usage

Run `brunnr-code` with no arguments to open the interactive TUI, or use a command:

| Command | What it does |
|---------|--------------|
| `brunnr-code install` | Configure agents on this machine (`--agents`, `--dry-run`). |
| `brunnr-code sync` | Re-apply the setup to the current version. |
| `brunnr-code uninstall` | Remove managed files from this machine. |
| `brunnr-code doctor` | Run health diagnostics. |
| `brunnr-code update` / `upgrade` | Check for Engram updates / apply them. |
| `brunnr-code restore` | Restore a config backup. |
| `brunnr-code review ...` | Native review workflow (`review mode status` shows whether it is on). |
| `brunnr-code version` | Print the version. |

Every command supports `--help`.

There is one setup, Dev Stack + Permissions. `install` and `sync` always apply
Engram memory, the persona, the permissions overlay and eight skills
(judgment-day, go-testing, skill-creator, skill-improver, skill-registry,
chained-pr, cognitive-doc-design, work-unit-commits). Only the agents are
selectable.

## Build from source

```bash
go build -o brunnr-code ./cmd/brunnr-code
go test ./...
```

## License

MIT. See [LICENSE](LICENSE).
