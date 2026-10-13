# Feature: default-branch-write-guard

Objective: a brunnr-code-managed Claude Code `PreToolUse` hook denies file edits while the repository is on its default branch, so the agent must branch before its first write instead of relying on model judgment.
Branch: `feat/default-branch-write-guard`. Delivery: `ask-on-risk` (forecast ~350 authored lines, single PR). Test runner: `go test` (`scripts/test-fast.sh` for the full suite). Risk: high (item 5 installer, item 2 guard).

## Specs

- **S1 — Behavior.** User decision (2026-10-13), verbatim option chosen: "Bloquear (Recommended)" — "Rechaza Edit/Write con un mensaje que le pide al agente crear una rama primero. Determinista; el agente no puede saltarlo, pero los cambios triviales en main también requieren rama."
  - When a Claude Code `Edit`, `Write`, `MultiEdit` or `NotebookEdit` call runs inside a git work tree whose current branch is the default branch, the hook denies it with a reason telling the agent to create a feature branch first.
  - Assumption: `Bash` is not matched (mutating shell detection is heuristic); revisit if the user asks.
- **S2 — Allowed cases (assumption, fail-open).** The hook allows the call when: the target is outside any git work tree, HEAD is detached, the current branch is not the default branch, or the hook cannot decide (git missing, malformed payload). A guard that blocks on its own failure would stop all work.
- **S3 — Default branch detection (assumption).** Resolve from `refs/remotes/origin/HEAD`; otherwise `init.defaultBranch`; otherwise treat `main` and `master` as default. No network access.
- **S4 — Hook command.** A new subcommand `brunnr-code hook branch-guard --agent claude-code` reads the PreToolUse JSON from stdin and writes the PreToolUse decision JSON to stdout, exit 0. It uses the payload `cwd` (and the tool's `file_path` directory when present) to locate the repository.
- **S5 — Install and sync.** `install` and `sync` add the `PreToolUse` entry (matcher `Edit|Write|MultiEdit|NotebookEdit`) to Claude settings through the existing retained-hooks path, append-only and deduplicated by exact command, preserving user hooks and JSONC bytes.
- **S6 — Uninstall.** Full Claude uninstall removes the new command like the other retained hooks.
- **S7 — Docs.** Help text and docs mention the new hook command and what it blocks.

## Tasks

| ID | Specs | Route | Status | Evidence |
|----|-------|-------|--------|----------|
| T1 | S1-S4 | inline (no writer reason: one sequential unit) | pending | |
| T2 | S5-S6 | inline | pending | |
| T3 | S7 | inline | pending | |
| V  | S1-S7 | delegated independent verifier (high risk) | pending | |

## Log

- **L1 (2026-10-13, user, verbatim):** "Comentario importante como retro, ya tenemos instalado brunnr-code la 0.3.0 y veo en esta sesion cosas importatnes: 1. Veo que no creaste la rama 2. No creaste el ODD 3. No delegaste el trabajo a agentes, todo lo estas haciendo inline. Debes revisar contra el fork de gentle ai por el si le funciona y a nosotros dejo de funcionae correctamente esto por que es compleramente al azar"
- **L2 (2026-10-13):** Explorer comparison: upstream gentle-ai HEAD `61125bb` and brunnr-code 0.3.0 share identical routing rules; branching/tracking/delegation depend on model judgment and no hook enforces them. Recommendation: a deterministic `PreToolUse` guard.
- **L3 (2026-10-13, user, verbatim):** "ya fusione a main para que hagas sync y empecemos con lo que sigue"; behavior choice recorded in S1.
- **L4 (2026-10-13):** Hook infrastructure map: retained hooks install via `agenthooks.InstallRetainedClaudeHooks` (`internal/components/agenthooks/claude.go:13`), shared by install and sync (`internal/cli/run.go:753`); ownership is the exact command string; uninstall walks `PreToolUse` already and needs the command in `managedRetainedHookCommand` (`internal/components/uninstall/service.go:897`). Classified large + high risk.
- **Next:** T1 after confirming the PreToolUse deny JSON contract against Claude Code docs.
