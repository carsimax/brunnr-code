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
| T1 | S1-S4 | inline (no writer reason: one sequential unit) | done | `d8eeef82` (RED: `RunHook` undefined; GREEN 11/11), hardening `53e7134c` (RED: large write + GIT_DIR tests fail on `d8eeef82`) |
| T2 | S5-S6 | inline | done | `efc4f0f4` (RED: installer byte test + uninstall test) |
| T3 | S7 | inline | done | `d8eeef82` help, `efc4f0f4` README |
| V  | S1-S7 | delegated independent verifier (high risk) | done | `0c3abee1`: S1-S7 PASS, no blockers; review `review-f8c8c7f555c29a7a` (4 lenses) approved and acknowledged |

## Log

- **L1 (2026-10-13, user, verbatim):** "Comentario importante como retro, ya tenemos instalado brunnr-code la 0.3.0 y veo en esta sesion cosas importatnes: 1. Veo que no creaste la rama 2. No creaste el ODD 3. No delegaste el trabajo a agentes, todo lo estas haciendo inline. Debes revisar contra el fork de gentle ai por el si le funciona y a nosotros dejo de funcionae correctamente esto por que es compleramente al azar"
- **L2 (2026-10-13):** Explorer comparison: upstream gentle-ai HEAD `61125bb` and brunnr-code 0.3.0 share identical routing rules; branching/tracking/delegation depend on model judgment and no hook enforces them. Recommendation: a deterministic `PreToolUse` guard.
- **L3 (2026-10-13, user, verbatim):** "ya fusione a main para que hagas sync y empecemos con lo que sigue"; behavior choice recorded in S1.
- **L4 (2026-10-13):** Hook infrastructure map: retained hooks install via `agenthooks.InstallRetainedClaudeHooks` (`internal/components/agenthooks/claude.go:13`), shared by install and sync (`internal/cli/run.go:753`); ownership is the exact command string; uninstall walks `PreToolUse` already and needs the command in `managedRetainedHookCommand` (`internal/components/uninstall/service.go:897`). Classified large + high risk.
- **L5 (2026-10-13):** PreToolUse contract confirmed (https://code.claude.com/docs/en/hooks.md): exit 0 + `hookSpecificOutput.permissionDecision: "deny"`; exit 1 fails open.
- **L6 (2026-10-13):** T1 committed `d8eeef82`; assess `high_risk` (review due). Verifier at `d8eeef82`: S1-S4 PASS, no blockers; advisories: 4 MiB cap let a large Write through, inherited `GIT_DIR` overrode `-C`, relative path out of domain, `tool_name` unchecked (matcher scopes it), unborn `main` denied (consistent with S1). First two fixed in `53e7134c` with regression tests.
- **L7 (2026-10-13):** Full suite run 1 failed `TestNoGoSourceBindsALiteralRuntimeIdentityIntoPrintedCommands` (literal `--agent claude-code` in printed strings, #2440 guard) and `TestRunArgsRestoreUnknownIDReturnsError` (passed 3/3 in isolation, intermittent). Fixed literals; run 2: `scripts/test-fast.sh` exit 0.
- **L8 (2026-10-13, user, verbatim):** "lo que estamos haciendo resuelve el tema de que no se delega a subagentes?" — answered: no; the hook only enforces branching. Proposed a follow-up lookup-counter hook.
- **L9 (2026-10-13, user, verbatim):** "Y como lo hace gentle-ia para que funcione sin un hook? esta bien la idea de hacerlo asi pero por que gentle ai no lo hace y si lo pidemos portar de  https://github.com/Gentleman-Programming/gentle-ai eso inclute el trabajo en ramas, dale un ojo." — explorer auditing upstream branch mechanisms and post-fork commits.
- **L10 (2026-10-13):** Upstream audit: gentle-ai enforces neither branching nor delegation (prompt-only, thresholds advisory); fork lacks upstream `f0b6e477` "Authorized work continuity". User approved porting it in a separate PR after this one ("ok").
- **L11 (2026-10-13):** Verifier at `0c3abee1`: S1-S7 PASS, no blockers; sync diff vs baseline is only the new entry. Pre-existing advisories (not caused here): install over JSONC settings loses all hooks (baseline identical), uninstall drops JSONC comments, second install not byte-idempotent. Change-caused cosmetic: `hook branch-guard --help` exits 1.
- **L12 (2026-10-13):** Review `review-f8c8c7f555c29a7a` (risk, resilience, readability, reliability) approved and acknowledged. Informational follow-ups: deny reason omits the repository path; NotebookEdit `notebook_path` lacks a unit test; relative `file_path` resolves against the hook process cwd (Claude sends absolute paths); test git helper inherits GIT_* env; guard also covers non-project repos the agent edits.
- **Next:** open PR; follow-ups above; then port `f0b6e477` on its own branch.
