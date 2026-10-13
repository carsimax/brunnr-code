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
| `BRUNNR_CODE_VERSION` | Pin a release, for example `v0.1.0`. |
| `BRUNNR_CODE_INSTALL_DIR` | Install to another directory. |

See [`scripts/install.sh`](scripts/install.sh) for details.

### Update

`brunnr-code` does not update itself. Updating an installed machine takes two
steps:

```bash
# 1. Replace the binary with the latest release (or pin one with BRUNNR_CODE_VERSION)
curl -fsSL https://raw.githubusercontent.com/carsimax/brunnr-code/main/scripts/install.sh | bash

# 2. Re-apply the managed setup with the new binary
brunnr-code sync
```

Until you run `sync`, the agents keep the files the previous version wrote.

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
Engram memory, the persona, the permissions overlay and nine skills
(judgment-day, go-testing, debugging, skill-creator, skill-improver,
skill-registry, chained-pr, cognitive-doc-design, work-unit-commits). Only the
agents are selectable.

For Claude Code they also install a status line that shows the `brunnr-code`
version. It needs `zsh` and `jq`. A `statusLine` you already configured is
left alone, and so is a status line script you edited.

The files of those nine skills belong to the installer: every `install` and
`sync` rewrites them, so local edits are lost. To customize one, copy it under a
skill ID of your own.

## Build from source

```bash
go build -o brunnr-code ./cmd/brunnr-code
go test ./...
```

## License

MIT. See [LICENSE](LICENSE).
