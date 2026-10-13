# Ponytail integration: adapt the useful parts of dietrichgebert/ponytail into ODD

Objective: fold the parts of dietrichgebert/ponytail (MIT, v5.2.0, commit `2961b52`) that ODD lacks into the shipped output style, persona, and skills, rewritten for this project, without its hooks or modes. Branch: `feat/ponytail-integration` (from `origin/main` `9a13025c`). Delivery strategy: `single-pr` (project convention: no chained PRs); forecast about 150 authored changed lines. Test runner: `go test ./...` (`scripts/test-fast.sh` locally), format gate `go run ./internal/gofmtcheck`.

## Specs

- **S1** — Integrate "lo mejor que nos pueda servir de https://github.com/dietrichgebert/ponytail que se pueda acoplar al flujo de odd". Content is rewritten, not copied. Out of scope: hooks (SessionStart, UserPromptSubmit, SubagentStart), lite/full/ultra levels, statusline nudge, regex codebase map, gain and help skills, "shortest working diff wins" (conflicts with "Budget is not code-golf"), "an assert self-check instead of a test" (conflicts with test-first), and benchmark claims.
- **S2** — Output style and persona (`internal/assets/claude/output-style-principal-engineer.md`, `internal/assets/generic/persona.md`):
  - when the reply follows a change or a verification, end with one line naming what was skipped or not checked and any risk the user must know;
  - simplicity refinements: never add a dependency for a few lines; a one-liner that needs decoding is not short; between options of equal size, take the one correct on edge cases;
  - a known limit left on purpose is marked in code as `shortcut: <the limit>, <when to upgrade>`.
- **S3** — Shortcut ledger: `work-unit-commits` lists `shortcut:` markers with `git grep` when closing work; `judgment-day` treats a marked shortcut as a recorded decision, not a finding, unless the stated limit is already crossed.
- **S4** — `judgment-day` finding format: each finding states what it is, the problem, the fix, and the cost of skipping it; no concrete failing case means no finding; findings are numbered under Must, Should, and Nice; the report ends with `Verdict:` and `Not checked:`.

Assumption: credit to the source goes in the PR description and this document; no NOTICE file is added, because no substantial text is copied.

## Tasks

| ID | Specs | Route | Status | Evidence |
|----|-------|-------|--------|----------|
| T1 | S1, S2 | delegated (two asset files, their wording tests and goldens) | done (`a4fab5f9`) | RED: `TestPersonaChannelsCarryClosingLineAndSimplicityRefinements` failed, five rules missing in both assets; GREEN: same test, assets/components/cli ok, `scripts/test-fast.sh` exit 0, gofmtcheck clean; parent spot check: new test ok, and on Linux `TestOrchestratorModuleBaseline` and the budget tests pass unchanged |
| T2 | S1, S3 | delegated (two skills and their tests) | done | RED: `TestSkillsCarryShortcutLedger` 8 phrases missing; GREEN: same test, assets/components ok (trigger and duplicate tests included), `scripts/test-fast.sh` exit 0, gofmtcheck clean; ledger command run against `origin/main`; parent spot check: ok |
| T3 | S1, S4 | delegated (judgment-day and its tests) | pending | |

## Log

- **L1** (2026-10-13) — User request, verbatim: "Integración de lo mejor que nos pueda servir de https://github.com/dietrichgebert/ponytail que se pueda acoplar al flujo de odd".
- **L2** (2026-10-13) — Research: the decision ladder is already paraphrased in our output style and persona (added 2026-10-12); only "Be lazy about the solution, never about the change" is near-identical, which is not a substantial portion. Gaps chosen: closing not-checked/risk line, three ladder refinements, the `shortcut:` marker and ledger, and a stricter review finding format. The installed ponytail plugin's SubagentStart hook injects its rules into every subagent in the user's environment; that is environment, not repository, scope.
- **L3** (2026-10-13) — User, verbatim: "ok vamos con ello". Worktree `../carsi-ai-worktrees/ponytail-integration` created from `origin/main` `9a13025c`.
- **L4** (2026-10-13) — T1 done: the output style gains four Simplicity bullets and a Response Contract closing line; the persona gains the same rules tersely (it has no ordered preference list, so the list was not copied there). Only `persona-claude-principal-engineer-outputstyle.golden` changed (reason for `-update`: the output style intentionally gained these rules). Next: T2.
- **L5** (2026-10-13) — T2 done: work-unit-commits Verification and Commands gain the shortcut ledger (`git diff -U0 <base> | grep '^+.*shortcut:'`, which also covers the commit being prepared, plus `git grep -n 'shortcut:'` for locations); judgment-day Hard Rules and the judge prompt in `references/prompts-and-formats.md` treat a marked shortcut as a decision unless its limit is crossed or the marker is incomplete. The jd-judge agents defer to that prompt, so they were not edited. Next: T3.
