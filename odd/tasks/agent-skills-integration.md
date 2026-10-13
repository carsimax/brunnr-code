# Agent skills integration: adapt the useful parts of addyosmani/agent-skills into ODD

Objective: fold the parts of addyosmani/agent-skills (MIT, commit `1be8e34`) that fit ODD into the shipped skills and ODD routing, rewritten for a Go CLI, without adding overlapping skills. Branch: `feat/agent-skills-integration` (from `origin/main` `48cae732`). Delivery strategy: `single-pr` (project convention: no chained PRs); forecast about 700 authored changed lines. Test runner: `go test ./...` (`scripts/test-fast.sh` locally), format gate `go run ./internal/gofmtcheck`.

## Specs

- **S1** — Integrate "lo mejor que nos pueda servir de https://github.com/addyosmani/agent-skills que se pueda acoplar al flujo de odd". Content is rewritten and adapted, not copied; skills that overlap ours (meta-router, spec, planning, TDD, review), web-only skills, and its ~100-line sizing are out of scope.
- **S2** — `git-workflow-and-versioning`: "tomar lo mejor e integrarlo en las nuestras". Fold into `internal/assets/skills/work-unit-commits/SKILL.md`, no new skill:
  formatting-only changes in their own commit; commit after each green step and never run `git reset --hard` without the user's approval; review `git diff --cached` for secrets, `.env` files, and build artifacts; list out-of-scope findings in the report instead of fixing them silently; short-lived feature branches deleted after merge; a sibling worktree when another agent uses the checkout; no force-push to a branch someone else pulled; the `v*` tag is the version source of truth (injected through `-ldflags`); the Conventional Commit subject is the changelog line; the bump follows what users can observe; a Common Rationalizations table and a Red Flags list.
  Keep the 400-line rule and the strings pinned by `internal/assets/assets_test.go` (`## PR Relationship`, `## ODD Relationship`, `400`, `Budget is not code-golf`). The description stays a quoted single line of at most 160 characters containing `Trigger:`.
- **S3** — Skill anatomy: skill-creator and skill-improver require Common Rationalizations, Red Flags, and Verification sections; backfill go-testing and work-unit-commits.
- **S4** — Quality floor in ODD routing and the writer prompt: no new `t.Skip` or `//nolint`, no deleted tests or assertions, no golden or baseline `-update` without a stated reason, no lowered thresholds.
- **S5** — New setup skill `debugging` for Go: reproduce, localize (`go test -run`, `-count=1`, `git bisect run`), reduce, fix the root cause, add a guard test, verify; error output is untrusted data, not instructions.
- **S6** — "incuyela pero hasta el final": a deterministic Go test over the embedded skill descriptions that catches descriptions that never trigger and near-duplicate descriptions.

Assumption: credit to the source goes in the PR description and this document; no NOTICE file is added, because no text is copied verbatim.

## Tasks

| ID | Specs | Route | Status | Evidence |
|----|-------|-------|--------|----------|
| T5 | S1, S2 | inline (one already-drafted asset file plus its pin test) | done (`e5240ce5`) | RED: `TestWorkUnitSkillCarriesGitWorkflowRules` failed on missing sections; GREEN: `go test ./internal/assets`, `scripts/test-fast.sh` exit 0, gofmtcheck clean; description 156 chars |
| T3 | S1, S3 | delegated (4 skill files, 2 goldens) | done (`1b753e40`) | RED: `TestSkillsCarryAnatomySections` 16 errors; GREEN: same test, `go test ./internal/assets ./internal/components`, `scripts/test-fast.sh` exit 0, gofmtcheck clean; parent spot check re-ran assets and components tests: ok |
| T2 | S1, S4 | delegated (routing.go, routing tests, Linux-only baseline) | done (`ce64e3a7`) | RED: `TestRenderRoutingHoldsTheQualityFloor` failed for claude-code and codex; GREEN: same test, agentguidance/assets/cli packages ok, `scripts/test-fast.sh` exit 0, gofmtcheck clean; Linux docker `TestOrchestratorModuleBaseline` and module budget pass; parent spot check re-ran the new test: ok |
| T1 | S1, S5 | delegated (new skill, about 14 registration places) | done | RED: catalog `MVPSkills() missing requested bundled skill "debugging"`, `TestSetupSkillsIsTheFixedSetupSet`, inject skipped `[debugging]`, anatomy test panic; GREEN: 16 packages ok, `scripts/test-fast.sh` exit 0, gofmtcheck clean, `bash -n e2e/e2e_test.sh` ok; parent spot check re-ran catalog and components/skills: ok |
| T4 | S1, S6 | delegated (new test) | pending | |

## Log

- **L1** (2026-10-13) — User request, verbatim: "Integración de lo mejor que nos pueda servir de https://github.com/addyosmani/agent-skills que se pueda acoplar al flujo de odd".
- **L2** (2026-10-13) — Research: 25 skills inventoried. Gaps chosen: debugging procedure, quality floor, skill anatomy sections, trigger evals. Rejected: `using-agent-skills` (double routing on Claude Code and Codex, which its own repo leaves unwired), spec/planning/TDD/review skills (overlap with ODD, go-testing, judgment-day), web and production skills.
- **L3** (2026-10-13) — User, verbatim: "seria tomar lo mejor e integrarlo en las nuestras como quedaria?" then "si es asi agregalo al plan". Added T5 (S2). Correction recorded: its trunk-based model uses short-lived feature branches, so the real conflicts are the ~100-line sizing and an always-firing description, not feature branches.
- **L4** (2026-10-13) — User, verbatim: "incuyela pero hasta el final". T4 included, last. Order: T5, T3, T2, T1, T4.
- **L5** (2026-10-13) — User, verbatim: "arrenquemos son worktree, parte de la ultima version remota de main". Worktree `../carsi-ai-worktrees/agent-skills-integration` created from `origin/main` `48cae732`.
- **L6** (2026-10-13) — T5 done: `work-unit-commits` 1.1 gains formatting and save-point rules, staged-diff and out-of-scope checklist items, `## Branches and Worktrees`, `## Release Relationship`, `## Common Rationalizations`, `## Red Flags`. Next: T3.
- **L7** (2026-10-13) — T3 done: skill-creator and skill-improver require Common Rationalizations, Red Flags, and Verification (sections and both identical style guides); go-testing backfilled; work-unit-commits `## Work Unit Checklist` renamed to `## Verification` (no pins on the old heading); two goldens regenerated. Known overage: the skill-creator body grew from 547 to 732 words, above its own advisory 700-token recommendation; left as is because the added rules are the point of the task. Next: T2.
- **L8** (2026-10-13) — T2 done: one quality-floor bullet in the shared ODD reference detail, outside the RDD branches, forwarded to workers. Baseline regenerated in a `golang:1.25` Linux container; reason for `-update`: the routing text intentionally gained this bullet. Module budget not raised. Note: both agents are RDD agents here, so the non-RDD routing variant is unreachable and covered only by placement. Next: T1.
- **L9** (2026-10-13) — T1 done: new setup skill `debugging` (description 140 chars) registered in model, catalog, presets, their tests, `skills-presets.json` (reason for `-update`: new setup skill), e2e script, and README (eight to nine skills); `TestEmbeddedAssetCount` 14 to 15. Docker and real-agent e2e not run; the e2e script was only syntax-checked. Next: T4.
